package duckgo

import (
	duckgotypes "aurora/typings/duckgo"
	officialtypes "aurora/typings/official"
	"fmt"
	"regexp"
)

func ConvertAPIRequest(apiRequest officialtypes.APIRequest) duckgotypes.ApiRequest {
	duckgoRequest := duckgotypes.NewApiRequest(apiRequest.Model)
	duckgoRequest.Model = apiRequest.Model
	buildMessage(&apiRequest, &duckgoRequest)
	return duckgoRequest
}

func buildMessage(apiRequest *officialtypes.APIRequest, duckgoRequest *duckgotypes.ApiRequest) {
	duckgoRequest.CanUseTools = true
	duckgoRequest.ReasoningEffort = "minimal"
	duckgoRequest.Metadata.ToolChoice = duckgotypes.Tool{
		LocalSearch:     false,
		NewsSearch:      false,
		VideosSearch:    false,
		WeatherForecast: false,
	}
	for _, msg := range apiRequest.Messages {
		if !isValidRole(msg.Role) {
			continue
		}

		role := normalizeRole(msg.Role)
		switch role {
		case "user":
			handleUserMessage(msg.Content, duckgoRequest)
		case "assistant":
			handleAssistantMessage(msg.Content, duckgoRequest)
		}
	}
}

func isValidRole(role string) bool {
	validRoles := map[string]bool{
		"developer": true,
		"user":      true,
		"system":    true,
		"assistant": true,
	}
	return validRoles[role]
}

func normalizeRole(role string) string {
	if role == "system" || role == "developer" {
		return "user"
	}
	return role
}

func handleUserMessage(content any, duckgoRequest *duckgotypes.ApiRequest) {
	if arrayContent, ok := content.([]any); ok {
		parts := buildMessageParts(arrayContent)
		duckgoRequest.AddMessageUser(parts)
	} else {
		duckgoRequest.AddMessageUser(content)
	}
}

func handleAssistantMessage(content any, duckgoRequest *duckgotypes.ApiRequest) {
	parts := []any{}
	if arrayContent, ok := content.([]any); ok {
		parts = buildMessageParts(arrayContent)
	} else {
		parts = append(parts, duckgotypes.PartText{
			Type: "text",
			Text: content.(string),
		})
	}
	duckgoRequest.AddMessageAssistant(parts)
}

func buildMessageParts(content []any) []any {
	var parts []any
	for _, element := range content {
		if elementMap, ok := element.(map[string]any); ok {
			part := createPart(elementMap)
			if part != nil {
				parts = append(parts, part)
			}
		}
	}
	return parts
}

func createPart(elementMap map[string]any) any {
	switch elementMap["type"] {
	case "text":
		return duckgotypes.PartText{
			Type: "text",
			Text: elementMap["text"].(string),
		}
	case "image_url":
		dataURL := elementMap["image_url"].(map[string]any)["url"].(string)
		mime, _ := GetMimeType(dataURL)
		return duckgotypes.PartImage{
			Type:     "image",
			MimeType: mime,
			Image:    dataURL,
		}
	default:
		return nil
	}
}

func GetMimeType(dataURL string) (string, error) {
	re := regexp.MustCompile(`^data:([^;]+)`)
	matches := re.FindStringSubmatch(dataURL)
	if len(matches) > 1 {
		return matches[1], nil
	}
	return "", fmt.Errorf("无法提取 MIME 类型")
}
