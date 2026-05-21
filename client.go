package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sdk "github.com/TheSlopMachine/llm-router-sdk"
)

const (
	socialAuthServiceURL = "https://prod.us-east-1.auth.desktop.kiro.dev"
	socialRefreshURL     = socialAuthServiceURL + "/refreshToken"
	socialTokenURL       = socialAuthServiceURL + "/oauth/token"
	kiroBuilderStartURL  = "https://view.awsapps.com/start"
	kiroIssuerURL        = "https://identitycenter.amazonaws.com/ssoins-722374e8c3c8e6c6"
)

var (
	kiroScopes = []string{
		"codewhisperer:completions",
		"codewhisperer:analysis",
		"codewhisperer:conversations",
	}
	kiroGrantTypes = []string{
		"urn:ietf:params:oauth:grant-type:device_code",
		"refresh_token",
	}
)

type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{},
	}
}

func (c *Client) RegisterClient(ctx context.Context, region string) (*clientRegistrationResponse, error) {
	if strings.TrimSpace(region) == "" {
		region = defaultRegion
	}
	endpoint := fmt.Sprintf("https://oidc.%s.amazonaws.com/client/register", region)
	payload := map[string]any{
		"clientName": "kiro-oauth-client",
		"clientType": "public",
		"scopes":     kiroScopes,
		"grantTypes": kiroGrantTypes,
		"issuerUrl":  kiroIssuerURL,
	}

	var out clientRegistrationResponse
	if err := c.postJSON(ctx, endpoint, payload, &out); err != nil {
		return nil, err
	}
	if out.ClientID == "" || out.ClientSecret == "" {
		return nil, fmt.Errorf("kiro: client registration response was incomplete")
	}
	return &out, nil
}

func (c *Client) StartDeviceAuthorization(
	ctx context.Context,
	clientID, clientSecret, startURL, region string,
) (*deviceAuthorizationResponse, error) {
	if strings.TrimSpace(region) == "" {
		region = defaultRegion
	}
	endpoint := fmt.Sprintf("https://oidc.%s.amazonaws.com/device_authorization", region)
	payload := map[string]string{
		"clientId":     clientID,
		"clientSecret": clientSecret,
		"startUrl":     startURL,
	}

	var out deviceAuthorizationResponse
	if err := c.postJSON(ctx, endpoint, payload, &out); err != nil {
		return nil, err
	}
	if out.Interval <= 0 {
		out.Interval = 5
	}
	if out.DeviceCode == "" || out.UserCode == "" {
		return nil, fmt.Errorf("kiro: device authorization response was incomplete")
	}
	return &out, nil
}

func (c *Client) PollDeviceToken(
	ctx context.Context,
	clientID, clientSecret, deviceCode, region string,
) (*devicePollResult, error) {
	if strings.TrimSpace(region) == "" {
		region = defaultRegion
	}
	endpoint := fmt.Sprintf("https://oidc.%s.amazonaws.com/token", region)
	payload := map[string]string{
		"clientId":     clientID,
		"clientSecret": clientSecret,
		"deviceCode":   deviceCode,
		"grantType":    "urn:ietf:params:oauth:grant-type:device_code",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("kiro: marshal device token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("kiro: create device token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kiro: device token request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kiro: read device token response: %w", err)
	}

	var parsed deviceTokenResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("kiro: parse device token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK || parsed.Error != "" {
		if parsed.Error == "authorization_pending" || parsed.Error == "slow_down" {
			message := parsed.ErrorDescription
			if message == "" {
				message = parsed.Error
			}
			return &devicePollResult{Pending: true, ErrorDescription: message}, nil
		}
		if parsed.ErrorDescription != "" {
			return nil, fmt.Errorf("kiro: %s", parsed.ErrorDescription)
		}
		return nil, fmt.Errorf("kiro: device login failed")
	}

	return &devicePollResult{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		ExpiresIn:    parsed.ExpiresIn,
	}, nil
}

func (c *Client) BuildSocialLoginURL(provider, codeChallenge, state string) string {
	idp := "Google"
	if provider == "github" {
		idp = "Github"
	}
	redirectURI := "kiro://kiro.kiroAgent/authenticate-success"
	return fmt.Sprintf(
		"%s/login?idp=%s&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256&state=%s&prompt=select_account",
		socialAuthServiceURL,
		idp,
		urlQueryEscape(redirectURI),
		urlQueryEscape(codeChallenge),
		urlQueryEscape(state),
	)
}

func (c *Client) ExchangeSocialCode(ctx context.Context, code, codeVerifier string) (*tokenRefreshResponse, error) {
	payload := map[string]string{
		"code":          code,
		"code_verifier": codeVerifier,
		"redirect_uri":  "kiro://kiro.kiroAgent/authenticate-success",
	}

	var out tokenRefreshResponse
	if err := c.postJSON(ctx, socialTokenURL, payload, &out); err != nil {
		return nil, err
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("kiro: token exchange did not return an access token")
	}
	return &out, nil
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
	if !strings.HasPrefix(refreshToken, "aorAAAAAG") {
		return nil, fmt.Errorf("Kiro refresh token should start with \"aorAAAAAG\"")
	}
	return c.RefreshCredential(ctx, map[string]string{
		refreshTokenField: refreshToken,
		authMethodField:   "imported",
	})
}

func findLocalRefreshToken() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("kiro: failed to resolve user home directory")
	}

	cacheDir := filepath.Join(home, ".aws", "sso", "cache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return "", "", fmt.Errorf("kiro: AWS SSO cache not found at %s", cacheDir)
	}

	preferred := []string{"kiro-auth-token.json", "amazon-q-auth-token.json"}
	for _, name := range preferred {
		token, source := tokenFromEntry(cacheDir, entries, name)
		if token != "" {
			return token, source, nil
		}
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		token, source := readRefreshTokenFile(filepath.Join(cacheDir, entry.Name()))
		if token != "" {
			return token, source, nil
		}
	}

	return "", "", fmt.Errorf("kiro: no Kiro refresh token found in %s", cacheDir)
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

func tokenFromEntry(cacheDir string, entries []os.DirEntry, target string) (string, string) {
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() != target {
			continue
		}
		return readRefreshTokenFile(filepath.Join(cacheDir, entry.Name()))
	}
	return "", ""
}

func readRefreshTokenFile(path string) (string, string) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	var payload struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.Unmarshal(content, &payload); err != nil {
		return "", ""
	}
	if strings.HasPrefix(payload.RefreshToken, "aorAAAAAG") {
		return payload.RefreshToken, path
	}
	return "", ""
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

func (c *Client) postJSON(ctx context.Context, endpoint string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("kiro: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("kiro: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("kiro: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("kiro: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return parseKiroError(resp.StatusCode, resp.Header, respBody)
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("kiro: parse response: %w", err)
	}
	return nil
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

func urlQueryEscape(value string) string {
	replacer := strings.NewReplacer(
		"%", "%25",
		" ", "%20",
		"!", "%21",
		"\"", "%22",
		"#", "%23",
		"$", "%24",
		"&", "%26",
		"'", "%27",
		"(", "%28",
		")", "%29",
		"+", "%2B",
		",", "%2C",
		"/", "%2F",
		":", "%3A",
		";", "%3B",
		"=", "%3D",
		"?", "%3F",
		"@", "%40",
		"[", "%5B",
		"]", "%5D",
	)
	return replacer.Replace(value)
}
