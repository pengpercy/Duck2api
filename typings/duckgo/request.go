package duckgo

type ApiRequest struct {
	Model       string   `json:"model"`
	Messages    []any    `json:"messages"`
	CanUseTools bool     `json:"canUseTools"`
	Metadata    metadata `json:"metadata"`
}

type messages struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type metadata struct {
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
