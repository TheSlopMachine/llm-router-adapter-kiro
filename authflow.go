package kiro

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"

	sdk "github.com/TheSlopMachine/llm-router-sdk"
)

const (
	actionField = "action"

	actionStartDevice = "start_device"
	actionPollDevice  = "poll_device"
	actionRestart     = "restart"

	storePrefix = "kiro_auth:"

	authMethodBuilderID = "builder-id"
	authMethodIDC       = "idc"

	fieldDeviceMethod = "device_method"
	fieldStartURL     = "start_url"
	fieldRegion       = "region"
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

func (f *AuthFlow) InitiateFlow(ctx sdk.AuthFlowContext) (sdk.AuthFlowState, error) {
	_ = clearFlowState(ctx)
	return sdk.AuthFlowState{
		RenderHTML: renderStartPage("", defaultRegion, kiroBuilderStartURL, authMethodBuilderID),
	}, nil
}

func (f *AuthFlow) HandleStep(ctx sdk.AuthFlowContext, input map[string][]string) (sdk.AuthFlowState, error) {
	action := strings.TrimSpace(firstValue(input, actionField))
	switch action {
	case actionRestart:
		_ = clearFlowState(ctx)
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage("", defaultRegion, kiroBuilderStartURL, authMethodBuilderID),
		}, nil
	case actionStartDevice:
		return f.handleStartDevice(ctx, input)
	case actionPollDevice:
		return f.handlePollDevice(ctx)
	default:
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage("Choose a device login method to continue.", defaultRegion, kiroBuilderStartURL, authMethodBuilderID),
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
			RenderHTML: renderStartPage("Unsupported device login method.", region, startURL, method),
		}, nil
	}
	if method == authMethodIDC && startURL == "" {
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage("IDC login requires a start URL.", region, startURL, method),
		}, nil
	}
	if method == authMethodBuilderID {
		startURL = kiroBuilderStartURL
	}

	registration, err := f.client.RegisterClient(context.Background(), region)
	if err != nil {
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage(err.Error(), region, startURL, method),
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
			RenderHTML: renderStartPage(err.Error(), region, startURL, method),
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
			RenderHTML: renderStartPage("Device login session expired. Start again.", defaultRegion, kiroBuilderStartURL, authMethodBuilderID),
		}, nil
	}

	if expiresAt, ok := parseExpiry(state.ExpiresAt); ok && time.Now().After(expiresAt) {
		_ = clearFlowState(ctx)
		return sdk.AuthFlowState{
			RenderHTML: renderStartPage("Device login expired. Start again.", state.Region, state.StartURL, state.Method),
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

func renderStartPage(errorMessage, region, startURL, deviceMethod string) string {
	return fmt.Sprintf(`
<div class="auth-flow-content">
	%s
	<p><strong>Kiro AI Device Login</strong></p>
	<p>Sign in with the same AWS-backed Kiro login you use in the IDE. Builder ID uses Kiro's default start URL. IAM Identity Center lets you provide your own start URL.</p>

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
