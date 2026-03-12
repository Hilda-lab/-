# 库存一致性修复验证文档

## 修复内容总结

### 1. 商品服务回滚逻辑改进 (product_service/main.go)

**问题**：
- 原代码只回滚库存，不回滚用户购买记录
- 导致用户即使订单失败，也无法再次购买

**修复**：
- 使用Lua脚本原子性地回滚库存和用户购买记录
- 确保回滚操作不会破坏数据一致性
- 智能处理用户购买记录（归零时自动删除hash字段）

```lua
-- 回滚Lua脚本
local stock = redis.call("INCRBY", KEYS[1], ARGV[1])
if ARGV[2] and ARGV[2] ~= "" and ARGV[2] ~= "0" then
    local current = tonumber(redis.call('hget', KEYS[2], ARGV[2])) or 0
    local new_count = math.max(0, current - tonumber(ARGV[1]))
    if new_count == 0 then
        redis.call('hdel', KEYS[2], ARGV[2])
    else
        redis.call('hset', KEYS[2], ARGV[2], new_count)
    end
end
return stock
```

### 2. 订单服务回滚重试机制 (order_service/main.go)

**问题**：
- 回滚失败时没有重试，导致库存永久丢失
- 没有传递UserId，无法回滚用户购买记录

**修复**：
- 添加3次重试机制，指数退避策略（500ms, 1s, 1.5s）
- 传递UserId给回滚接口
- 增强错误日志，包含关键信息便于人工介入

```go
// 重试逻辑
maxRetries := 3
for i := 0; i < maxRetries; i++ {
    rollbackCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    _, errRb := productClient.RollbackStock(rollbackCtx, &pb.DeductStockRequest{
        ProductId: req.ProductId,
        Count:     req.Count,
        UserId:    req.UserId,  // 关键：传递UserId
    })
    cancel()
    
    if errRb == nil {
        break  // 成功则退出
    }
    if i < maxRetries-1 {
        time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)  // 指数退避
    }
}
```

## 修复后的优势

### ✅ 原子性保障
- 库存和用户购买记录同时回滚，不会出现不一致
- Lua脚本在Redis中原子执行，无竞态条件

### ✅ 高可靠性
- 重试机制大幅降低回滚失败概率
- 指数退避策略避免雪崩

### ✅ 完整性保证
- 用户购买记录被正确清除，允许用户重新购买
- 避免"少卖"问题（库存丢失）

### ✅ 可观测性
- 详细的日志记录，便于问题追踪
- CRITICAL级别日志提醒人工介入

## 测试验证步骤

### 1. 单元测试 - Redis Lua脚本
```bash
# 测试回滚脚本的原子性
redis-cli -a 123456
> SET product:stock:1 100
> HSET product:users:1 10001 5
> EVAL "..." 2 product:stock:1 product:users:1 5 10001
# 预期：库存变105，用户购买记录变0并删除
```

### 2. 集成测试 - 模拟MQ失败
```bash
# 启动所有服务
docker-compose up -d
go run product_service/main.go &
go run order_service/main.go &

# 模拟MQ故障（停止RabbitMQ）
docker stop rabbitmq

# 发送秒杀请求，观察回滚日志
curl -X POST http://localhost:8080/order \
  -H "Authorization: Bearer <token>" \
  -d '{"product_id":1,"count":1}'

# 检查Redis库存是否正确回滚
redis-cli -a 123456 GET product:stock:1
redis-cli -a 123456 HGETALL product:users:1
```

### 3. 压力测试 - 并发场景
```bash
# 运行压测工具
cd stress_test
go run main.go

# 验证最终一致性：
# 成功订单数 + Redis库存 = 初始库存
```

## 预期结果

1. **MQ发送失败时**：
   - 日志显示重试过程
   - 库存和用户购买记录都被回滚
   - 用户可以重新发起购买

2. **回滚全部失败时**：
   - 输出CRITICAL级别日志
   - 包含商品ID、数量、用户ID等关键信息
   - 运维人员可根据日志手动修复

3. **并发场景**：
   - 无超卖（库存不为负）
   - 无少卖（回滚成功率高）
   - 用户限购正常工作

## 下一步优化建议

1. **持久化用户购买记录**
   - 当前仅在Redis中，重启会丢失
   - 建议在订单成功后写入MySQL

2. **死信队列监控**
   - 添加告警机制，检测死信队列堆积
   - 自动重试死信消息

3. **分布式事务框架**
   - 考虑引入Saga或TCC框架
   - 提供更完善的补偿机制

4. **监控指标**
   - 回滚成功率
   - 回滚延迟
   - 库存不一致告警
