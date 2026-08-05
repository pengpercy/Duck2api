package initialize

import (
	"aurora/logger"
	"aurora/middlewares"
	"log"
	"os"

	"github.com/gin-gonic/gin"
)

// RegisterRouter 负责初始化和注册所有路由。
// 这是应用程序的启动入口。
func RegisterRouter() *gin.Engine {
	logger.Init()
	// NewHandler 可能会因为 Provider 初始化失败而返回错误。
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
			"message": "Hello World!",
		})
	})

	router.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
		})
	})

	// registerV1ApiRoutes 封装了所有 /v1 相关的路由注册逻辑。
	registerV1ApiRoutes := func(rg *gin.RouterGroup) {
		for _, path := range []string{"/chat/completions", "/messages", "/responses", "/images/generations", "/images/edits", "/files", "/audio/transcriptions", "/audio/speech", "/models"} {
			rg.OPTIONS(path, optionsHandler)
		}

		// 使用中间件保护需要授权的路由
		authGroup := rg.Group("").Use(middlewares.Authorization)
		{
			authGroup.POST("/chat/completions", handler.duckduckgo)
			authGroup.POST("/messages", handler.messagesHandler)
			authGroup.POST("/responses", handler.responses)
			authGroup.POST("/images/generations", handler.imageGenerations)
			authGroup.POST("/images/edits", handler.imageEdits)
			authGroup.POST("/files", handler.filesUpload)
			authGroup.GET("/files", handler.filesList)
			authGroup.GET("/files/:file_id", handler.filesGet)
			authGroup.DELETE("/files/:file_id", handler.filesDelete)
			authGroup.GET("/files/:file_id/content", handler.filesContent)
			authGroup.POST("/audio/transcriptions", handler.audioTranscriptions)
			authGroup.POST("/audio/speech", handler.audioSpeech)
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
