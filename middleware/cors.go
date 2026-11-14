package middleware

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func CORS() gin.HandlerFunc {
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowCredentials = true
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	config.AllowHeaders = []string{"*"}
	// 让浏览器端的客户端能读到 new-api 的 request id，用于反馈/日志关联。
	config.ExposeHeaders = []string{common.RequestIdKey}
	return cors.New(config)
}

// CORSForTokenAPI 用于只接受 Authorization 令牌的公开接口。
// 与 CORS() 的差别：不允许携带浏览器凭据（Cookie），避免跨站请求借用登录态；
// 允许的方法与请求头也收窄到该接口真正需要的范围。
func CORSForTokenAPI() gin.HandlerFunc {
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowCredentials = false
	config.AllowMethods = []string{"POST", "OPTIONS"}
	config.AllowHeaders = []string{"Authorization", "Content-Type"}
	config.ExposeHeaders = []string{common.RequestIdKey}
	return cors.New(config)
}

func PoweredBy() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-New-Api-Version", common.Version)
		c.Next()
	}
}
