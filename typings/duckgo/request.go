package duckgo

type ApiRequest struct {
	Model       string     `json:"model"`
	Messages    []messages `json:"messages"`
	CanUseTools bool       `json:"canUseTools"`
	Metadata    metadata   `json:"metadata"`
}
type messages struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
	Parts   []Part `json:"parts"`
}
type metadata struct {
}
type Part struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (a *ApiRequest) AddMessage(role string, content any, parts []Part) {
	a.Messages = append(a.Messages, messages{
		Role:    role,
		Content: content,
		Parts:   parts,
	})
}

func NewApiRequest(model string) ApiRequest {
	return ApiRequest{
		Model: model,
	}
}
