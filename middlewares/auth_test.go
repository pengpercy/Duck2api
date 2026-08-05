package middlewares

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAuthorizationAcceptsAnthropicAPIKey(t *testing.T) {
	t.Setenv("Authorization", "secret")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Authorization)
	router.POST("/v1/messages", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("x-api-key", "secret")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAuthorizationRejectsWrongAnthropicAPIKey(t *testing.T) {
	t.Setenv("Authorization", "secret")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Authorization)
	router.POST("/v1/messages", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("x-api-key", "wrong")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", recorder.Code)
	}
}
