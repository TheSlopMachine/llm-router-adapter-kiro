package kiro

import (
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
