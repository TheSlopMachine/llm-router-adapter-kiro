package kiro

type generateAssistantResponseRequest struct {
	ConversationState conversationState `json:"conversationState"`
	ProfileARN        string            `json:"profileArn,omitempty"`
	InferenceConfig   *inferenceConfig  `json:"inferenceConfig,omitempty"`
}

type conversationState struct {
	ChatTriggerType string              `json:"chatTriggerType"`
	ConversationID  string              `json:"conversationId"`
	CurrentMessage  currentMessage      `json:"currentMessage"`
	History         []conversationEntry `json:"history,omitempty"`
}

type currentMessage struct {
	UserInputMessage userInputMessage `json:"userInputMessage"`
}

type conversationEntry struct {
	UserInputMessage         *userInputMessage         `json:"userInputMessage,omitempty"`
	AssistantResponseMessage *assistantResponseMessage `json:"assistantResponseMessage,omitempty"`
}

type userInputMessage struct {
	Content string `json:"content"`
	ModelID string `json:"modelId"`
	Origin  string `json:"origin,omitempty"`
}

type assistantResponseMessage struct {
	Content string `json:"content"`
}

type inferenceConfig struct {
	MaxTokens   *int     `json:"maxTokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"topP,omitempty"`
}

type tokenRefreshResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileARN   string `json:"profileArn"`
	ExpiresIn    int    `json:"expiresIn"`
}

type clientRegistrationResponse struct {
	ClientID              string `json:"clientId"`
	ClientSecret          string `json:"clientSecret"`
	ClientSecretExpiresAt int64  `json:"clientSecretExpiresAt"`
}

type deviceAuthorizationResponse struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURI         string `json:"verificationUri"`
	VerificationURIComplete string `json:"verificationUriComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

type deviceTokenResponse struct {
	AccessToken      string `json:"accessToken"`
	RefreshToken     string `json:"refreshToken"`
	ExpiresIn        int    `json:"expiresIn"`
	TokenType        string `json:"tokenType"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type devicePollResult struct {
	AccessToken      string
	RefreshToken     string
	ProfileARN       string
	ExpiresIn        int
	Pending          bool
	ErrorDescription string
}
