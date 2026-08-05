package middlewares

import (
	"aurora/typings/anthropic"
	"aurora/util"
	"github.com/gin-gonic/gin"
	"os"
	"strings"
)

func unauthorized(c *gin.Context) {
	if strings.HasSuffix(c.Request.URL.Path, "/v1/messages") {
		requestID := "req_" + util.RandomHexadecimalString()
		c.Header("request-id", requestID)
		c.JSON(401, anthropic.ErrorResponse{
			Type:      "error",
			Error:     anthropic.AnthropicError{Type: "authentication_error", Message: "Invalid API key"},
			RequestID: requestID,
		})
		c.Abort()
		return
	}
	c.JSON(401, gin.H{"error": "Unauthorized"})
	c.Abort()
}

func Authorization(c *gin.Context) {
	customer_key := os.Getenv("Authorization")
	if customer_key != "" {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			// Anthropic SDKs use x-api-key instead of Bearer authentication.
			if apiKey := c.GetHeader("x-api-key"); apiKey == customer_key {
				c.Next()
				return
			}
			unauthorized(c)
			return
		}
		tokenParts := strings.Split(strings.Replace(authHeader, "Bearer ", "", 1), " ")
		customAccessToken := tokenParts[0]
		if customer_key != customAccessToken {
			unauthorized(c)
			return
		}
		if len(tokenParts) > 1 {
			openaiAccessToken := tokenParts[1]
			c.Request.Header.Set("Authorization", "Bearer "+openaiAccessToken)
		}
	}
	c.Next()
}
