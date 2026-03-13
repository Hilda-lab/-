package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

const (
	BaseURL       = "http://127.0.0.1:18080"
	TotalRequests = 200
	Concurrency   = 50
	ProductID     = 1
)

type LoginResponse struct {
	Code  int    `json:"code"`
	Token string `json:"token"`
	Msg   string `json:"message"`
}

type OrderResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		OrderID string `json:"order_id"`
		Success bool   `json:"success"`
		Message string `json:"message"`
	} `json:"data"`
}

var (
	successCount     int
	failCount        int
	loginFailCount   int
	networkFailCount int
	parseFailCount   int
	bizFailCount     int
	latencyMs        []float64
	countMutex       sync.Mutex
)

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	idx := p * float64(len(values)-1)
	low := int(idx)
	high := low + 1
	if high >= len(values) {
		return values[low]
	}
	frac := idx - float64(low)
	return values[low] + (values[high]-values[low])*frac
}

func main() {
	fmt.Printf("开始模拟压测\n")
	fmt.Printf("总人数: %d, 并发控制: %d, 商品ID: %d\n", TotalRequests, Concurrency, ProductID)

	var wg sync.WaitGroup
	limitChan := make(chan struct{}, Concurrency)
	startTime := time.Now()

	for i := 0; i < TotalRequests; i++ {
		wg.Add(1)
		limitChan <- struct{}{}

		go func(idx int) {
			defer wg.Done()
			defer func() { <-limitChan }()

			currentUID := 20000 + idx
			token, err := login(currentUID)
			if err != nil {
				fmt.Printf("[用户 %d] 登录失败: %v\n", currentUID, err)
				countMutex.Lock()
				failCount++
				loginFailCount++
				countMutex.Unlock()
				return
			}

			createOrder(currentUID, token)
		}(i)
	}

	wg.Wait()

	elapsed := time.Since(startTime)
	requestTPS := float64(TotalRequests) / elapsed.Seconds()
	successTPS := float64(successCount) / elapsed.Seconds()

	countMutex.Lock()
	latencies := append([]float64(nil), latencyMs...)
	countMutex.Unlock()

	sort.Float64s(latencies)
	avg := 0.0
	for _, v := range latencies {
		avg += v
	}
	if len(latencies) > 0 {
		avg /= float64(len(latencies))
	}

	fmt.Printf("\n========== 压测结果 ==========\n")
	fmt.Printf("总请求数: %d\n", TotalRequests)
	fmt.Printf("成功: %d\n", successCount)
	fmt.Printf("失败: %d\n", failCount)
	fmt.Printf("总耗时: %v\n", elapsed)
	fmt.Printf("请求TPS: %.2f\n", requestTPS)
	fmt.Printf("成功TPS: %.2f\n", successTPS)
	fmt.Printf("平均延迟(ms): %.2f\n", avg)
	fmt.Printf("P50延迟(ms): %.2f\n", percentile(latencies, 0.50))
	fmt.Printf("P95延迟(ms): %.2f\n", percentile(latencies, 0.95))
	fmt.Printf("P99延迟(ms): %.2f\n", percentile(latencies, 0.99))
	fmt.Printf("失败分类: login=%d network=%d parse=%d biz=%d\n", loginFailCount, networkFailCount, parseFailCount, bizFailCount)
	fmt.Printf("==============================\n")
}

func login(uid int) (string, error) {
	reqBody := map[string]interface{}{"user_id": uid}
	jsonData, _ := json.Marshal(reqBody)

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
	requestStart := time.Now()
	resp, err := client.Do(req)

	if err != nil {
		fmt.Printf("[用户 %d] 请求超时/错误: %v\n", uid, err)
		countMutex.Lock()
		failCount++
		networkFailCount++
		latencyMs = append(latencyMs, float64(time.Since(requestStart).Milliseconds()))
		countMutex.Unlock()
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var res OrderResponse
	if err := json.Unmarshal(body, &res); err != nil {
		fmt.Printf("[用户 %d] 解析响应失败: %v\n", uid, err)
		countMutex.Lock()
		failCount++
		parseFailCount++
		latencyMs = append(latencyMs, float64(time.Since(requestStart).Milliseconds()))
		countMutex.Unlock()
		return
	}

	if res.Code == 200 && res.Data.Success {
		fmt.Printf("[用户 %d] 抢购成功 ✓ (订单号: %s)\n", uid, res.Data.OrderID)
		countMutex.Lock()
		successCount++
		latencyMs = append(latencyMs, float64(time.Since(requestStart).Milliseconds()))
		countMutex.Unlock()
	} else {
		fmt.Printf("[用户 %d] 抢购失败 ✗ (%s)\n", uid, res.Data.Message)
		countMutex.Lock()
		failCount++
		bizFailCount++
		latencyMs = append(latencyMs, float64(time.Since(requestStart).Milliseconds()))
		countMutex.Unlock()
	}
}
