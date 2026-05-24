package duckgo

type ApiRequest struct {
	Model                string         `json:"model"`
	Messages             []any          `json:"messages"`
	CanUseTools          bool           `json:"canUseTools"`
	CanUseApproxLocation any            `json:"canUseApproxLocation"`
	Metadata             Metadata       `json:"metadata"`
	ReasoningEffort      string         `json:"reasoningEffort"`
	DurableStream        *DurableStream `json:"durableStream,omitempty"`
}

type messages struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type Metadata struct {
	ToolChoice Tool `json:"toolChoice"`
}

type Tool struct {
	LocalSearch     bool `json:"LocalSearch"`
	NewsSearch      bool `json:"NewsSearch"`
	VideosSearch    bool `json:"VideosSearch"`
	WeatherForecast bool `json:"WeatherForecast"`
}

type DurableStream struct {
	MessageID      string    `json:"messageId"`
	ConversationID string    `json:"conversationId"`
	PublicKey      PublicKey `json:"publicKey"`
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

type MessageUser struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type MessageAssistant struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Parts   []any  `json:"parts"`
}

type PartText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type PartImage struct {
	Type     string `json:"type"`
	MimeType string `json:"mimeType"`
	Image    string `json:"image"`
}

func (a *ApiRequest) AddMessage(role string, content any) {
	a.Messages = append(a.Messages, messages{
		Role:    role,
		Content: content,
	})
}

func (a *ApiRequest) AddMessageUser(content any) {
	a.Messages = append(a.Messages, MessageUser{
		Role:    "user",
		Content: content,
	})
}

func (a *ApiRequest) AddMessageAssistant(parts []any) {
	a.Messages = append(a.Messages, MessageAssistant{
		Role:    "assistant",
		Content: "",
		Parts:   parts,
	})
}

func NewApiRequest(model string) ApiRequest {
	return ApiRequest{
		Model: model,
	}
}
