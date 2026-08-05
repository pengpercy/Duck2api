package initialize

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	officialtypes "aurora/typings/official"
	"github.com/gin-gonic/gin"
)

func TestFileLifecycleAndAttachment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fileStorage.Lock()
	fileStorage.data = make(map[string]*StoredFile)
	fileStorage.Unlock()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("important context"))
	writer.Close()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/files", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	(&Handler{}).filesUpload(c)
	if rec.Code != 200 {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	fileStorage.RLock()
	var id string
	for key := range fileStorage.data {
		id = key
	}
	fileStorage.RUnlock()
	if id == "" {
		t.Fatal("missing stored file")
	}
	req := officialtypes.APIRequest{Messages: []officialtypes.ApiMessage{{Role: "user", Content: "summarize"}}, FileIDs: []string{id}}
	if err := attachFiles(&req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.Messages[0].Content.(string), "important context") {
		t.Fatalf("attachment not injected: %#v", req.Messages[0].Content)
	}
}
