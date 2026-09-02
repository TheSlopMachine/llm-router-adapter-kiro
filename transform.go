package kiro

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	sdk "github.com/TheSlopMachine/llm-router-sdk"
)

func transformRequest(
	req *sdk.ChatCompletionRequest,
	modelName string,
	_ *sdk.Credential,
) *generateAssistantResponseRequest {
	history, current := convertMessages(req.Messages, req.Tools, modelName)

	if strings.TrimSpace(current.Content) == "" {
		current.Content = "continue"
	}

	current.Content = fmt.Sprintf("[Context: Current time is %s]\n\n%s", time.Now().UTC().Format(time.RFC3339), current.Content)
	current.Origin = "AI_EDITOR"

	out := &generateAssistantResponseRequest{
		ConversationState: conversationState{
			ChatTriggerType: "MANUAL",
			ConversationID:  deterministicConversationID(history, current.Content),
			CurrentMessage: currentMessage{
				UserInputMessage: current,
			},
			History: history,
		},
	}

	if req.MaxTokens > 0 || req.Temperature > 0 || req.TopP > 0 {
		out.InferenceConfig = &inferenceConfig{}
		if req.MaxTokens > 0 {
			maxTokens := req.MaxTokens
			out.InferenceConfig.MaxTokens = &maxTokens
		}
		if req.Temperature > 0 {
			temperature := req.Temperature
			out.InferenceConfig.Temperature = &temperature
		}
		if req.TopP > 0 {
			topP := req.TopP
			out.InferenceConfig.TopP = &topP
		}
	}

	debugJSON(context.Background(), "kiro transformed request",
		out,
		"model", modelName,
		"messages", len(req.Messages),
		"tools", len(req.Tools),
	)
	return out
}

func convertMessages(messages []sdk.ChatMessage, tools []sdk.ChatTool, modelName string) ([]conversationEntry, userInputMessage) {
	var (
		history        []conversationEntry
		currentRole    string
		userParts      []string
		assistantParts []string
		toolResults    []toolResult
	)

	flush := func() {
		switch currentRole {
		case "user":
			content := strings.TrimSpace(strings.Join(userParts, "\n\n"))
			if content == "" && len(toolResults) == 0 {
				userParts = nil
				return
			}
			msg := &userInputMessage{
				Content: content,
				ModelID: modelName,
			}
			if msg.Content == "" {
				msg.Content = "continue"
			}
			if len(toolResults) > 0 {
				msg.UserInputMessageContext = &userInputMessageContext{
					ToolResults: toolResults,
				}
			}
			if len(tools) > 0 && len(history) == 0 {
				if msg.UserInputMessageContext == nil {
					msg.UserInputMessageContext = &userInputMessageContext{}
				}
				msg.UserInputMessageContext.Tools = buildToolSpecs(tools)
			}
			history = append(history, conversationEntry{UserInputMessage: msg})
			userParts = nil
			toolResults = nil
		case "assistant":
			content := strings.TrimSpace(strings.Join(assistantParts, "\n\n"))
			if content == "" {
				content = "..."
			}
			history = append(history, conversationEntry{
				AssistantResponseMessage: &assistantResponseMessage{
					Content: content,
				},
			})
			assistantParts = nil
		}
	}

	for _, msg := range messages {
		role := msg.Role
		switch role {
		case "assistant":
			role = "assistant"
		default:
			role = "user"
		}

		if currentRole != "" && currentRole != role {
			flush()
		}
		currentRole = role

		if role == "assistant" {
			textContent := strings.TrimSpace(msg.Content)
			if textContent != "" {
				assistantParts = append(assistantParts, textContent)
			}

			toolUses := extractToolUses(msg)
			if len(toolUses) > 0 {
				flush()
				history[len(history)-1].AssistantResponseMessage.ToolUses = append(
					history[len(history)-1].AssistantResponseMessage.ToolUses,
					toolUses...,
				)
				currentRole = ""
			}
			continue
		}

		if msg.Role == "tool" {
			toolResults = appendToolResult(toolResults, msg.ToolCallID, msg.TextContent())
			continue
		}

		textParts, contentToolResults := extractUserContent(msg)
		toolResults = append(toolResults, contentToolResults...)
		if text := strings.TrimSpace(strings.Join(textParts, "\n")); text != "" {
			userParts = append(userParts, text)
		}
	}

	if currentRole != "" {
		flush()
	}

	var current userInputMessage
	if len(history) > 0 {
		last := history[len(history)-1]
		if last.UserInputMessage != nil {
			history = history[:len(history)-1]
			current = *last.UserInputMessage
		}
	}

	if current.Content == "" {
		current = userInputMessage{
			Content: "continue",
			ModelID: modelName,
		}
	}
	if current.ModelID == "" {
		current.ModelID = modelName
	}
	if len(tools) > 0 && (current.UserInputMessageContext == nil || len(current.UserInputMessageContext.Tools) == 0) {
		if current.UserInputMessageContext == nil {
			current.UserInputMessageContext = &userInputMessageContext{}
		}
		if len(history) > 0 && history[0].UserInputMessage != nil && history[0].UserInputMessage.UserInputMessageContext != nil && len(history[0].UserInputMessage.UserInputMessageContext.Tools) > 0 {
			current.UserInputMessageContext.Tools = history[0].UserInputMessage.UserInputMessageContext.Tools
		} else {
			current.UserInputMessageContext.Tools = buildToolSpecs(tools)
		}
	}

	for i := range history {
		entry := history[i].UserInputMessage
		if entry == nil || entry.UserInputMessageContext == nil {
			continue
		}
		entry.UserInputMessageContext.Tools = nil
		if len(entry.UserInputMessageContext.ToolResults) == 0 {
			entry.UserInputMessageContext = nil
		}
		if entry.ModelID == "" {
			entry.ModelID = modelName
		}
	}

	return history, current
}

func extractUserContent(msg sdk.ChatMessage) ([]string, []toolResult) {
	if len(msg.ContentParts) == 0 {
		text := strings.TrimSpace(msg.Content)
		if text == "" {
			return nil, nil
		}
		return []string{text}, nil
	}

	texts := make([]string, 0, len(msg.ContentParts))
	results := make([]toolResult, 0)
	for _, part := range msg.ContentParts {
		if part.Type == "tool_result" {
			results = appendToolResult(results, part.ToolUseID, part.TextContent())
			continue
		}
		if text := strings.TrimSpace(part.TextContent()); text != "" {
			texts = append(texts, text)
		}
	}
	return texts, results
}

func extractToolUses(msg sdk.ChatMessage) []toolUse {
	toolUses := make([]toolUse, 0, len(msg.ToolCalls))
	for _, call := range msg.ToolCalls {
		name := strings.TrimSpace(call.Function.Name)
		if name == "" {
			continue
		}
		toolUses = append(toolUses, toolUse{
			ToolUseID: fallbackToolUseID(call.ID),
			Name:      name,
			Input:     parseToolInput(call.Function.Arguments),
		})
	}

	for _, part := range msg.ContentParts {
		if part.Type != "tool_use" || strings.TrimSpace(part.Name) == "" {
			continue
		}
		toolUses = append(toolUses, toolUse{
			ToolUseID: fallbackToolUseID(part.ID),
			Name:      strings.TrimSpace(part.Name),
			Input:     parseRawObject(part.Input),
		})
	}

	return toolUses
}

func buildToolSpecs(tools []sdk.ChatTool) []toolSpec {
	specs := make([]toolSpec, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		description := strings.TrimSpace(tool.Description)
		inputSchema := tool.Parameters

		if tool.Function != nil {
			if strings.TrimSpace(tool.Function.Name) != "" {
				name = strings.TrimSpace(tool.Function.Name)
			}
			if strings.TrimSpace(tool.Function.Description) != "" {
				description = strings.TrimSpace(tool.Function.Description)
			}
			if len(tool.Function.Parameters) > 0 {
				inputSchema = tool.Function.Parameters
			}
		}

		if name == "" {
			continue
		}
		if description == "" {
			description = "Tool: " + name
		}
		description = normalizeToolDescription(name, description, inputSchema)
		inputSchema = normalizeToolSchema(inputSchema)

		specs = append(specs, toolSpec{
			ToolSpecification: toolSpecification{
				Name:        name,
				Description: description,
				InputSchema: inputSchemaBody{JSON: inputSchema},
				Strict:      true,
			},
		})
	}
	return specs
}

var whitespaceRE = regexp.MustCompile(`\s+`)

func normalizeToolDescription(name, description string, schema map[string]any) string {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" {
		return description
	}

	if strings.EqualFold(name, "todowrite") {
		return `Write or update the todo list. Required input: {"todos":[{"content":"task","status":"pending|in_progress|completed|cancelled","priority":"high|medium|low"}]}. Always include the todos array.`
	}

	if description == "" {
		return "Tool: " + name
	}

	description = whitespaceRE.ReplaceAllString(description, " ")
	if sentence := firstSentence(description); sentence != "" {
		description = sentence
	}

	required := extractRequiredProperties(schema)
	if len(required) > 0 && !strings.Contains(strings.ToLower(description), "required") {
		description += " Required fields: " + strings.Join(required, ", ") + "."
	}

	const maxLen = 280
	if len(description) > maxLen {
		description = strings.TrimSpace(description[:maxLen-3]) + "..."
	}
	return description
}

func firstSentence(s string) string {
	for _, sep := range []string{". ", "\n", "\r"} {
		if idx := strings.Index(s, sep); idx > 0 {
			return strings.TrimSpace(s[:idx+1])
		}
	}
	return strings.TrimSpace(s)
}

func extractRequiredProperties(schema map[string]any) []string {
	required, ok := schema["required"]
	if !ok {
		return nil
	}
	names := toStringSlice(required)
	if len(names) == 0 {
		return nil
	}
	slices.Sort(names)
	return names
}

func normalizeToolSchema(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		}
	}

	normalized := normalizeSchemaNode(schema)
	out, ok := normalized.(map[string]any)
	if !ok || len(out) == 0 {
		return map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		}
	}
	if _, ok := out["type"]; !ok {
		out["type"] = "object"
	}
	if _, ok := out["properties"]; !ok && out["type"] == "object" {
		out["properties"] = map[string]any{}
	}
	if _, ok := out["additionalProperties"]; !ok && out["type"] == "object" {
		out["additionalProperties"] = false
	}
	return out
}

func normalizeSchemaNode(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any)
		for key, raw := range v {
			switch key {
			case "type", "format", "pattern", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum",
				"minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties",
				"uniqueItems", "const", "nullable":
				out[key] = raw
			case "description":
				if text := compactSchemaText(raw); text != "" {
					out[key] = text
				}
			case "enum":
				if items := normalizeSchemaArray(raw, false); len(items) > 0 {
					out[key] = items
				}
			case "required":
				if items := toStringSlice(raw); len(items) > 0 {
					out[key] = items
				}
			case "properties", "$defs", "definitions":
				if props := normalizeSchemaProperties(raw); len(props) > 0 && key == "properties" {
					out[key] = props
				}
			case "items", "additionalProperties":
				if child := normalizeSchemaNode(raw); child != nil {
					out[key] = child
				} else if boolVal, ok := raw.(bool); ok {
					out[key] = boolVal
				}
			case "anyOf", "oneOf", "allOf":
				if items := normalizeSchemaArray(raw, true); len(items) > 0 {
					out[key] = items
				}
			}
		}
		return out
	case []any:
		return normalizeSchemaArray(v, true)
	default:
		return value
	}
}

func normalizeSchemaProperties(raw any) map[string]any {
	props, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]any, len(props))
	for key, value := range props {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		child := normalizeSchemaNode(value)
		switch typed := child.(type) {
		case map[string]any:
			if len(typed) > 0 {
				out[key] = typed
			}
		default:
			if child != nil {
				out[key] = child
			}
		}
	}
	return out
}

func normalizeSchemaArray(raw any, objectsOnly bool) []any {
	items, ok := raw.([]any)
	if !ok {
		if strings, ok := raw.([]string); ok {
			out := make([]any, 0, len(strings))
			for _, item := range strings {
				out = append(out, item)
			}
			return out
		}
		return nil
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		normalized := normalizeSchemaNode(item)
		if normalized == nil {
			continue
		}
		if objectsOnly {
			if obj, ok := normalized.(map[string]any); ok && len(obj) > 0 {
				out = append(out, obj)
			}
			continue
		}
		out = append(out, normalized)
	}
	return out
}

func compactSchemaText(raw any) string {
	text, ok := raw.(string)
	if !ok {
		return ""
	}
	text = whitespaceRE.ReplaceAllString(strings.TrimSpace(text), " ")
	const maxLen = 160
	if len(text) > maxLen {
		text = strings.TrimSpace(text[:maxLen-3]) + "..."
	}
	return text
}

func toStringSlice(raw any) []string {
	values, ok := raw.([]any)
	if !ok {
		if strings, ok := raw.([]string); ok {
			return slices.Clone(strings)
		}
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		out = append(out, text)
	}
	return out
}

func appendToolResult(results []toolResult, toolUseID, text string) []toolResult {
	toolUseID = strings.TrimSpace(toolUseID)
	text = strings.TrimSpace(text)
	if toolUseID == "" && text == "" {
		return results
	}
	
	return append(results, toolResult{
		ToolUseID: toolUseID,
		Status:    "success",
		Content: []toolResultContent{
			{Text: text},
		},
	})
}

func parseToolInput(raw string) map[string]any {
	return parseRawObject(json.RawMessage(strings.TrimSpace(raw)))
}

func parseRawObject(raw json.RawMessage) map[string]any {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return map[string]any{}
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return map[string]any{}
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}

func fallbackToolUseID(id string) string {
	id = strings.TrimSpace(id)
	if id != "" {
		return id
	}
	return newConversationID()
}

func deterministicConversationID(history []conversationEntry, currentContent string) string {
	seed := currentContent
	if len(history) > 0 && history[0].UserInputMessage != nil && strings.TrimSpace(history[0].UserInputMessage.Content) != "" {
		seed = history[0].UserInputMessage.Content
	}
	if len(seed) > 4000 {
		seed = seed[:4000]
	}
	sum := sha1.Sum([]byte(kiroConversationNamespace + seed))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		sum[0:4],
		sum[4:6],
		sum[6:8],
		sum[8:10],
		sum[10:16],
	)
}

func newConversationID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("kiro-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}
