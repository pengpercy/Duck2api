package initialize

import (
	"aurora/middlewares"
	"log"
	"os"

	"github.com/gin-gonic/gin"
)

// RegisterRouter 负责初始化和注册所有路由。
// 这是应用程序的启动入口。
func RegisterRouter() *gin.Engine {
	// 关键改动 1: 正确处理 NewHandler 的初始化错误
	// NewHandler 现在可能会因为 ChromeDP 连接失败等原因返回错误。
	// 如果初始化失败，程序必须终止，否则后续请求会因 handler 为 nil 而 panic。
	handler, err := NewHandler(checkProxy())
	if err != nil {
		// 使用 log.Fatalf 会打印错误并立即退出程序，这是处理启动时致命错误的正确方式。
		log.Fatalf("Failed to initialize application handler: %v", err)
	}

	router := gin.Default()
	router.Use(middlewares.Cors)

	// --- 健康检查和基本路由 ---
	router.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "Hello, Aurora!",
		})
	})

	router.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
		})
	})

	// 关键改动 2: 使用辅助函数注册 API 路由，避免代码重复
	// registerV1ApiRoutes 封装了所有 /v1 相关的路由注册逻辑。
	registerV1ApiRoutes := func(rg *gin.RouterGroup) {
		rg.OPTIONS("/chat/completions", optionsHandler)
		rg.OPTIONS("/models", optionsHandler) // 修正：与 GET /v1/models 路径保持一致

		// 使用中间件保护需要授权的路由
		authGroup := rg.Group("").Use(middlewares.Authorization)
		{
			authGroup.POST("/chat/completions", handler.duckduckgo)
			authGroup.GET("/models", handler.engines)
		}
	}

	// 注册核心的 /v1 路由
	v1Group := router.Group("/v1")
	registerV1ApiRoutes(v1Group)

	// 如果配置了 PREFIX 环境变量，则在指定前缀下再次注册所有 /v1 路由
	// 这使得 API 可以通过例如 /api/v1/... 和 /v1/... 两种方式访问
	if prefix := os.Getenv("PREFIX"); prefix != "" {
		prefixV1Group := router.Group(prefix + "/v1")
		registerV1ApiRoutes(prefixV1Group)
		log.Printf("API routes also registered under prefix: %s", prefix)
	}

	return router
}
