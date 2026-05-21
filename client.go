package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	sdk "github.com/TheSlopMachine/llm-router-sdk"
)

const socialRefreshURL = "https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken"

type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{},
	}
}

func (c *Client) Generate(
	ctx context.Context,
	cred *sdk.Credential,
	req *generateAssistantResponseRequest,
	modelID string,
) (*sdk.ChatCompletionResponse, error) {
	resp, err := c.doGenerateRequest(ctx, cred, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kiro: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseKiroError(resp.StatusCode, resp.Header, body)
	}

	state, err := parseEventStreamResponse(body)
	if err != nil {
		return nil, fmt.Errorf("kiro: parse response stream: %w", err)
	}
	if state.totalTokens == 0 {
		state.completionTokens = max(1, state.totalContentBytes/4)
		state.totalTokens = state.promptTokens + state.completionTokens
	}
	if state.finishReason == "" {
		state.finishReason = "stop"
	}

	return &sdk.ChatCompletionResponse{
		ID:      fmt.Sprintf("kiro-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   modelID,
		Choices: []sdk.ChatCompletionChoice{
			{
				Index: 0,
				Message: sdk.ChatMessage{
					Role:    "assistant",
					Content: state.content.String(),
				},
				FinishReason: state.finishReason,
			},
		},
		Usage: sdk.ChatCompletionUsage{
			PromptTokens:     state.promptTokens,
			CompletionTokens: state.completionTokens,
			TotalTokens:      state.totalTokens,
		},
	}, nil
}

func (c *Client) GenerateStream(
	ctx context.Context,
	cred *sdk.Credential,
	req *generateAssistantResponseRequest,
	modelID string,
	w io.Writer,
) error {
	resp, err := c.doGenerateRequest(ctx, cred, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return parseKiroError(resp.StatusCode, resp.Header, body)
	}

	return streamEventStreamToSSE(ctx, resp.Body, w, modelID)
}

func (c *Client) ValidateImportToken(ctx context.Context, refreshToken string) (*tokenRefreshResponse, error) {
	if !strings.HasPrefix(refreshToken, "aor") {
		return nil, fmt.Errorf("Kiro refresh token should usually start with \"aor\"")
	}
	return c.RefreshCredential(ctx, map[string]string{
		refreshTokenField: refreshToken,
		authMethodField:   "imported",
	})
}

func (c *Client) RefreshCredential(ctx context.Context, credData map[string]string) (*tokenRefreshResponse, error) {
	refreshToken := strings.TrimSpace(credData[refreshTokenField])
	if refreshToken == "" {
		return nil, fmt.Errorf("kiro: missing refresh token")
	}

	clientID := strings.TrimSpace(credData[clientIDField])
	clientSecret := strings.TrimSpace(credData[clientSecretField])
	region := strings.TrimSpace(credData[regionField])
	if region == "" {
		region = defaultRegion
	}

	var (
		url  string
		body []byte
		err  error
	)

	if clientID != "" && clientSecret != "" {
		url = fmt.Sprintf("https://oidc.%s.amazonaws.com/token", region)
		body, err = json.Marshal(map[string]string{
			"clientId":     clientID,
			"clientSecret": clientSecret,
			"refreshToken": refreshToken,
			"grantType":    "refresh_token",
		})
	} else {
		url = socialRefreshURL
		body, err = json.Marshal(map[string]string{
			"refreshToken": refreshToken,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("kiro: marshal refresh request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("kiro: create refresh request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("kiro: refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kiro: read refresh response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseKiroError(resp.StatusCode, resp.Header, respBody)
	}

	var refreshed tokenRefreshResponse
	if err := json.Unmarshal(respBody, &refreshed); err != nil {
		return nil, fmt.Errorf("kiro: parse refresh response: %w", err)
	}
	if refreshed.AccessToken == "" {
		return nil, fmt.Errorf("kiro: refresh response did not include an access token")
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = refreshToken
	}

	return &refreshed, nil
}

func (c *Client) doGenerateRequest(
	ctx context.Context,
	cred *sdk.Credential,
	req *generateAssistantResponseRequest,
) (*http.Response, error) {
	if cred == nil {
		return nil, fmt.Errorf("kiro: missing credential")
	}

	accessToken := strings.TrimSpace(cred.Data[accessTokenField])
	if accessToken == "" {
		return nil, fmt.Errorf("kiro: credential does not contain %s", accessTokenField)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("kiro: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, providerBaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("kiro: create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+accessToken)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/vnd.amazon.eventstream")
	httpReq.Header.Set("X-Amz-Target", "AmazonCodeWhispererStreamingService.GenerateAssistantResponse")
	httpReq.Header.Set("User-Agent", "AWS-SDK-JS/3.0.0 kiro-ide/1.0.0")
	httpReq.Header.Set("X-Amz-User-Agent", "aws-sdk-js/3.0.0 kiro-ide/1.0.0")
	httpReq.Header.Set("Amz-Sdk-Request", "attempt=1; max=3")
	httpReq.Header.Set("Amz-Sdk-Invocation-Id", newConversationID())
	httpReq.Header.Set("x-amzn-bedrock-cache-control", "enable")
	httpReq.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")

	return c.httpClient.Do(httpReq)
}

func parseKiroError(statusCode int, headers http.Header, body []byte) error {
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(statusCode)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err == nil {
		if extracted := nestedErrorMessage(payload); extracted != "" {
			message = extracted
		}
	}

	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &sdk.ProviderError{StatusCode: statusCode, Message: message, Type: sdk.ErrorTypeAuth}
	case http.StatusTooManyRequests:
		retryAfter := retryAfterFromHeaders(headers)
		if retryAfter == nil {
			t := time.Now().Add(time.Minute)
			retryAfter = &t
		}
		if strings.Contains(strings.ToLower(message), "quota") {
			return &sdk.ProviderError{StatusCode: statusCode, Message: message, Type: sdk.ErrorTypeQuotaExceeded, RetryAfter: retryAfter}
		}
		return &sdk.ProviderError{StatusCode: statusCode, Message: message, Type: sdk.ErrorTypeRateLimit, RetryAfter: retryAfter}
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return &sdk.ProviderError{StatusCode: statusCode, Message: message, Type: sdk.ErrorTypeTimeout}
	default:
		if statusCode >= 500 {
			return &sdk.ProviderError{StatusCode: statusCode, Message: message, Type: sdk.ErrorTypeUpstream}
		}
		return fmt.Errorf("kiro: upstream error (%d): %s", statusCode, message)
	}
}

func nestedErrorMessage(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if message, ok := payload["message"].(string); ok && message != "" {
		return message
	}
	if nested, ok := payload["error"].(map[string]any); ok {
		if message, ok := nested["message"].(string); ok && message != "" {
			return message
		}
	}
	return ""
}

func retryAfterFromHeaders(headers http.Header) *time.Time {
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return nil
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		t := time.Now().Add(time.Duration(seconds) * time.Second)
		return &t
	}
	if parsed, err := http.ParseTime(raw); err == nil {
		return &parsed
	}
	return nil
}

func parseExpiry(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed, true
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Unix(seconds, 0).UTC(), true
	}
	return time.Time{}, false
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
