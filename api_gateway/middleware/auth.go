package middleware

import (
	"net/http"
	"seckill-mall/common/utils"
	"strings"

	"github.com/gin-gonic/gin"
)

// JWTAuth鉴权中间件
func JWTAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		//获取Authorization头部信息
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "请求未携带token，请先登录",
			})
			return
		}

		//解析Bearer
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "Token格式有误",
			})
			return
		}

		//验证Token
		claims, err := utils.ParseToken(parts[1])
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "Token无效或已过期",
			})
			return
		}

		//将解析出来的UserID存入Context
		c.Set("userID", claims.UserID)

		c.Next()
	}
}
