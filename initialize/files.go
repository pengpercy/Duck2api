package initialize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type FileObject struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Bytes     int    `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
	Filename  string `json:"filename"`
	Purpose   string `json:"purpose"`
}
type StoredFile struct {
	ID, Filename, MimeType, Purpose string
	Bytes                           []byte
	Created                         int64
}
type TranscriptionResponse struct {
	Text string `json:"text"`
}

var fileStorage = struct {
	sync.RWMutex
	data map[string]*StoredFile
}{data: make(map[string]*StoredFile)}

func fileObject(f *StoredFile) FileObject {
	return FileObject{f.ID, "file", len(f.Bytes), f.Created, f.Filename, f.Purpose}
}

func (h *Handler) filesUpload(c *gin.Context) {
	f, hdr, err := c.Request.FormFile("file")
	if err != nil {
		apiError(c, 400, "file is required", "missing_file")
		return
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 32<<20+1))
	if err != nil || len(b) > 32<<20 {
		apiError(c, 400, "file exceeds 32 MiB limit", "file_too_large")
		return
	}
	purpose := c.PostForm("purpose")
	if purpose == "" {
		purpose = "assistants"
	}
	mime := hdr.Header.Get("Content-Type")
	if mime == "" || mime == "application/octet-stream" {
		mime = detectMimeType(hdr.Filename)
	}
	item := &StoredFile{ID: fmt.Sprintf("file-%d", time.Now().UnixNano()), Filename: hdr.Filename, Bytes: b, MimeType: mime, Purpose: purpose, Created: time.Now().Unix()}
	fileStorage.Lock()
	fileStorage.data[item.ID] = item
	fileStorage.Unlock()
	c.JSON(http.StatusOK, fileObject(item))
}
func (h *Handler) filesList(c *gin.Context) {
	fileStorage.RLock()
	out := make([]FileObject, 0, len(fileStorage.data))
	for _, f := range fileStorage.data {
		out = append(out, fileObject(f))
	}
	fileStorage.RUnlock()
	c.JSON(200, gin.H{"object": "list", "data": out})
}
func (h *Handler) filesGet(c *gin.Context) {
	f, ok := getFile(c.Param("file_id"))
	if !ok {
		apiError(c, 404, "File not found", "file_not_found")
		return
	}
	c.JSON(200, fileObject(f))
}
func (h *Handler) filesDelete(c *gin.Context) {
	id := c.Param("file_id")
	fileStorage.Lock()
	_, ok := fileStorage.data[id]
	if ok {
		delete(fileStorage.data, id)
	}
	fileStorage.Unlock()
	if !ok {
		apiError(c, 404, "File not found", "file_not_found")
		return
	}
	c.JSON(200, gin.H{"id": id, "object": "file", "deleted": true})
}
func (h *Handler) filesContent(c *gin.Context) {
	f, ok := getFile(c.Param("file_id"))
	if !ok {
		apiError(c, 404, "File not found", "file_not_found")
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", f.Filename))
	c.Data(200, f.MimeType, f.Bytes)
}
func getFile(id string) (*StoredFile, bool) {
	fileStorage.RLock()
	defer fileStorage.RUnlock()
	f, ok := fileStorage.data[id]
	return f, ok
}

func (h *Handler) audioTranscriptions(c *gin.Context) {
	f, hdr, err := c.Request.FormFile("file")
	if err != nil {
		apiError(c, 400, "file is required", "missing_file")
		return
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 32<<20+1))
	if err != nil || len(b) > 32<<20 {
		apiError(c, 400, "audio exceeds 32 MiB limit", "file_too_large")
		return
	}
	mime := hdr.Header.Get("Content-Type")
	if mime == "" || mime == "application/octet-stream" {
		mime = detectAudioMimeType(hdr.Filename)
	}
	resp, err := h.duckgoProvider.AuthenticatedRequest("POST", "https://duck.ai/duckchat/v1/dictation", mime, "application/json", b)
	if err != nil {
		apiError(c, 502, "Transcription failed: "+err.Error(), "upstream_error")
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		apiError(c, resp.StatusCode, "Transcription failed: "+string(body), "upstream_error")
		return
	}
	var v map[string]any
	if json.Unmarshal(body, &v) == nil {
		for _, k := range []string{"text", "transcription", "result", "content"} {
			if s, ok := v[k].(string); ok {
				c.JSON(200, TranscriptionResponse{Text: s})
				return
			}
		}
	}
	c.JSON(200, TranscriptionResponse{Text: strings.TrimSpace(string(bytes.TrimSpace(body)))})
}

func detectMimeType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt":
		return "text/plain"
	case ".md":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".csv":
		return "text/csv"
	case ".pdf":
		return "application/pdf"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	}
	return "application/octet-stream"
}
func detectAudioMimeType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	case ".m4a":
		return "audio/mp4"
	case ".flac":
		return "audio/flac"
	case ".opus":
		return "audio/opus"
	case ".aac":
		return "audio/aac"
	}
	return "audio/webm"
}
