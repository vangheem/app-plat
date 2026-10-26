package api

// Public HTTP contract for the control plane.
// Session tokens and API keys both travel as Authorization: Bearer <token>.
// API keys always start with "apk_". Anything else is a Kratos session token.

type User struct {
	ID                string `json:"id"`
	Email             string `json:"email"`
	Name              string `json:"name"`
	Role              string `json:"role"`
	LLMRequiresReview bool   `json:"llm_requires_review"`
	LLMCapUSD         float64 `json:"llm_cap_usd"`
	CreatedAt         string `json:"created_at,omitempty"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type RegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type AuthResponse struct {
	SessionToken string `json:"session_token"`
	User         User   `json:"user"`
}

type APIKey struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	CreatedAt string `json:"created_at"`
	Key       string `json:"key,omitempty"`
}

type CreateAPIKeyRequest struct {
	Name string `json:"name"`
}

type App struct {
	ID           string   `json:"id"`
	OwnerID      string   `json:"owner_id"`
	OwnerEmail   string   `json:"owner_email,omitempty"`
	Name         string   `json:"name"`
	Slug         string   `json:"slug"`
	Port         int      `json:"port"`
	Persistence  []string `json:"persistence"`
	Status       string   `json:"status"`
	Image        string   `json:"image"`
	DetectedType string   `json:"detected_type"`
	PublicURL    string   `json:"public_url"`
	BuildLogTail string   `json:"build_log_tail,omitempty"`
	Error        string   `json:"error,omitempty"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

type CreateAppRequest struct {
	Name        string   `json:"name"`
	Port        int      `json:"port"`
	Persistence []string `json:"persistence"`
	AppYAML     string   `json:"app_yaml,omitempty"`
}

type UpdateAppRequest struct {
	Name        *string  `json:"name,omitempty"`
	Port        *int     `json:"port,omitempty"`
	Persistence []string `json:"persistence,omitempty"`
}

type EnvVar struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

type PutEnvRequest struct {
	Vars []EnvVar `json:"vars"`
}

type Build struct {
	ID        string `json:"id"`
	AppID     string `json:"app_id"`
	Status    string `json:"status"`
	Image     string `json:"image"`
	Log       string `json:"log"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"created_at"`
}

type LLMModel struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Enabled         bool   `json:"enabled"`
	ContextLength   int    `json:"context_length"`
	PromptPrice     string `json:"prompt_price"`
	CompletionPrice string `json:"completion_price"`
}

type ModelToggle struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

type SetModelsRequest struct {
	Models []ModelToggle `json:"models"`
}

type LLMRequest struct {
	ID             string   `json:"id"`
	UserID         string   `json:"user_id"`
	UserEmail      string   `json:"user_email,omitempty"`
	AppID          string   `json:"app_id"`
	AppSlug        string   `json:"app_slug,omitempty"`
	Status         string   `json:"status"`
	SpendingCapUSD float64  `json:"spending_cap_usd"`
	Models         []string `json:"models"`
	Note           string   `json:"note"`
	LLMKeyPrefix   string   `json:"llm_key_prefix,omitempty"`
	CreatedAt      string   `json:"created_at"`
	DecidedAt      string   `json:"decided_at,omitempty"`
}

type CreateLLMRequest struct {
	AppID          string   `json:"app_id"`
	Models         []string `json:"models"`
	SpendingCapUSD float64  `json:"spending_cap_usd"`
}

type DecideLLMRequest struct {
	Approve bool   `json:"approve"`
	Note    string `json:"note"`
}

type UsageRow struct {
	UserID           string  `json:"user_id"`
	UserEmail        string  `json:"user_email,omitempty"`
	AppID            string  `json:"app_id"`
	AppSlug          string  `json:"app_slug,omitempty"`
	Model            string  `json:"model"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	Requests         int     `json:"requests"`
}

type OAuthClient struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Secret       string   `json:"secret,omitempty"`
	GrantTypes   []string `json:"grant_types"`
	RedirectURIs []string `json:"redirect_uris"`
	Scope        string   `json:"scope"`
}

type CreateOAuthClientRequest struct {
	Name         string   `json:"name"`
	GrantTypes   []string `json:"grant_types"`
	RedirectURIs []string `json:"redirect_uris"`
	Scope        string   `json:"scope"`
}

type CreateUserRequest struct {
	Email             string `json:"email"`
	Password          string `json:"password"`
	Name              string `json:"name"`
	Role              string `json:"role"`
	LLMRequiresReview *bool  `json:"llm_requires_review,omitempty"`
}

type UpdateUserRequest struct {
	Role              *string  `json:"role,omitempty"`
	LLMRequiresReview *bool    `json:"llm_requires_review,omitempty"`
	Name              *string  `json:"name,omitempty"`
	LLMCapUSD         *float64 `json:"llm_cap_usd,omitempty"`
}

type ComponentHealth struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type HealthReport struct {
	Components []ComponentHealth `json:"components"`
}

type ErrorBody struct {
	Error string `json:"error"`
}

type OAuthLoginInfo struct {
	Challenge   string   `json:"challenge"`
	ClientName  string   `json:"client_name"`
	Subject     string   `json:"subject,omitempty"`
	RequestedScope []string `json:"requested_scope"`
	Skip        bool     `json:"skip"`
}

type OAuthConsentInfo struct {
	Challenge      string   `json:"challenge"`
	ClientName     string   `json:"client_name"`
	RequestedScope []string `json:"requested_scope"`
	Subject        string   `json:"subject"`
}

type OAuthAcceptRequest struct {
	Challenge  string   `json:"challenge"`
	Remember   bool     `json:"remember"`
	GrantScope []string `json:"grant_scope,omitempty"`
}

type OAuthRedirect struct {
	RedirectTo string `json:"redirect_to"`
}

type LLMPolicy struct {
	RequiresReview bool    `json:"requires_review"`
	UserCapUSD     float64 `json:"user_cap_usd"`
	MonthSpendUSD  float64 `json:"month_spend_usd"`
}

type SetCapRequest struct {
	UserID         string   `json:"user_id,omitempty"`
	AppID          string   `json:"app_id,omitempty"`
	SpendingCapUSD *float64 `json:"spending_cap_usd"`
}
