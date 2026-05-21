package kiro

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"

	sdk "github.com/TheSlopMachine/llm-router-sdk"
)

type AuthFlow struct {
	client *Client
}

func (f *AuthFlow) InitiateFlow(ctx sdk.AuthFlowContext) (sdk.AuthFlowState, error) {
	_ = ctx
	return sdk.AuthFlowState{
		RenderHTML: renderAuthForm("", "", ""),
	}, nil
}

func (f *AuthFlow) HandleStep(ctx sdk.AuthFlowContext, input map[string][]string) (sdk.AuthFlowState, error) {
	_ = ctx

	refreshToken := strings.TrimSpace(firstValue(input, refreshTokenField))
	accessToken := strings.TrimSpace(firstValue(input, accessTokenField))

	if refreshToken == "" && accessToken == "" {
		return sdk.AuthFlowState{
			RenderHTML: renderAuthForm("Paste a Kiro refresh token or access token.", refreshToken, accessToken),
		}, nil
	}

	if refreshToken == "" {
		return sdk.AuthFlowState{
			Credentials: map[string]string{
				accessTokenField: accessToken,
				authMethodField:  "manual",
			},
		}, nil
	}

	validated, err := f.client.ValidateImportToken(context.Background(), refreshToken)
	if err != nil {
		return sdk.AuthFlowState{
			RenderHTML: renderAuthForm(err.Error(), refreshToken, accessToken),
		}, nil
	}

	credentials := map[string]string{
		accessTokenField:  validated.AccessToken,
		refreshTokenField: validated.RefreshToken,
		authMethodField:   "imported",
		regionField:       defaultRegion,
	}
	if validated.ExpiresIn > 0 {
		credentials[expiresAtField] = time.Now().UTC().Add(time.Duration(validated.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	if validated.ProfileARN != "" {
		credentials[profileARNField] = validated.ProfileARN
	}

	return sdk.AuthFlowState{
		Credentials: credentials,
	}, nil
}

func renderAuthForm(errorMessage, refreshToken, accessToken string) string {
	var alert string
	if errorMessage != "" {
		alert = fmt.Sprintf("<div class=\"alert alert-danger\">%s</div>", html.EscapeString(errorMessage))
	}

	return fmt.Sprintf(`
<div class="auth-flow-content">
	%s
	<p><strong>Kiro AI token import</strong></p>
	<p>Paste the refresh token from Kiro's local auth JSON. The adapter validates it and fetches an access token automatically.</p>
	<div class="form-group">
		<label for="%s">Refresh Token</label>
		<input
			type="text"
			id="%s"
			name="%s"
			class="form-control"
			placeholder="aorAAAAAG..."
			value="%s"
		/>
	</div>
	<div class="form-group">
		<label for="%s">Access Token (optional)</label>
		<input
			type="text"
			id="%s"
			name="%s"
			class="form-control"
			placeholder="Optional short-lived token"
			value="%s"
		/>
	</div>
	<button type="submit" class="btn btn-primary">Import Credential</button>
</div>`,
		alert,
		refreshTokenField,
		refreshTokenField,
		refreshTokenField,
		html.EscapeString(refreshToken),
		accessTokenField,
		accessTokenField,
		accessTokenField,
		html.EscapeString(accessToken),
	)
}

func firstValue(input map[string][]string, key string) string {
	values, ok := input[key]
	if !ok || len(values) == 0 {
		return ""
	}
	return values[0]
}
