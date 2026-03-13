#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

if ! command -v docker >/dev/null 2>&1; then
  echo "docker 未安装或不在 PATH 中"
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "go 未安装或不在 PATH 中"
  exit 1
fi

BASE="${BASE:-http://127.0.0.1:18080}"
PRODUCT_ID="${PRODUCT_ID:-1}"
REDIS_PASS="${REDIS_PASS:-123456}"
MYSQL_PASS="${MYSQL_PASS:-123456}"
RESET_BEFORE_RUN="${RESET_BEFORE_RUN:-1}"
WAIT_DRAIN_SECONDS="${WAIT_DRAIN_SECONDS:-60}"
WAIT_DRAIN_INTERVAL="${WAIT_DRAIN_INTERVAL:-2}"

if [[ "$RESET_BEFORE_RUN" == "1" ]]; then
  echo "[benchmark] 重置环境中 (/dev/reset + 清空队列)..."
  curl -s -X POST http://127.0.0.1:9091/dev/reset >/dev/null || {
    echo "调用 /dev/reset 失败，请确认 product_service 正在运行 (9091)"
    exit 1
  }
  docker exec rabbitmq rabbitmqctl purge_queue seckill_order_queue >/dev/null || true
  docker exec rabbitmq rabbitmqctl purge_queue dead_queue >/dev/null || true
fi

# 1) 基线（压测前）
init_stock=$(docker exec redis-seckill redis-cli -a "$REDIS_PASS" GET "product:stock:${PRODUCT_ID}" 2>/dev/null | tail -n1 | tr -d '\r')
start_orders=$(docker exec mysql-seckill mysql -N -s -uroot -p"$MYSQL_PASS" -Dseckill -e "SELECT COUNT(*) FROM orders;" 2>/dev/null | tr -d '\r')

if [[ -z "${init_stock}" || -z "${start_orders}" ]]; then
  echo "无法获取基线数据，请确认 redis/mysql 容器已启动且可访问"
  exit 1
fi

if [[ "$init_stock" -le 0 ]]; then
  echo "基线库存为 ${init_stock}，本次压测将全部失败。"
  echo "请先重置环境后再跑：curl -X POST http://127.0.0.1:9091/dev/reset"
  echo "或使用：RESET_BEFORE_RUN=1 bash ./scripts/benchmark.sh"
  exit 1
fi

start_ms=$(date +%s%3N)

# 2) 压测
out=$(go run ./stress_test/main.go 2>&1)
echo "$out"

end_ms=$(date +%s%3N)
duration_ms=$((end_ms - start_ms))

# 3) 解析压测输出
total=$(echo "$out"   | awk -F': ' '/总请求数/{print $2}' | tail -n1)
success=$(echo "$out" | awk -F': ' '/成功:/{print $2}'   | tail -n1)
fail=$(echo "$out"    | awk -F': ' '/失败:/{print $2}'   | tail -n1)
request_tps_raw=$(echo "$out" | awk -F': ' '/请求TPS/{print $2}' | tail -n1)
success_tps_raw=$(echo "$out" | awk -F': ' '/成功TPS/{print $2}' | tail -n1)
p95_ms=$(echo "$out" | awk -F': ' '/P95延迟\(ms\)/{print $2}' | tail -n1)
p99_ms=$(echo "$out" | awk -F': ' '/P99延迟\(ms\)/{print $2}' | tail -n1)
avg_ms=$(echo "$out" | awk -F': ' '/平均延迟\(ms\)/{print $2}' | tail -n1)
fail_breakdown=$(echo "$out" | awk -F': ' '/失败分类/{print $2}' | tail -n1)

if [[ -z "${total}" || -z "${success}" || -z "${fail}" ]]; then
  echo "无法解析压测输出，请检查 stress_test/main.go 输出格式"
  exit 1
fi

# 4) 采样（压测后）
queue_line=$(docker exec rabbitmq rabbitmqctl list_queues name messages consumers 2>/dev/null | awk '/seckill_order_queue/ {print $0}')
queue_msg=$(echo "$queue_line" | awk '{print $2}')
queue_consumer=$(echo "$queue_line" | awk '{print $3}')
dead_queue_msg=$(docker exec rabbitmq rabbitmqctl list_queues name messages 2>/dev/null | awk '/dead_queue/ {print $2}' | tail -n1)

if [[ -n "${queue_msg:-}" && "${queue_msg}" =~ ^[0-9]+$ && "${queue_msg}" -gt 0 ]]; then
  echo "[benchmark] 检测到队列积压=${queue_msg}，等待消费者清空（最多 ${WAIT_DRAIN_SECONDS}s）..."
  elapsed=0
  while [[ "$elapsed" -lt "$WAIT_DRAIN_SECONDS" ]]; do
    sleep "$WAIT_DRAIN_INTERVAL"
    elapsed=$((elapsed + WAIT_DRAIN_INTERVAL))

    queue_line=$(docker exec rabbitmq rabbitmqctl list_queues name messages consumers 2>/dev/null | awk '/seckill_order_queue/ {print $0}')
    queue_msg=$(echo "$queue_line" | awk '{print $2}')
    queue_consumer=$(echo "$queue_line" | awk '{print $3}')

    if [[ -n "${queue_msg:-}" && "${queue_msg}" =~ ^[0-9]+$ && "${queue_msg}" -eq 0 ]]; then
      echo "[benchmark] 队列已清空，用时 ${elapsed}s"
      break
    fi
  done

  if [[ -n "${queue_msg:-}" && "${queue_msg}" =~ ^[0-9]+$ && "${queue_msg}" -gt 0 ]]; then
    echo "[benchmark] 超时仍有积压=${queue_msg}，将按当前状态输出结果（可能低估落库TPS）"
  fi
fi

end_orders=$(docker exec mysql-seckill mysql -N -s -uroot -p"$MYSQL_PASS" -Dseckill -e "SELECT COUNT(*) FROM orders;" 2>/dev/null | tr -d '\r')
distinct_orders=$(docker exec mysql-seckill mysql -N -s -uroot -p"$MYSQL_PASS" -Dseckill -e "SELECT COUNT(DISTINCT order_id) FROM orders;" 2>/dev/null | tr -d '\r')
end_stock=$(docker exec redis-seckill redis-cli -a "$REDIS_PASS" GET "product:stock:${PRODUCT_ID}" 2>/dev/null | tail -n1 | tr -d '\r')

if [[ -z "${end_orders}" || -z "${end_stock}" || -z "${distinct_orders}" ]]; then
  echo "无法获取压测后采样数据"
  exit 1
fi

delta_orders=$((end_orders - start_orders))
consistency_gap=$((init_stock - end_stock - delta_orders))

duration_sec=$(awk -v ms="$duration_ms" 'BEGIN{printf "%.3f", ms/1000}')
tps=$(awk -v o="$delta_orders" -v d="$duration_ms" 'BEGIN{ if (d==0) print "0.00"; else printf "%.2f", o/(d/1000) }')
success_rate=$(awk -v s="$success" -v t="$total" 'BEGIN{ if (t==0) print "0.00%"; else printf "%.2f%%", (s/t)*100 }')
unique_order_rate=$(awk -v d="$distinct_orders" -v e="$end_orders" 'BEGIN{ if (e==0) print "0.00%"; else printf "%.2f%%", (d/e)*100 }')

request_tps_show="${request_tps_raw:-N/A}"
success_tps_show="${success_tps_raw:-N/A}"
p95_show="${p95_ms:-N/A}"
p99_show="${p99_ms:-N/A}"
avg_show="${avg_ms:-N/A}"
fail_breakdown_show="${fail_breakdown:-N/A}"

echo
echo "===== 压测量化结果 ====="
echo "总请求数:      ${total}"
echo "成功请求数:    ${success}"
echo "失败请求数:    ${fail}"
echo "成功率:        ${success_rate}"
echo "压测耗时:      ${duration_sec}s"
echo "请求TPS(入口): ${request_tps_show}"
echo "成功TPS(入口): ${success_tps_show}"
echo "落库订单增量:  ${delta_orders}"
echo "TPS(按落库):   ${tps}"
echo "平均延迟(ms):  ${avg_show}"
echo "P95延迟(ms):   ${p95_show}"
echo "P99延迟(ms):   ${p99_show}"
echo "失败分类:      ${fail_breakdown_show}"
echo "初始库存:      ${init_stock}"
echo "当前库存:      ${end_stock}"
echo "队列积压:      ${queue_msg:-N/A}"
echo "死信队列:      ${dead_queue_msg:-N/A}"
echo "消费者数量:    ${queue_consumer:-N/A}"
echo "唯一订单率:    ${unique_order_rate}"
echo "一致性差值:    ${consistency_gap}   (理想=0)"
echo "========================"
