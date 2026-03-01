package utils

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// 定义加密密钥，后期配置在config.yaml中
var jwtSecret = []byte("seckill-secert-key-2025")

// 自定义Claims结构体
type UserClaims struct {
	UserID int64 `json:"user_id"`
	jwt.RegisteredClaims
}

// 生成Token
func GenerateToken(userID int64, expireDuration time.Duration) (string, error) {
	now := time.Now()
	expireTime := now.Add(expireDuration)

	claims := UserClaims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			//NotBefore: jwt.NewNumericDate(now), // 可选：生效时间
			//IssuedAt:  jwt.NewNumericDate(now), // 可选：签发时间
			ExpiresAt: jwt.NewNumericDate(expireTime),
			Issuer:    "seckill-app",
		},
	}

	tokenClaims := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tokenClaims.SignedString(jwtSecret)
}

// 解析Token
func ParseToken(token string) (*UserClaims, error) {
	//解析并验证签名
	tokenClaims, err := jwt.ParseWithClaims(token, &UserClaims{}, func(token *jwt.Token) (interface{}, error) {
		return jwtSecret, nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := tokenClaims.Claims.(*UserClaims); ok && tokenClaims.Valid {
		return claims, nil
	}

	return nil, errors.New("invalid token")
}
