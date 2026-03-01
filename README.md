# ⚡ Go-Seckill-Mall (高并发秒杀微服务系统)

![Go](https://img.shields.io/badge/Go-1.20%2B-blue) ![Gin](https://img.shields.io/badge/Gin-Framework-lightgrey) ![gRPC](https://img.shields.io/badge/gRPC-Microservices-red) ![Sentinel](https://img.shields.io/badge/Sentinel-FlowControl-green)

基于 Go 语言构建的微服务架构秒杀系统。项目采用 **DDD (领域驱动设计)** 思想，解决了高并发场景下的**超卖**、**少卖**、**流量削峰**以及**分布式事务一致性**等核心难题。

## 🏗️ 系统架构

![Architecture](https://via.placeholder.com/800x400?text=Client+->+Gateway+->+Redis(Lua)+->+MQ+->+Order+Service+->+MySQL)

* **API Gateway**: 基于 Gin + Sentinel 的流量入口，负责鉴权与限流。
* **Product Service**: 提供商品管理与库存扣减服务 (gRPC)。
* **Order Service**: 负责订单创建与异步落库 (gRPC + MQ Consumer)。
* **Middleware**: Etcd (服务发现), RabbitMQ (削峰), Redis (缓存), Jaeger (链路追踪)。

## 🚀 核心亮点 (Key Features)

### 1. 🛡️ 企业级流量治理 (Sentinel)
* 集成 **Alibaba Sentinel**，在网关层实现 **QPS 限流**。
* 配置了 `Threshold: 5` 的流控规则，精准拦截突发流量，防止后端服务雪崩。
* 自定义中间件处理逻辑，实现了优雅的 `429 Too Many Requests` 降级返回。

### 2. ⚡ 极致性能与原子性 (Redis + Lua)
* 抛弃传统的数据库锁机制，采用 **Redis 预热 + Lua 脚本** 扣减库存。
* **原子性保障**: 确保在高并发下库存扣减操作的原子性，彻底解决 **"超卖"** 问题。
* **智能反馈**: 优化 Lua 脚本逻辑，精准区分 **"库存不足"** 与 **"商品不存在"** 两种状态。

### 3. 🌊 削峰填谷与可靠性 (RabbitMQ + DLQ)
* **异步下单**: 将耗时的数据库写入操作剥离，通过 RabbitMQ 异步解耦，实现毫秒级响应。
* **死信队列 (DLQ)**: 针对消费者处理失败（如数据库宕机）的场景，配置了 `x-dead-letter-exchange`，确保故障消息自动进入死信队列，**数据零丢失**。

### 4. 🔄 分布式事务最终一致性 (Compensation)
* 实现了基于 **MQ 确认机制** 的柔性事务。
* **自动补偿**: 当订单服务发送 MQ 失败（如网络抖动）时，自动触发 **"库存回滚"** 策略，调用商品服务将 Redis 库存恢复，消除 **"少卖"** 隐患。
* 采用 `Context.Background()` 独立的上下文控制回滚超时，防止因主请求超时导致回滚失败。

## 🛠️ 技术栈

* **开发语言**: Go (Golang)
* **Web 框架**: Gin
* **RPC 框架**: gRPC + Protobuf
* **服务发现**: Etcd v3
* **数据库**: MySQL (GORM)
* **缓存**: Redis
* **消息队列**: RabbitMQ
* **链路追踪**: OpenTelemetry + Jaeger
* **限流熔断**: Sentinel-Golang

## 🏃‍♂️ 快速开始

### 1. 环境准备
确保本地已安装 Docker 和 Docker Compose。

### 2. 启动基础设施
```bash
docker-compose up -d
# 启动 MySQL, Redis, RabbitMQ, Etcd, Jaeger
```

### 3. 初始化数据
* 将 `sql/schema.sql` 导入 MySQL。
* 运行 `make init` (或手动预热 Redis 库存)。

### 4. 启动微服务
```bash
# 启动商品服务
go run product_service/main.go

# 启动订单服务
go run order_service/main.go

# 启动网关
go run api_gateway/main.go
```

### 5. 压力测试
使用 `stress_test/main.go` 脚本模拟 20+ 并发请求，观察 Sentinel 限流与 MQ 削峰效果。

```bash
go run stress_test/main.go
```

---
*Created by Li