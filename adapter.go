package kiro

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	sdk "github.com/TheSlopMachine/llm-router-sdk"
)

const (
	adapterTypeKey   = "kiro"
	providerName     = "Kiro AI"
	providerBaseURL  = "https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse"
	defaultRegion    = "us-east-1"
	refreshWindow    = 5 * time.Minute
	defaultContext   = 200000
	defaultMaxTokens = 32000

	accessTokenField  = "access_token"
	refreshTokenField = "refresh_token"
	expiresAtField    = "expires_at"
	authMethodField   = "auth_method"
	clientIDField     = "client_id"
	clientSecretField = "client_secret"
	regionField       = "region"

	kiroConversationNamespace = "34f7193f-561d-4050-bc84-9547d953d6bf"
)

func init() {
	sdk.Register(&Adapter{})
}

type Adapter struct {
	client *Client
}

func (a *Adapter) TypeKey() string {
	return adapterTypeKey
}

func (a *Adapter) AuthType() sdk.AuthType {
	return sdk.AuthTypeOAuth2
}

func (a *Adapter) ValidateCredentials(data map[string]string) error {
	accessToken := strings.TrimSpace(data[accessTokenField])
	refreshToken := strings.TrimSpace(data[refreshTokenField])

	if accessToken == "" && refreshToken == "" {
		return fmt.Errorf("%s: either %s or %s is required", adapterTypeKey, accessTokenField, refreshTokenField)
	}
	if accessToken != "" && len(accessToken) < 20 {
		return fmt.Errorf("%s: %s appears invalid (too short)", adapterTypeKey, accessTokenField)
	}
	if refreshToken != "" && len(refreshToken) < 20 {
		return fmt.Errorf("%s: %s appears invalid (too short)", adapterTypeKey, refreshTokenField)
	}
	return nil
}

func (a *Adapter) Complete(
	ctx context.Context,
	cred *sdk.Credential,
	req *sdk.ChatCompletionRequest,
) (*sdk.ChatCompletionResponse, error) {
	_, modelName, err := req.Model.Parse()
	if err != nil {
		return nil, fmt.Errorf("invalid model id: %w", err)
	}

	return a.getClient().Generate(ctx, cred, transformRequest(req, modelName, cred), string(req.Model))
}

func (a *Adapter) CompleteStream(
	ctx context.Context,
	cred *sdk.Credential,
	req *sdk.ChatCompletionRequest,
	w io.Writer,
) error {
	_, modelName, err := req.Model.Parse()
	if err != nil {
		return fmt.Errorf("invalid model id: %w", err)
	}

	return a.getClient().GenerateStream(ctx, cred, transformRequest(req, modelName, cred), string(req.Model), w)
}

func (a *Adapter) NeedsRefresh(cred *sdk.Credential) bool {
	if cred == nil || strings.TrimSpace(cred.Data[refreshTokenField]) == "" {
		return false
	}
	if strings.TrimSpace(cred.Data[accessTokenField]) == "" {
		return true
	}

	expiresAt, ok := parseExpiry(cred.Data[expiresAtField])
	if !ok {
		return false
	}
	return time.Until(expiresAt) <= refreshWindow
}

func (a *Adapter) RefreshCredential(ctx context.Context, cred *sdk.Credential) (*sdk.Credential, error) {
	if cred == nil {
		return nil, fmt.Errorf("%s: missing credential", adapterTypeKey)
	}
	if strings.TrimSpace(cred.Data[refreshTokenField]) == "" {
		return nil, sdk.ErrNoRefreshNeeded
	}

	refreshed, err := a.getClient().RefreshCredential(ctx, cred.Data)
	if err != nil {
		return nil, err
	}

	data := cloneCredentialData(cred.Data)
	data[accessTokenField] = refreshed.AccessToken
	if refreshed.RefreshToken != "" {
		data[refreshTokenField] = refreshed.RefreshToken
	}
	if refreshed.ExpiresIn > 0 {
		data[expiresAtField] = time.Now().UTC().Add(time.Duration(refreshed.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	if strings.TrimSpace(data[regionField]) == "" {
		data[regionField] = defaultRegion
	}

	return &sdk.Credential{
		ID:   cred.ID,
		Data: data,
	}, nil
}

func (a *Adapter) GetModelInfos(
	ctx context.Context,
	cred *sdk.Credential,
	providerQualifier string,
) ([]sdk.ModelInfo, error) {
	_ = ctx
	_ = cred
	_ = providerQualifier

	return []sdk.ModelInfo{
		{Name: "claude-opus-4.7", DisplayName: "Claude Opus 4.7", ContextWindow: defaultContext, MaxTokens: defaultMaxTokens},
		{Name: "claude-opus-4.6", DisplayName: "Claude Opus 4.6", ContextWindow: defaultContext, MaxTokens: defaultMaxTokens},
		{Name: "claude-sonnet-4.6", DisplayName: "Claude Sonnet 4.6", ContextWindow: defaultContext, MaxTokens: defaultMaxTokens},
		{Name: "claude-sonnet-4.5", DisplayName: "Claude Sonnet 4.5", ContextWindow: defaultContext, MaxTokens: defaultMaxTokens},
		{Name: "claude-haiku-4.5", DisplayName: "Claude Haiku 4.5", ContextWindow: defaultContext, MaxTokens: defaultMaxTokens},
	}, nil
}

func (a *Adapter) GetAuthFlow() sdk.AuthFlowHandler {
	return &AuthFlow{client: a.getClient()}
}

func (a *Adapter) GetDefaultProviders() []sdk.ProviderInfo {
	return []sdk.ProviderInfo{
		{
			Name:      providerName,
			Qualifier: "",
			BaseURL:   providerBaseURL,
			IconURL:   "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='32' height='32' viewBox='0 0 32 32'%3E%3Crect width='32' height='32' rx='8' fill='%23121A24'/%3E%3Cpath d='M9 7h4v8.07L20.1 7H25l-8.04 8.95L25 25h-5.02l-6.42-7.51L13 18.11V25H9z' fill='%23FFFFFF'/%3E%3C/svg%3E",
		},
	}
}

func (a *Adapter) getClient() *Client {
	if a.client == nil {
		a.client = NewClient()
	}
	return a.client
}

func cloneCredentialData(data map[string]string) map[string]string {
	cloned := make(map[string]string, len(data))
	for key, value := range data {
		cloned[key] = value
	}
	return cloned
}

var _ sdk.Adapter = (*Adapter)(nil)
