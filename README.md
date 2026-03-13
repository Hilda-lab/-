# 秒杀系统

基于 Go 的秒杀示例项目，采用 **API Gateway + gRPC 微服务 + Redis + RabbitMQ + MySQL + Etcd** 架构，核心目标是：

- 高并发入口快速响应
- 避免超卖（Redis + Lua 原子扣减）
- 异步落库（MQ 削峰）
- 异常可补偿（MQ 失败回滚库存）
- 最终一致性可量化验证（benchmark 脚本）

---

## 1. 当前架构

### 1.1 服务与职责

- `api_gateway`：HTTP 入口（Gin），JWT 鉴权、Sentinel 限流、转发 gRPC 调用
- `product_service`：商品查询、库存扣减、库存回滚（Redis Lua）
- `order_service`：下单编排（扣库存 -> 发 MQ），MQ 失败补偿回滚
- `mq_consumer`：消费订单消息并写 MySQL，支持断线重连与幂等处理

### 1.2 基础设施

- MySQL：订单持久化
- Redis：库存与用户购买记录
- RabbitMQ：异步队列 + 死信队列
- Etcd：服务注册与发现
- Jaeger：链路追踪
- Prometheus + Grafana：监控

### 1.3 关键一致性链路

1. 网关调用 `order_service` 下单
2. `order_service` 先调用 `product_service` 扣 Redis 库存（Lua 原子）
3. 扣减成功后发 MQ 消息
4. `mq_consumer` 消费后写入 MySQL
5. 若 MQ 发布失败：`order_service` 触发库存回滚（含用户购买记录回滚）

---

## 2. 目录结构

```text
api_gateway/      # 网关
product_service/  # 商品服务（库存）
order_service/    # 订单服务（下单编排）
mq_consumer/      # MQ 消费落库
stress_test/      # 并发压测程序
scripts/          # benchmark 自动化脚本
common/           # 配置/公共库/proto生成代码
config/           # 服务配置
proto/            # protobuf 定义
deploy/           # prometheus 配置
docker-compose.yaml
init.sql
```

---

## 3. 运行环境

- Go `1.25+`
- Docker / Docker Compose
- Windows、Linux、macOS 均可（脚本主要为 bash）

---

## 4. 默认端口（当前代码）

### 4.1 业务服务

- API Gateway: `18080`
- product_service gRPC: `50051`
- order_service gRPC: `50052`
- product_service metrics + dev reset: `9091`
- order_service metrics: `9092`
- mq_consumer metrics（配置项）：`9093`

### 4.2 中间件（docker 映射到宿主机）

- MySQL: `3307`
- Redis: `6379`
- RabbitMQ: `5672`
- RabbitMQ 管理台: `15672`
- Etcd: `23790`
- Jaeger UI: `16686`
- Prometheus: `9090`
- Grafana: `3000`

---

## 5. 快速启动

### 5.1 启动基础设施

```bash
docker compose up -d
docker compose ps
```

### 5.2 启动 4 个核心进程（建议 4 个终端）

```bash
go run ./product_service/main.go
go run ./order_service/main.go
go run ./mq_consumer/main.go
go run ./api_gateway/main.go
```

> 说明：`product_service` 在 debug 模式会暴露 `POST /dev/reset`（`9091`）用于测试重置。

---

## 6. 接口说明（网关）

### 6.1 登录获取 Token

`POST /login`

请求体：

```json
{
  "user_id": 10001
}
```

### 6.2 查询商品

`GET /product/:id`

### 6.3 下单

`POST /order`

请求头：

```text
Authorization: Bearer <token>
```

请求体：

```json
{
  "product_id": 1,
  "count": 1
}
```

---

## 7. 一键压测与一致性验证

项目已提供自动化脚本：`scripts/benchmark.sh`

```bash
bash ./scripts/benchmark.sh
```

脚本会执行以下动作：

1. （默认）调用 `POST http://127.0.0.1:9091/dev/reset` 重置环境
2. 清空 RabbitMQ 主队列与死信队列
3. 执行 `go run ./stress_test/main.go`
4. 等待队列清空后采样 MySQL/Redis
5. 输出量化指标

### 7.1 关键指标解释

- 请求TPS(入口)：HTTP 接口吞吐
- 成功TPS(入口)：成功请求吞吐
- TPS(按落库)：按 MySQL 新增订单计算吞吐
- P95/P99 延迟：尾延迟
- 死信队列：消费失败并进入 DLQ 的消息数
- 唯一订单率：`COUNT(DISTINCT order_id) / COUNT(*)`
- 一致性差值：`初始库存 - 当前库存 - 落库增量`（理想值 = `0`）

---

## 8. 配置文件说明

- `config/gateway.yaml`
  - 网关端口 `18080`
  - Etcd 地址 `127.0.0.1:23790`
- `config/product.yaml`
  - MySQL DSN 默认 `127.0.0.1:3307`
  - Redis DB `0`
- `config/order.yaml`
  - MySQL DSN 默认 `127.0.0.1:3307`
  - Redis DB `1`
  - Etcd 地址 `127.0.0.1:23790`
- `config/mq.yaml`
  - MySQL DSN 默认 `127.0.0.1:3307`

---

## 9. 观测入口

- Prometheus: `http://127.0.0.1:9090`
- Grafana: `http://127.0.0.1:3000`
- Jaeger: `http://127.0.0.1:16686`
- RabbitMQ 管理台: `http://127.0.0.1:15672`

---

## 10. 已实现的稳定性能力（当前版本）

- Redis Lua 原子扣减库存，防超卖
- 回滚逻辑同时恢复库存与用户购买记录
- MQ 发布失败后自动重连重试
- MQ 消费端守护循环 + 断线自动重连
- 订单落库幂等（重复消息不会重复插入）
- benchmark 脚本支持端到端一致性量化

---

## 11. 常见问题

### 11.1 全部请求都失败

请先检查是否已重置库存：

```bash
curl -X POST http://127.0.0.1:9091/dev/reset
```

### 11.2 网关无法访问

- 检查 `api_gateway` 是否已启动
- 确认端口为 `18080`（不是 `8080`）

### 11.3 MySQL 连接失败

- 当前默认端口是 `3307`
- 检查 `docker compose ps` 是否显示 `3307->3306`

---

## 12. 建议开发流程

1. 启动基础设施
2. 启动 4 个核心服务
3. 执行 `bash ./scripts/benchmark.sh`
4. 关注一致性差值、死信、P99 等关键指标

如果你打算继续演进该项目，建议下一步补充：readiness/liveness、告警阈值、自动化 CI 压测基线。
