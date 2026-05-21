package kiro

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	sdk "github.com/TheSlopMachine/llm-router-sdk"
)

const (
	actionField = "action"

	actionStartDevice    = "start_device"
	actionPollDevice     = "poll_device"
	actionStartSocial    = "start_social"
	actionExchangeSocial = "exchange_social"
	actionImportToken    = "import_token"
	actionAutoImport     = "auto_import"
	actionRestart        = "restart"

	storePrefix = "kiro_auth:"

	authMethodBuilderID = "builder-id"
	authMethodIDC       = "idc"
	authMethodGoogle    = "google"
	authMethodGitHub    = "github"
	authMethodImported  = "imported"

	fieldDeviceMethod   = "device_method"
	fieldStartURL       = "start_url"
	fieldRegion         = "region"
	fieldSocialProvider = "social_provider"
	fieldCallbackURL    = "callback_url"
	fieldAuthCode       = "auth_code"
	fieldSocialState    = "social_state"
	fieldRefreshToken   = "refresh_token"
)

type AuthFlow struct {
	client *Client
}

type deviceFlowState struct {
	Method                  string `json:"method"`
	Region                  string `json:"region"`
	StartURL                string `json:"start_url"`
	ClientID                string `json:"client_id"`
	ClientSecret            string `json:"client_secret"`
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresAt               string `json:"expires_at"`
	Interval                int    `json:"interval"`
}

type socialFlowState struct {
	Provider     string `json:"provider"`
	State        string `json:"state"`
	CodeVerifier string `json:"code_verifier"`
	AuthURL      string `json:"auth_url"`
}

func (f *AuthFlow) InitiateFlow(ctx sdk.AuthFlowContext) (sdk.AuthFlowState, error) {
	_ = clearFlowState(ctx)
	return sdk.AuthFlowState{
		RenderHTML: renderStartPage("", defaultRegion, kiroBuilderStartURL, authMethodBuilderID, authMethodGoogle, ""),
	}, nil
}

func (f *AuthFlow) HandleStep(ctx sdk.AuthFlowContext, input map[string][]string) (sdk.AuthFlowState, error) {
	action := strings.TrimSpace(firstValue(input, actionField))
	switch action {
	case actionRestart:
		_ = clearFlowState(ctx)
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage("", defaultRegion, kiroBuilderStartURL, authMethodBuilderID, authMethodGoogle, ""),
		}, nil
	case actionStartDevice:
		return f.handleStartDevice(ctx, input)
	case actionPollDevice:
		return f.handlePollDevice(ctx)
	case actionStartSocial:
		return f.handleStartSocial(ctx, input)
	case actionExchangeSocial:
		return f.handleExchangeSocial(ctx, input)
	case actionImportToken:
		return f.handleImportToken(ctx, input)
	case actionAutoImport:
		return f.handleAutoImport(ctx)
	default:
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage("Choose a Kiro login method to continue.", defaultRegion, kiroBuilderStartURL, authMethodBuilderID, authMethodGoogle, ""),
		}, nil
	}
}

func (f *AuthFlow) handleStartDevice(ctx sdk.AuthFlowContext, input map[string][]string) (sdk.AuthFlowState, error) {
	method := strings.TrimSpace(firstValue(input, fieldDeviceMethod))
	if method == "" {
		method = authMethodBuilderID
	}
	region := strings.TrimSpace(firstValue(input, fieldRegion))
	if region == "" {
		region = defaultRegion
	}
	startURL := strings.TrimSpace(firstValue(input, fieldStartURL))
	if startURL == "" {
		startURL = kiroBuilderStartURL
	}

	if method != authMethodBuilderID && method != authMethodIDC {
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage("Unsupported device login method.", region, startURL, method, authMethodGoogle, ""),
		}, nil
	}
	if method == authMethodIDC && startURL == "" {
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage("IDC login requires a start URL.", region, startURL, method, authMethodGoogle, ""),
		}, nil
	}
	if method == authMethodBuilderID {
		startURL = kiroBuilderStartURL
	}

	registration, err := f.client.RegisterClient(context.Background(), region)
	if err != nil {
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage(err.Error(), region, startURL, method, authMethodGoogle, ""),
		}, nil
	}

	deviceAuth, err := f.client.StartDeviceAuthorization(
		context.Background(),
		registration.ClientID,
		registration.ClientSecret,
		startURL,
		region,
	)
	if err != nil {
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage(err.Error(), region, startURL, method, authMethodGoogle, ""),
		}, nil
	}

	state := deviceFlowState{
		Method:                  method,
		Region:                  region,
		StartURL:                startURL,
		ClientID:                registration.ClientID,
		ClientSecret:            registration.ClientSecret,
		DeviceCode:              deviceAuth.DeviceCode,
		UserCode:                deviceAuth.UserCode,
		VerificationURI:         deviceAuth.VerificationURI,
		VerificationURIComplete: deviceAuth.VerificationURIComplete,
		ExpiresAt:               time.Now().UTC().Add(time.Duration(deviceAuth.ExpiresIn) * time.Second).Format(time.RFC3339),
		Interval:                deviceAuth.Interval,
	}

	if err := storeJSON(ctx, "device", state); err != nil {
		return sdk.AuthFlowState{}, err
	}

	return sdk.AuthFlowState{
		RenderHTML: renderDevicePage("", state),
	}, nil
}

func (f *AuthFlow) handlePollDevice(ctx sdk.AuthFlowContext) (sdk.AuthFlowState, error) {
	var state deviceFlowState
	if err := loadJSON(ctx, "device", &state); err != nil {
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage("Device login session expired. Start again.", defaultRegion, kiroBuilderStartURL, authMethodBuilderID, authMethodGoogle, ""),
		}, nil
	}

	if expiresAt, ok := parseExpiry(state.ExpiresAt); ok && time.Now().After(expiresAt) {
		_ = clearFlowState(ctx)
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage("Device login expired. Start again.", state.Region, state.StartURL, state.Method, authMethodGoogle, ""),
		}, nil
	}

	result, err := f.client.PollDeviceToken(
		context.Background(),
		state.ClientID,
		state.ClientSecret,
		state.DeviceCode,
		state.Region,
	)
	if err != nil {
		return sdk.AuthFlowState{
			RenderHTML: renderDevicePage(err.Error(), state),
		}, nil
	}
	if result.Pending {
		message := "Authorization is still pending. Finish the browser step, then check again."
		if result.ErrorDescription != "" {
			message = result.ErrorDescription
		}
		return sdk.AuthFlowState{
			RenderHTML: renderDevicePage(message, state),
		}, nil
	}

	_ = clearFlowState(ctx)
	return sdk.AuthFlowState{
		Credentials: buildCredentialMap(result.AccessToken, result.RefreshToken, result.ProfileARN, state.Method, state.Region, state.ClientID, state.ClientSecret, result.ExpiresIn),
	}, nil
}

func (f *AuthFlow) handleStartSocial(ctx sdk.AuthFlowContext, input map[string][]string) (sdk.AuthFlowState, error) {
	provider := strings.TrimSpace(firstValue(input, fieldSocialProvider))
	if provider == "" {
		provider = authMethodGoogle
	}
	if provider != authMethodGoogle && provider != authMethodGitHub {
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage("Choose Google or GitHub for social login.", defaultRegion, kiroBuilderStartURL, authMethodBuilderID, provider, ""),
		}, nil
	}

	codeVerifier, err := randomBase64URL(32)
	if err != nil {
		return sdk.AuthFlowState{}, err
	}
	stateValue, err := randomBase64URL(24)
	if err != nil {
		return sdk.AuthFlowState{}, err
	}
	codeChallenge := pkceChallenge(codeVerifier)
	authURL := f.client.BuildSocialLoginURL(provider, codeChallenge, stateValue)

	state := socialFlowState{
		Provider:     provider,
		State:        stateValue,
		CodeVerifier: codeVerifier,
		AuthURL:      authURL,
	}
	if err := storeJSON(ctx, "social", state); err != nil {
		return sdk.AuthFlowState{}, err
	}

	return sdk.AuthFlowState{
		RenderHTML: renderSocialPage("", state, "", ""),
	}, nil
}

func (f *AuthFlow) handleExchangeSocial(ctx sdk.AuthFlowContext, input map[string][]string) (sdk.AuthFlowState, error) {
	var state socialFlowState
	if err := loadJSON(ctx, "social", &state); err != nil {
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage("Social login session expired. Start again.", defaultRegion, kiroBuilderStartURL, authMethodBuilderID, authMethodGoogle, ""),
		}, nil
	}

	callbackURL := strings.TrimSpace(firstValue(input, fieldCallbackURL))
	code := strings.TrimSpace(firstValue(input, fieldAuthCode))
	returnedState := strings.TrimSpace(firstValue(input, fieldSocialState))

	if callbackURL != "" {
		parsedCode, parsedState, err := parseSocialCallback(callbackURL)
		if err != nil {
			return sdk.AuthFlowState{
				RenderHTML: renderSocialPage(err.Error(), state, callbackURL, code),
			}, nil
		}
		code = parsedCode
		returnedState = parsedState
	}

	if code == "" {
		return sdk.AuthFlowState{
			RenderHTML: renderSocialPage("Paste the callback URL or authorization code.", state, callbackURL, code),
		}, nil
	}
	if returnedState != "" && returnedState != state.State {
		return sdk.AuthFlowState{
			RenderHTML: renderSocialPage("Returned state does not match the login session.", state, callbackURL, code),
		}, nil
	}

	result, err := f.client.ExchangeSocialCode(context.Background(), code, state.CodeVerifier)
	if err != nil {
		return sdk.AuthFlowState{
			RenderHTML: renderSocialPage(err.Error(), state, callbackURL, code),
		}, nil
	}

	_ = clearFlowState(ctx)
	return sdk.AuthFlowState{
		Credentials: buildCredentialMap(result.AccessToken, result.RefreshToken, result.ProfileARN, state.Provider, defaultRegion, "", "", result.ExpiresIn),
	}, nil
}

func (f *AuthFlow) handleImportToken(ctx sdk.AuthFlowContext, input map[string][]string) (sdk.AuthFlowState, error) {
	refreshToken := strings.TrimSpace(firstValue(input, fieldRefreshToken))
	if refreshToken == "" {
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage("Paste a Kiro refresh token to import it.", defaultRegion, kiroBuilderStartURL, authMethodBuilderID, authMethodGoogle, ""),
		}, nil
	}

	result, err := f.client.ValidateImportToken(context.Background(), refreshToken)
	if err != nil {
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage(err.Error(), defaultRegion, kiroBuilderStartURL, authMethodBuilderID, authMethodGoogle, refreshToken),
		}, nil
	}

	_ = clearFlowState(ctx)
	return sdk.AuthFlowState{
		Credentials: buildCredentialMap(result.AccessToken, result.RefreshToken, result.ProfileARN, authMethodImported, defaultRegion, "", "", result.ExpiresIn),
	}, nil
}

func (f *AuthFlow) handleAutoImport(ctx sdk.AuthFlowContext) (sdk.AuthFlowState, error) {
	refreshToken, source, err := findLocalRefreshToken()
	if err != nil {
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage(err.Error(), defaultRegion, kiroBuilderStartURL, authMethodBuilderID, authMethodGoogle, ""),
		}, nil
	}

	result, err := f.client.ValidateImportToken(context.Background(), refreshToken)
	if err != nil {
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage(fmt.Sprintf("%s (found in %s)", err.Error(), source), defaultRegion, kiroBuilderStartURL, authMethodBuilderID, authMethodGoogle, refreshToken),
		}, nil
	}

	_ = clearFlowState(ctx)
	return sdk.AuthFlowState{
		Credentials: buildCredentialMap(result.AccessToken, result.RefreshToken, result.ProfileARN, authMethodImported, defaultRegion, "", "", result.ExpiresIn),
	}, nil
}

func renderStartPage(errorMessage, region, startURL, deviceMethod, socialProvider, refreshToken string) string {
	return fmt.Sprintf(`
<div class="auth-flow-content">
	%s
	<p><strong>Kiro AI Authentication</strong></p>
	<p>Choose the same kind of login you use in Kiro. Builder ID and IAM Identity Center use device login. Google and GitHub use a browser callback flow. Import is available as a fallback.</p>

	<hr />
	<p><strong>Device Login</strong></p>
	<div class="form-group">
		<label for="%s">Method</label>
		<select id="%s" name="%s" class="form-control">
			<option value="%s"%s>AWS Builder ID</option>
			<option value="%s"%s>AWS IAM Identity Center</option>
		</select>
	</div>
	<div class="form-group">
		<label for="%s">Region</label>
		<input type="text" id="%s" name="%s" class="form-control" value="%s" />
	</div>
	<div class="form-group">
		<label for="%s">Start URL</label>
		<input type="text" id="%s" name="%s" class="form-control" value="%s" />
	</div>
	<button type="submit" class="btn btn-primary" name="%s" value="%s">Start Device Login</button>

	<hr />
	<p><strong>Social Login</strong></p>
	<div class="form-group">
		<label for="%s">Provider</label>
		<select id="%s" name="%s" class="form-control">
			<option value="%s"%s>Google</option>
			<option value="%s"%s>GitHub</option>
		</select>
	</div>
	<button type="submit" class="btn btn-primary" name="%s" value="%s">Prepare Social Login</button>

	<hr />
	<p><strong>Import Existing Login</strong></p>
	<div class="form-group">
		<label for="%s">Refresh Token</label>
		<input type="text" id="%s" name="%s" class="form-control" placeholder="aorAAAAAG..." value="%s" />
	</div>
	<button type="submit" class="btn btn-primary" name="%s" value="%s">Import Refresh Token</button>
	<button type="submit" class="btn btn-secondary" name="%s" value="%s">Auto-detect Local Token</button>
</div>`,
		renderAlert(errorMessage),
		fieldDeviceMethod,
		fieldDeviceMethod,
		fieldDeviceMethod,
		authMethodBuilderID,
		selectedIf(deviceMethod == authMethodBuilderID),
		authMethodIDC,
		selectedIf(deviceMethod == authMethodIDC),
		fieldRegion,
		fieldRegion,
		fieldRegion,
		html.EscapeString(region),
		fieldStartURL,
		fieldStartURL,
		fieldStartURL,
		html.EscapeString(startURL),
		actionField,
		actionStartDevice,
		fieldSocialProvider,
		fieldSocialProvider,
		fieldSocialProvider,
		authMethodGoogle,
		selectedIf(socialProvider == authMethodGoogle),
		authMethodGitHub,
		selectedIf(socialProvider == authMethodGitHub),
		actionField,
		actionStartSocial,
		fieldRefreshToken,
		fieldRefreshToken,
		fieldRefreshToken,
		html.EscapeString(refreshToken),
		actionField,
		actionImportToken,
		actionField,
		actionAutoImport,
	)
}

func renderDevicePage(message string, state deviceFlowState) string {
	instructionsURL := state.VerificationURIComplete
	if instructionsURL == "" {
		instructionsURL = state.VerificationURI
	}
	return fmt.Sprintf(`
<div class="auth-flow-content">
	%s
	<p><strong>Complete Device Login</strong></p>
	<p>1. Open <a href="%s" target="_blank" rel="noopener noreferrer">%s</a></p>
	<p>2. If prompted, enter code <code>%s</code></p>
	<p>3. After the browser shows success, come back here and click <strong>Check Authorization</strong>.</p>
	<button type="submit" class="btn btn-primary" name="%s" value="%s">Check Authorization</button>
	<button type="submit" class="btn btn-secondary" name="%s" value="%s">Start Over</button>
</div>`,
		renderAlert(message),
		html.EscapeString(instructionsURL),
		html.EscapeString(instructionsURL),
		html.EscapeString(state.UserCode),
		actionField,
		actionPollDevice,
		actionField,
		actionRestart,
	)
}

func renderSocialPage(message string, state socialFlowState, callbackURL, code string) string {
	return fmt.Sprintf(`
<div class="auth-flow-content">
	%s
	<p><strong>Complete %s Login</strong></p>
	<p>1. Open <a href="%s" target="_blank" rel="noopener noreferrer">the Kiro login page</a>.</p>
	<p>2. Finish the browser login. Kiro will try to open a <code>kiro://...</code> callback URL.</p>
	<p>3. Paste that full callback URL below. If needed, you can paste the authorization code and state manually instead.</p>
	<div class="form-group">
		<label for="%s">Callback URL</label>
		<input type="text" id="%s" name="%s" class="form-control" placeholder="kiro://kiro.kiroAgent/authenticate-success?code=...&state=..." value="%s" />
	</div>
	<div class="form-group">
		<label for="%s">Authorization Code</label>
		<input type="text" id="%s" name="%s" class="form-control" value="%s" />
	</div>
	<div class="form-group">
		<label for="%s">State</label>
		<input type="text" id="%s" name="%s" class="form-control" value="%s" />
	</div>
	<button type="submit" class="btn btn-primary" name="%s" value="%s">Exchange Authorization Code</button>
	<button type="submit" class="btn btn-secondary" name="%s" value="%s">Start Over</button>
</div>`,
		renderAlert(message),
		html.EscapeString(displayProviderName(state.Provider)),
		html.EscapeString(state.AuthURL),
		fieldCallbackURL,
		fieldCallbackURL,
		fieldCallbackURL,
		html.EscapeString(callbackURL),
		fieldAuthCode,
		fieldAuthCode,
		fieldAuthCode,
		html.EscapeString(code),
		fieldSocialState,
		fieldSocialState,
		fieldSocialState,
		html.EscapeString(state.State),
		actionField,
		actionExchangeSocial,
		actionField,
		actionRestart,
	)
}

func renderAlert(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	return fmt.Sprintf(`<div class="alert alert-info">%s</div>`, html.EscapeString(message))
}

func selectedIf(selected bool) string {
	if selected {
		return " selected"
	}
	return ""
}

func buildCredentialMap(accessToken, refreshToken, profileARN, authMethod, region, clientID, clientSecret string, expiresIn int) map[string]string {
	credentials := map[string]string{
		accessTokenField: accessToken,
		authMethodField:  authMethod,
	}
	if refreshToken != "" {
		credentials[refreshTokenField] = refreshToken
	}
	if profileARN != "" {
		credentials[profileARNField] = profileARN
	}
	if region != "" {
		credentials[regionField] = region
	}
	if clientID != "" {
		credentials[clientIDField] = clientID
	}
	if clientSecret != "" {
		credentials[clientSecretField] = clientSecret
	}
	if expiresIn > 0 {
		credentials[expiresAtField] = time.Now().UTC().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339)
	}
	return credentials
}

func parseSocialCallback(raw string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", fmt.Errorf("invalid callback URL")
	}
	code := strings.TrimSpace(parsed.Query().Get("code"))
	state := strings.TrimSpace(parsed.Query().Get("state"))
	if code == "" {
		return "", "", fmt.Errorf("callback URL is missing the code parameter")
	}
	return code, state, nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomBase64URL(size int) (string, error) {
	random := make([]byte, size)
	_, err := rand.Read(random)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func storeJSON(ctx sdk.AuthFlowContext, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return ctx.Store.Set(flowKey(ctx.FlowID, key), string(raw))
}

func loadJSON(ctx sdk.AuthFlowContext, key string, dest any) error {
	raw, err := ctx.Store.Get(flowKey(ctx.FlowID, key))
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), dest)
}

func clearFlowState(ctx sdk.AuthFlowContext) error {
	_ = ctx.Store.Delete(flowKey(ctx.FlowID, "device"))
	_ = ctx.Store.Delete(flowKey(ctx.FlowID, "social"))
	return nil
}

func flowKey(flowID, key string) string {
	return storePrefix + flowID + ":" + key
}

func firstValue(input map[string][]string, key string) string {
	values, ok := input[key]
	if !ok || len(values) == 0 {
		return ""
	}
	return values[0]
}

func displayProviderName(provider string) string {
	switch provider {
	case authMethodGitHub:
		return "GitHub"
	case authMethodGoogle:
		return "Google"
	default:
		return provider
	}
}
