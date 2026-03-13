package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"seckill-system/common/config"
)

const (
	MQ_URL = "amqp://guest:guest@localhost:5672/"

	OrderQueue = "seckill_order_queue"

	DeadExchange   = "dlx_exchange" // 死信交换机
	DeadQueue      = "dead_queue"   // 死信队列
	DeadRoutingKey = "dead_key"     // 死信路由键
)

// 初始化队列系统
func setupQueue(ch *amqp.Channel) (amqp.Queue, error) {
	//声明死信交换机
	err := ch.ExchangeDeclare(DeadExchange, "direct", true, false, false, false, nil)
	if err != nil {
		return amqp.Queue{}, fmt.Errorf("无法声明死信交换机: %w", err)
	}

	//声明死信队列
	_, err = ch.QueueDeclare(DeadQueue, true, false, false, false, nil)
	if err != nil {
		return amqp.Queue{}, fmt.Errorf("无法声明死信队列: %w", err)
	}

	//绑定：死信交换机 -> 死信队列
	err = ch.QueueBind(DeadQueue, DeadRoutingKey, DeadExchange, false, nil)
	if err != nil {
		return amqp.Queue{}, fmt.Errorf("无法绑定死信队列: %w", err)
	}

	//声明主队列（业务队列），并配置它“连接”到死信交换机
	args := amqp.Table{
		"x-dead-letter-exchange":    DeadExchange,   // 报错后发给谁？
		"x-dead-letter-routing-key": DeadRoutingKey, // 带什么暗号发？
	}

	q, err := ch.QueueDeclare(
		OrderQueue,
		true,
		false,
		false,
		false,
		args, //把死信参数传进去
	)
	if err != nil {
		return amqp.Queue{}, fmt.Errorf("无法声明主队列(可能参数冲突): %w", err)
	}

	log.Printf("✅ RabbitMQ 队列结构初始化完成：主队列[%s] -> 死信[%s]", OrderQueue, DeadQueue)
	return q, nil
}

// 对应数据库结构
type Order struct {
	// 对应数据库 id, bigint(20) unsigned, auto_increment
	ID uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	// 对应数据库 order_id, varchar(64)
	OrderID string `gorm:"column:order_id;uniqueIndex;not null"`
	// 其他字段
	UserID    int64     `gorm:"column:user_id;not null"`
	ProductID int64     `gorm:"column:product_id;not null"`
	Amount    float32   `gorm:"column:amount;not null"`
	Status    int       `gorm:"column:status;default:0"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (Order) TableName() string { return "orders" }

// MQ 消息结构
type OrderMessage struct {
	OrderID   string  `json:"order_id"`
	UserID    int64   `json:"user_id"`
	ProductID int64   `json:"product_id"`
	Amount    float32 `json:"amount"`
}

var db *gorm.DB

var (
	metricReceived  uint64
	metricSuccess   uint64
	metricDuplicate uint64
	metricNack      uint64
	metricInvalid   uint64
)

func main() {
	config.InitConfig("mq")
	initDB()
	startMetricsReporter()
	runConsumerLoop()
}

func runConsumerLoop() {
	retryDelay := 1 * time.Second
	maxRetryDelay := 15 * time.Second

	for {
		err := consumeOnce()
		if err == nil {
			retryDelay = 1 * time.Second
			continue
		}

		log.Printf("⚠️ 消费循环异常，%v；%s 后重连...", err, retryDelay)
		time.Sleep(retryDelay)

		retryDelay *= 2
		if retryDelay > maxRetryDelay {
			retryDelay = maxRetryDelay
		}
	}
}

func consumeOnce() error {
	conn, err := amqp.Dial(MQ_URL)
	if err != nil {
		return fmt.Errorf("连接RabbitMQ失败: %w", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("创建MQ通道失败: %w", err)
	}
	defer ch.Close()

	if err := ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("设置Qos失败: %w", err)
	}

	q, err := setupQueue(ch)
	if err != nil {
		return err
	}

	msgs, err := ch.Consume(
		q.Name,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("注册消费者失败: %w", err)
	}

	log.Println("📧 消费者服务已启动 (守护重连版)，等待订单中...")

	closeErrCh := make(chan *amqp.Error, 1)
	conn.NotifyClose(closeErrCh)

	for {
		select {
		case d, ok := <-msgs:
			if !ok {
				return errors.New("消息通道关闭")
			}
			handleDelivery(d)
		case closeErr := <-closeErrCh:
			if closeErr == nil {
				return errors.New("RabbitMQ连接关闭")
			}
			return fmt.Errorf("RabbitMQ连接关闭: %v", closeErr)
		}
	}
}

func handleDelivery(d amqp.Delivery) {
	atomic.AddUint64(&metricReceived, 1)

	var msg OrderMessage
	if err := json.Unmarshal(d.Body, &msg); err != nil {
		log.Printf("❌ 消息格式错误，直接丢弃: %v", err)
		atomic.AddUint64(&metricInvalid, 1)
		atomic.AddUint64(&metricNack, 1)
		_ = d.Nack(false, false)
		return
	}

	fmt.Printf("📦 接收订单: %s | 金额：%.2f | 处理中...", msg.OrderID, msg.Amount)

	order := Order{
		OrderID:   msg.OrderID,
		UserID:    msg.UserID,
		ProductID: msg.ProductID,
		Amount:    msg.Amount,
		Status:    1,
	}

	time.Sleep(50 * time.Millisecond)

	err := db.Create(&order).Error
	if err != nil {
		if strings.Contains(err.Error(), "Duplicate entry") {
			fmt.Printf(" -> ⚠️ 订单已存在，确认消息\n")
			atomic.AddUint64(&metricDuplicate, 1)
			_ = d.Ack(false)
			return
		}

		log.Printf(" -> ❌ 落库失败: %v，发送 Nack(不重回队列)->进入死信", err)
		atomic.AddUint64(&metricNack, 1)
		_ = d.Nack(false, false)
		return
	}

	fmt.Printf(" -> ✅ 落库成功\n")
	atomic.AddUint64(&metricSuccess, 1)
	_ = d.Ack(false)
}

func startMetricsReporter() {
	go func() {
		interval := 60 * time.Second
		if strings.EqualFold(config.Conf.Server.Mode, "debug") {
			interval = 10 * time.Second
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		var lastReceived uint64
		var lastSuccess uint64
		var lastDuplicate uint64
		var lastNack uint64
		var lastInvalid uint64

		log.Printf("📊 ConsumerStats 已启用，interval=%s（仅计数变化时输出）", interval)

		for range ticker.C {
			received := atomic.LoadUint64(&metricReceived)
			success := atomic.LoadUint64(&metricSuccess)
			duplicate := atomic.LoadUint64(&metricDuplicate)
			nack := atomic.LoadUint64(&metricNack)
			invalid := atomic.LoadUint64(&metricInvalid)

			if received == lastReceived &&
				success == lastSuccess &&
				duplicate == lastDuplicate &&
				nack == lastNack &&
				invalid == lastInvalid {
				continue
			}

			lastReceived = received
			lastSuccess = success
			lastDuplicate = duplicate
			lastNack = nack
			lastInvalid = invalid

			log.Printf("📊 ConsumerStats received=%d success=%d duplicate=%d nack=%d invalid=%d", received, success, duplicate, nack, invalid)
		}
	}()
}

func initDB() {
	dsn := config.Conf.MySQL.DSN
	var err error
	db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("连接MySQL失败: %v", err)
	}
	// 表结构已固定，注释掉 AutoMigrate 防止改动
	// db.AutoMigrate(&Order{})
	fmt.Println("✅ MySQL 连接成功")
}
