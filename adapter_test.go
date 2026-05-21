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

	history, current := convertMessages(messages, "claude-sonnet-4.6")

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
