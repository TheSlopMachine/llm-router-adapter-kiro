package kiro

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	sdk "github.com/TheSlopMachine/llm-router-sdk"
)

func transformRequest(
	req *sdk.ChatCompletionRequest,
	modelName string,
	cred *sdk.Credential,
) *generateAssistantResponseRequest {
	history, current := convertMessages(req.Messages, modelName)

	if current.Content == "" {
		current.Content = "continue"
	}

	current.Content = fmt.Sprintf("[Context: Current time is %s]\n\n%s", time.Now().UTC().Format(time.RFC3339), current.Content)
	current.Origin = "AI_EDITOR"

	out := &generateAssistantResponseRequest{
		ConversationState: conversationState{
			ChatTriggerType: "MANUAL",
			ConversationID:  newConversationID(),
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

	if cred != nil {
		if profileARN := strings.TrimSpace(cred.Data[profileARNField]); profileARN != "" {
			out.ProfileARN = profileARN
		}
	}

	return out
}

func convertMessages(messages []sdk.ChatMessage, modelName string) ([]conversationEntry, userInputMessage) {
	var (
		history        []conversationEntry
		currentRole    string
		userParts      []string
		assistantParts []string
	)

	flush := func() {
		switch currentRole {
		case "user":
			content := strings.TrimSpace(strings.Join(userParts, "\n\n"))
			if content != "" {
				history = append(history, conversationEntry{
					UserInputMessage: &userInputMessage{
						Content: content,
						ModelID: modelName,
					},
				})
			}
			userParts = nil
		case "assistant":
			content := strings.TrimSpace(strings.Join(assistantParts, "\n\n"))
			if content != "" {
				history = append(history, conversationEntry{
					AssistantResponseMessage: &assistantResponseMessage{
						Content: content,
					},
				})
			}
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
			assistantParts = append(assistantParts, msg.Content)
			continue
		}
		userParts = append(userParts, msg.Content)
	}

	if currentRole != "" {
		flush()
	}

	if len(history) > 0 {
		last := history[len(history)-1]
		if last.UserInputMessage != nil {
			history = history[:len(history)-1]
			return history, *last.UserInputMessage
		}
	}

	return history, userInputMessage{
		Content: "continue",
		ModelID: modelName,
	}
}

func newConversationID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("kiro-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}
