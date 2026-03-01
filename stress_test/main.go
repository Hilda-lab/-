package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// 配置
const (
	BaseURL       = "http://127.0.0.1:8080"
	TotalRequests = 200 // 总共模拟多少人抢购 (想抢光100件，建议设为200或更多)
	Concurrency   = 50  // 限制同时有多少个请求在跑(控制并发度，防止本机端口耗尽)
	ProductID     = 1
)

type LoginResponse struct {
	Code  int    `json:"code"`
	Token string `json:"token"`
	Msg   string `json:"message"`
}

func main() {
	fmt.Printf("开始模拟压测\n")
	fmt.Printf("总人数: %d, 并发控制: %d, 商品ID: %d\n", TotalRequests, Concurrency, ProductID)

	var wg sync.WaitGroup

	// 创建一个通道来控制并发数 (Semaphore模式)
	// 类似于环形路口，只有拿到令牌的才能进
	limitChan := make(chan struct{}, Concurrency)

	startTime := time.Now()

	// 循环 TotalRequests 次，模拟不同的人
	for i := 0; i < TotalRequests; i++ {
		wg.Add(1)

		// 占用一个并发名额
		limitChan <- struct{}{}

		go func(idx int) {
			defer wg.Done()
			defer func() { <-limitChan }() // 任务做完，释放名额

			// 生成不同的 UserID (从 20000 开始，避免和之前的冲突)
			currentUID := 20000 + idx

			// 每个人都要单独登录，拿自己的 Token
			token, err := login(currentUID)
			if err != nil {
				fmt.Printf("[用户 %d] 登录失败: %v\n", currentUID, err)
				return
			}

			// 带着自己的 Token 去抢购
			createOrder(currentUID, token)
		}(i)
	}

	wg.Wait()
	fmt.Printf("\n压测完成，总耗时: %v\n", time.Since(startTime))
}

// 登录动作
func login(uid int) (string, error) {
	reqBody := map[string]interface{}{"user_id": uid}
	jsonData, _ := json.Marshal(reqBody)

	// 注意：如果你的电脑跑不动太快的登录，这里可能会报错，那是正常的
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(BaseURL+"/login", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var res LoginResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("解析响应失败")
	}

	if res.Code != 200 {
		return "", fmt.Errorf("服务端错误: %s", string(body))
	}
	return res.Token, nil
}

// 下单动作
func createOrder(uid int, token string) {
	reqBody := map[string]interface{}{
		"product_id": ProductID,
		"count":      1,
	}
	jsonData, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", BaseURL+"/order", bytes.NewBuffer(jsonData))

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)

	if err != nil {
		fmt.Printf("[用户 %d] 请求超时/错误\n", uid)
		return
	}
	defer resp.Body.Close()

	// 简单打印结果
	if resp.StatusCode == 200 {
		// 为了控制台干净点，只打印成功的
		fmt.Printf("[用户 %d] 抢购成功\n", uid)
	} else {
		fmt.Printf("[用户 %d] 失败: %d\n", uid, resp.StatusCode)
	}
}
