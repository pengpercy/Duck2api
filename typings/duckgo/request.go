package duckgo

type ApiRequest struct {
	Model       string     `json:"model"`
	Messages    []messages `json:"messages"`
	CanUseTools bool       `json:"canUseTools"`
	Metadata    metadata   `json:"metadata"`
}
type messages struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type metadata struct {
}

func (a *ApiRequest) AddMessage(role string, content string) {
	a.Messages = append(a.Messages, messages{
		Role:    role,
		Content: content,
	})
}

func NewApiRequest(model string) ApiRequest {
	return ApiRequest{
		Model: model,
	}
}
