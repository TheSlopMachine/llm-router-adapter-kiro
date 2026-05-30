package kiro

type generateAssistantResponseRequest struct {
	ConversationState conversationState `json:"conversationState"`
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
	Content                 string                   `json:"content"`
	ModelID                 string                   `json:"modelId"`
	Origin                  string                   `json:"origin,omitempty"`
	UserInputMessageContext *userInputMessageContext `json:"userInputMessageContext,omitempty"`
}

type assistantResponseMessage struct {
	Content  string    `json:"content"`
	ToolUses []toolUse `json:"toolUses,omitempty"`
}

type userInputMessageContext struct {
	ToolResults []toolResult `json:"toolResults,omitempty"`
	Tools       []toolSpec   `json:"tools,omitempty"`
}

type toolSpec struct {
	ToolSpecification toolSpecification `json:"toolSpecification"`
}

type toolSpecification struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema inputSchemaBody `json:"inputSchema"`
	Strict      bool            `json:"strict,omitempty"`
}

type inputSchemaBody struct {
	JSON map[string]any `json:"json"`
}

type toolUse struct {
	ToolUseID string         `json:"toolUseId"`
	Name      string         `json:"name"`
	Input     map[string]any `json:"input"`
}

type toolResult struct {
	ToolUseID string              `json:"toolUseId"`
	Status    string              `json:"status"`
	Content   []toolResultContent `json:"content"`
}

type toolResultContent struct {
	Text string `json:"text"`
}

type inferenceConfig struct {
	MaxTokens   *int     `json:"maxTokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"topP,omitempty"`
}

type tokenRefreshResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
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
	ExpiresIn        int
	Pending          bool
	ErrorDescription string
}
