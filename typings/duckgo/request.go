package duckgo

import "encoding/json"

type DurableStream struct {
	MessageID      string    `json:"messageId"`
	ConversationID string    `json:"conversationId"`
	PublicKey      PublicKey `json:"publicKey"`
}

type Metadata struct {
	ToolChoice ToolChoice `json:"toolChoice"`
}

type PublicKey struct {
	Alg    string   `json:"alg"`
	E      string   `json:"e"`
	Ext    bool     `json:"ext"`
	KeyOps []string `json:"key_ops"`
	Kty    string   `json:"kty"`
	N      string   `json:"n"`
	Use    string   `json:"use"`
}

type ToolChoice struct {
	GenerateImage   bool `json:"GenerateImage,omitempty"`
	WebSearch       bool `json:"WebSearch,omitempty"`
	NewsSearch      bool `json:"NewsSearch"`
	VideosSearch    bool `json:"VideosSearch"`
	LocalSearch     bool `json:"LocalSearch"`
	WeatherForecast bool `json:"WeatherForecast"`
}

// ContentPart represents a single part in a multipart message
type ContentPart struct {
	Type     string `json:"type"`               // "text", "image", "file"
	Text     string `json:"text,omitempty"`     // for type=text
	Image    string `json:"image,omitempty"`    // for type=image (data URL)
	MimeType string `json:"mimeType,omitempty"` // for type=image or type=file
	Filename string `json:"filename,omitempty"` // for type=file
}

// MessageContent can be either a plain string or an array of ContentParts
type MessageContent struct {
	Parts []ContentPart
}

func (m MessageContent) MarshalJSON() ([]byte, error) {
	if len(m.Parts) == 1 && m.Parts[0].Type == "text" {
		return json.Marshal(m.Parts[0].Text)
	}
	return json.Marshal(m.Parts)
}

func (m *MessageContent) IsEmpty() bool {
	if m == nil || len(m.Parts) == 0 {
		return true
	}
	for _, p := range m.Parts {
		if p.Type == "text" && p.Text != "" {
			return false
		}
		if p.Type == "image" && p.Image != "" {
			return false
		}
		if p.Type == "file" {
			return false
		}
	}
	return true
}

func (m *MessageContent) TextContent() string {
	if m == nil {
		return ""
	}
	var text string
	for _, p := range m.Parts {
		if p.Type == "text" {
			text += p.Text
		}
	}
	return text
}

type ApiRequest struct {
	Model                      string        `json:"model"`
	Metadata                   Metadata      `json:"metadata"`
	Messages                   []any         `json:"messages"`
	CanUseTools                bool          `json:"canUseTools"`
	ReasoningEffort            string        `json:"reasoningEffort"`
	CanUseApproxLocation       *bool         `json:"canUseApproxLocation"`
	CanDelegateImageGeneration *bool         `json:"canDelegateImageGeneration"`
	DurableStream              DurableStream `json:"durableStream"`
}

type MessageUser struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}
type MessageAssistant struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func (a *ApiRequest) AddMessage(role string, content string) {
	a.AddMessageWithParts(role, []ContentPart{{Type: "text", Text: content}})
}

// AddMessageWithParts adds a message with complex content (text + images + files)
func (a *ApiRequest) AddMessageWithParts(role string, parts []ContentPart) {
	content := any(MessageContent{Parts: parts})
	if role == "assistant" {
		a.Messages = append(a.Messages, MessageAssistant{Role: role, Content: content})
		return
	}
	a.Messages = append(a.Messages, MessageUser{Role: role, Content: content})
}

func NewApiRequest(model string) ApiRequest {
	return ApiRequest{
		Model:                      model,
		CanUseTools:                true,
		ReasoningEffort:            "none",
		CanUseApproxLocation:       nil,
		CanDelegateImageGeneration: nil,
		Metadata: Metadata{
			ToolChoice: ToolChoice{
				NewsSearch:      false,
				VideosSearch:    false,
				LocalSearch:     false,
				WeatherForecast: false,
			},
		},
	}
}
