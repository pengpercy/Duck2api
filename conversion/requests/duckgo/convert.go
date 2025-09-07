package duckgo

import (
	duckgotypes "aurora/typings/duckgo"
	officialtypes "aurora/typings/official"
)

func ConvertAPIRequest(apiRequest officialtypes.APIRequest) duckgotypes.ApiRequest {
	duckgoRequest := duckgotypes.NewApiRequest(apiRequest.Model)
	duckgoRequest.Model = apiRequest.Model
	buildMessage(&apiRequest, &duckgoRequest)
	return duckgoRequest
}

func buildMessage(apiRequest *officialtypes.APIRequest, duckgoRequest *duckgotypes.ApiRequest) {
	duckgoRequest.CanUseTools = true
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
		"user":      true,
		"system":    true,
		"assistant": true,
	}
	return validRoles[role]
}

func normalizeRole(role string) string {
	if role == "system" {
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
		return duckgotypes.PartImage{
			Type:     "image",
			MimeType: "image/webp",
			Image:    elementMap["image_url"].(map[string]any)["url"].(string),
		}
	default:
		return nil
	}
}
