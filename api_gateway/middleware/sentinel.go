package middleware

import (
	"net/http"

	sentinel "github.com/alibaba/sentinel-golang/api"
	"github.com/alibaba/sentinel-golang/core/base"
	"github.com/gin-gonic/gin"
)

func SentinelLimit(resourceName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		//进入Sentinel的Entry
		//TrafficType表示入口流量
		e, b := sentinel.Entry(resourceName, sentinel.WithTrafficType(base.Inbound))
		if b != nil {
			//被限流或降级处理
			//直接拦截
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "活动太火爆了，请稍后再试~",
			})
			return
		}

		defer e.Exit()

		c.Next()
	}
}
