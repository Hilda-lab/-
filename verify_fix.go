package main

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

/**
 * 库存一致性修复验证脚本
 * 运行前确保Redis已启动: docker-compose up -d redis
 */

func main() {
	fmt.Println("========== 库存一致性修复验证 ==========\n")

	// 连接Redis
	rdb := redis.NewClient(&redis.Options{
		Addr:     "127.0.0.1:6379",
		Password: "123456",
		DB:       0,
	})
	defer rdb.Close()

	ctx := context.Background()

	// 测试连接
	if err := rdb.Ping(ctx).Err(); err != nil {
		fmt.Printf("❌ Redis连接失败: %v\n", err)
		fmt.Println("请确保Redis已启动: docker-compose up -d redis")
		return
	}
	fmt.Println("✅ Redis连接成功\n")

	// 测试1: 验证回滚Lua脚本
	testRollbackScript(rdb, ctx)

	// 测试2: 验证扣减Lua脚本
	testDeductScript(rdb, ctx)

	// 测试3: 验证并发一致性
	testConcurrentConsistency(rdb, ctx)

	fmt.Println("\n========== 所有测试完成 ==========")
}

// 测试回滚脚本
func testRollbackScript(rdb *redis.Client, ctx context.Context) {
	fmt.Println("【测试1】回滚Lua脚本原子性")

	productID := int64(9999)
	userID := int64(10001)
	stockKey := fmt.Sprintf("product:stock:%d", productID)
	userSetKey := fmt.Sprintf("product:users:%d", productID)

	// 清理测试数据
	rdb.Del(ctx, stockKey, userSetKey)

	// 初始化：库存100，用户已购买5件
	rdb.Set(ctx, stockKey, 100, 0)
	rdb.HSet(ctx, userSetKey, fmt.Sprintf("%d", userID), 5)

	fmt.Printf("  初始状态 - 库存: 100, 用户%d已购买: 5\n", userID)

	// 回滚Lua脚本
	const ROLLBACK_SCRIPT = `
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
	`

	// 回滚5件
	stock, err := rdb.Eval(ctx, ROLLBACK_SCRIPT, []string{stockKey, userSetKey}, 5, userID).Int64()
	if err != nil {
		fmt.Printf("  ❌ 回滚失败: %v\n\n", err)
		return
	}

	// 验证结果
	userBuy, _ := rdb.HGet(ctx, userSetKey, fmt.Sprintf("%d", userID)).Result()

	fmt.Printf("  回滚后 - 库存: %d (预期105)\n", stock)
	fmt.Printf("  回滚后 - 用户%d购买记录: %s (预期已删除)\n", userID, userBuy)

	if stock == 105 && userBuy == "" {
		fmt.Println("  ✅ 测试通过：回滚正确，购买记录已清除\n")
	} else {
		fmt.Println("  ❌ 测试失败：回滚结果不符合预期\n")
	}
}

// 测试扣减脚本
func testDeductScript(rdb *redis.Client, ctx context.Context) {
	fmt.Println("【测试2】扣减Lua脚本（限购检查）")

	productID := int64(9998)
	userID := int64(10002)
	stockKey := fmt.Sprintf("product:stock:%d", productID)
	userSetKey := fmt.Sprintf("product:users:%d", productID)

	// 清理并初始化
	rdb.Del(ctx, stockKey, userSetKey)
	rdb.Set(ctx, stockKey, 50, 0)

	const DEDUCT_SCRIPT = `
		if redis.call("EXISTS", KEYS[1]) == 0 then return 0 end
		local current_buy = tonumber(redis.call('hget', KEYS[2], ARGV[2])) or 0
		local want_buy = tonumber(ARGV[1])
		local limit = tonumber(ARGV[3])
		if current_buy + want_buy > limit then return 3 end
		local stock = tonumber(redis.call("GET", KEYS[1]))
		if stock < want_buy then return 2 end
		redis.call("decrby", KEYS[1], want_buy)
		redis.call("hincrby", KEYS[2], ARGV[2], want_buy)
		return 1
	`

	purchaseLimit := 5

	// 第一次购买5件（达到上限）
	val1, _ := rdb.Eval(ctx, DEDUCT_SCRIPT, []string{stockKey, userSetKey}, 5, userID, purchaseLimit).Int()
	fmt.Printf("  第1次购买5件 - 结果: %d (1=成功)\n", val1)

	// 第二次购买1件（应该超限）
	val2, _ := rdb.Eval(ctx, DEDUCT_SCRIPT, []string{stockKey, userSetKey}, 1, userID, purchaseLimit).Int()
	fmt.Printf("  第2次购买1件 - 结果: %d (3=超限)\n", val2)

	userBuy, _ := rdb.HGet(ctx, userSetKey, fmt.Sprintf("%d", userID)).Int64()
	stock, _ := rdb.Get(ctx, stockKey).Int64()

	fmt.Printf("  最终状态 - 库存: %d, 用户已购买: %d\n", stock, userBuy)

	if val1 == 1 && val2 == 3 && userBuy == 5 && stock == 45 {
		fmt.Println("  ✅ 测试通过：限购逻辑正确\n")
	} else {
		fmt.Println("  ❌ 测试失败：限购逻辑异常\n")
	}
}

// 测试并发一致性
func testConcurrentConsistency(rdb *redis.Client, ctx context.Context) {
	fmt.Println("【测试3】并发场景一致性验证")

	productID := int64(9997)
	stockKey := fmt.Sprintf("product:stock:%d", productID)
	userSetKey := fmt.Sprintf("product:users:%d", productID)

	// 初始化库存30件
	initialStock := int64(30)
	rdb.Del(ctx, stockKey, userSetKey)
	rdb.Set(ctx, stockKey, initialStock, 0)

	const DEDUCT_SCRIPT = `
		if redis.call("EXISTS", KEYS[1]) == 0 then return 0 end
		local current_buy = tonumber(redis.call('hget', KEYS[2], ARGV[2])) or 0
		local want_buy = tonumber(ARGV[1])
		local limit = tonumber(ARGV[3])
		if current_buy + want_buy > limit then return 3 end
		local stock = tonumber(redis.call("GET", KEYS[1]))
		if stock < want_buy then return 2 end
		redis.call("decrby", KEYS[1], want_buy)
		redis.call("hincrby", KEYS[2], ARGV[2], want_buy)
		return 1
	`

	const ROLLBACK_SCRIPT = `
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
	`

	// 模拟50个并发请求
	totalRequests := 50
	successCount := 0
	rollbackCount := 0
	failCount := 0

	done := make(chan int, totalRequests)

	for i := 0; i < totalRequests; i++ {
		go func(idx int) {
			userID := int64(20000 + idx)
			
			// 尝试扣减
			val, _ := rdb.Eval(ctx, DEDUCT_SCRIPT, []string{stockKey, userSetKey}, 1, userID, 5).Int()
			
			if val == 1 {
				// 模拟30%的MQ失败需要回滚
				if idx%3 == 0 {
					time.Sleep(10 * time.Millisecond)
					rdb.Eval(ctx, ROLLBACK_SCRIPT, []string{stockKey, userSetKey}, 1, userID)
					done <- 2 // 回滚
					return
				}
				done <- 1 // 成功
			} else {
				done <- 0 // 失败
			}
		}(i)
	}

	// 收集结果
	for i := 0; i < totalRequests; i++ {
		result := <-done
		switch result {
		case 1:
			successCount++
		case 2:
			rollbackCount++
		default:
			failCount++
		}
	}

	// 验证最终一致性
	finalStock, _ := rdb.Get(ctx, stockKey).Int64()
	expectedStock := initialStock - int64(successCount-rollbackCount)

	fmt.Printf("  并发请求: %d\n", totalRequests)
	fmt.Printf("  成功扣减: %d\n", successCount)
	fmt.Printf("  已回滚: %d\n", rollbackCount)
	fmt.Printf("  失败(库存不足): %d\n", failCount)
	fmt.Printf("  最终库存: %d\n", finalStock)
	fmt.Printf("  预期库存: %d (初始%d - 成功%d + 回滚%d)\n", 
		expectedStock, initialStock, successCount, rollbackCount)

	if finalStock == expectedStock {
		fmt.Println("  ✅ 测试通过：库存一致性正确\n")
	} else {
		fmt.Printf("  ❌ 测试失败：库存不一致 (差值: %d)\n\n", finalStock-expectedStock)
	}
}
