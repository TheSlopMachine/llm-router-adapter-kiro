package kiro

import (
	"strings"
	"testing"
	"time"

	sdk "github.com/TheSlopMachine/llm-router-sdk"
)

func TestConvertMessagesUsesLastUserAsCurrentMessage(t *testing.T) {
	messages := []sdk.ChatMessage{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "first user"},
		{Role: "assistant", Content: "assistant reply"},
		{Role: "user", Content: "final user"},
	}

	history, current := convertMessages(messages, nil, "claude-sonnet-4.6")

	if current.Content != "final user" {
		t.Fatalf("expected last user message as current content, got %q", current.Content)
	}
	if current.ModelID != "claude-sonnet-4.6" {
		t.Fatalf("expected model id to be propagated, got %q", current.ModelID)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(history))
	}
	if history[0].UserInputMessage == nil || history[0].UserInputMessage.Content != "system\n\nfirst user" {
		t.Fatalf("unexpected first history entry: %#v", history[0])
	}
}

func TestConvertMessagesPreservesToolCallsAndToolResults(t *testing.T) {
	messages := []sdk.ChatMessage{
		{Role: "system", Content: "Rules"},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "I can help"},
		{
			Role: "assistant",
			ToolCalls: []sdk.ChatToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: sdk.ChatToolFunction{
						Name:      "read_file",
						Arguments: `{"path":"/tmp/a"}`,
					},
				},
			},
		},
		{Role: "tool", ToolCallID: "call_1", Content: "file contents"},
		{
			Role: "user",
			ContentParts: []sdk.ChatMessageContentPart{
				{Type: "text", Text: "Thanks"},
				{
					Type:      "tool_result",
					ToolUseID: "call_1",
					Content: []sdk.ChatMessageContentPart{
						{Type: "text", Text: "done"},
					},
				},
			},
			Content: "Thanks\ndone",
		},
	}
	tools := []sdk.ChatTool{
		{
			Type: "function",
			Function: &sdk.ChatToolFunction{
				Name:        "read_file",
				Description: "Read",
				Parameters: map[string]any{
					"type": "object",
				},
			},
		},
	}

	history, current := convertMessages(messages, tools, "claude-sonnet-4.6")

	if len(history) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(history))
	}
	if got := history[1].AssistantResponseMessage.ToolUses[0].Name; got != "read_file" {
		t.Fatalf("expected tool use to be preserved, got %#v", history[1].AssistantResponseMessage.ToolUses)
	}
	if got := current.UserInputMessageContext.ToolResults; len(got) != 2 {
		t.Fatalf("expected two tool results on current message, got %#v", got)
	}
	if got := current.UserInputMessageContext.Tools; len(got) != 1 || got[0].ToolSpecification.Name != "read_file" {
		t.Fatalf("expected tools to be attached to current message, got %#v", current.UserInputMessageContext.Tools)
	}
	if history[0].UserInputMessage.UserInputMessageContext != nil && len(history[0].UserInputMessage.UserInputMessageContext.Tools) != 0 {
		t.Fatalf("expected tools to be removed from history for API compatibility, got %#v", history[0].UserInputMessage.UserInputMessageContext)
	}
	if current.Content != "Thanks" {
		t.Fatalf("expected current user content to exclude tool_result text, got %q", current.Content)
	}
}

func TestDeterministicConversationID(t *testing.T) {
	messages := []sdk.ChatMessage{
		{Role: "system", Content: "Rules"},
		{Role: "user", Content: "Hello"},
	}

	req := &sdk.ChatCompletionRequest{
		Model:    "kiro/claude-sonnet-4.6",
		Messages: messages,
	}

	first := transformRequest(req, "claude-sonnet-4.6", nil)
	second := transformRequest(req, "claude-sonnet-4.6", nil)
	if first.ConversationState.ConversationID != second.ConversationState.ConversationID {
		t.Fatalf("expected stable conversation ids, got %q vs %q", first.ConversationState.ConversationID, second.ConversationState.ConversationID)
	}
}

func TestBuildToolSpecsNormalizesVerboseSchemasForKiro(t *testing.T) {
	tools := []sdk.ChatTool{
		{
			Type: "function",
			Function: &sdk.ChatToolFunction{
				Name: "todowrite",
				Description: "Create and maintain a structured task list for the current coding session. " +
					"Tracks progress, organizes multi-step work, and surfaces status to the user.\n\n" +
					"Use proactively when there are 3+ steps.",
				Parameters: map[string]any{
					"$schema": "https://json-schema.org/draft/2020-12/schema",
					"type":    "object",
					"title":   "Todo Write",
					"properties": map[string]any{
						"todos": map[string]any{
							"type":        "array",
							"description": "The updated todo list for the current session with enough detail to execute accurately and keep the user informed.",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"content":  map[string]any{"type": "string", "description": "Task text"},
									"status":   map[string]any{"type": "string", "enum": []any{"pending", "in_progress", "completed", "cancelled"}},
									"priority": map[string]any{"type": "string", "default": "medium"},
								},
								"required": []any{"content", "status", "priority"},
							},
						},
					},
					"required": []any{"todos"},
				},
			},
		},
	}

	specs := buildToolSpecs(tools)
	if len(specs) != 1 {
		t.Fatalf("expected one tool spec, got %d", len(specs))
	}

	spec := specs[0].ToolSpecification
	if !spec.Strict {
		t.Fatalf("expected strict mode to be enabled")
	}
	if !strings.Contains(spec.Description, `"todos"`) || !strings.Contains(spec.Description, "Always include the todos array") {
		t.Fatalf("expected todowrite description to spell out required payload, got %q", spec.Description)
	}

	schema := spec.InputSchema.JSON
	if _, ok := schema["$schema"]; ok {
		t.Fatalf("expected $schema to be removed, got %#v", schema)
	}
	if _, ok := schema["title"]; ok {
		t.Fatalf("expected title to be removed, got %#v", schema)
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("expected top-level additionalProperties=false, got %#v", schema["additionalProperties"])
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties object, got %#v", schema["properties"])
	}
	todos, ok := props["todos"].(map[string]any)
	if !ok {
		t.Fatalf("expected todos schema, got %#v", props["todos"])
	}
	items, ok := todos["items"].(map[string]any)
	if !ok {
		t.Fatalf("expected todos.items schema, got %#v", todos["items"])
	}
	itemProps, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested properties, got %#v", items["properties"])
	}
	priority, ok := itemProps["priority"].(map[string]any)
	if !ok {
		t.Fatalf("expected priority schema, got %#v", itemProps["priority"])
	}
	if _, ok := priority["default"]; ok {
		t.Fatalf("expected default to be removed from nested schema, got %#v", priority)
	}
}

func TestNeedsRefresh(t *testing.T) {
	adapter := &Adapter{}

	if adapter.NeedsRefresh(&sdk.Credential{
		Data: map[string]string{
			refreshTokenField: "refresh-token",
		},
	}) != true {
		t.Fatal("expected missing access token to trigger refresh")
	}

	if adapter.NeedsRefresh(&sdk.Credential{
		Data: map[string]string{
			accessTokenField:  "access-token",
			refreshTokenField: "refresh-token",
			expiresAtField:    time.Now().Add(10 * time.Minute).Format(time.RFC3339),
		},
	}) {
		t.Fatal("did not expect refresh when token is still healthy")
	}

	if !adapter.NeedsRefresh(&sdk.Credential{
		Data: map[string]string{
			accessTokenField:  "access-token",
			refreshTokenField: "refresh-token",
			expiresAtField:    time.Now().Add(2 * time.Minute).Format(time.RFC3339),
		},
	}) {
		t.Fatal("expected refresh when token is near expiry")
	}
}
