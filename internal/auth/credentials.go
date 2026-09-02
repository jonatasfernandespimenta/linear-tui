package auth

import "time"

// TokenSource identifies how an auth token was obtained.
type TokenSource string

const (
	// TokenSourceAPIKey is a personal Linear API key from LINEAR_API_KEY.
	TokenSourceAPIKey TokenSource = "api_key"
	// TokenSourceOAuth is an OAuth access token from stored credentials.
	TokenSourceOAuth TokenSource = "oauth"
)

// Credentials are OAuth tokens in the legacy (pre-v2) single-workspace file
// under ~/.linear-tui/credentials.json. See Store for the current format.
type Credentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Scope        string    `json:"scope"`
	ExpiresAt    time.Time `json:"expires_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ResolvedAuth is the token selected for API calls at startup or refresh.
type ResolvedAuth struct {
	Token     string
	Source    TokenSource
	ExpiresAt *time.Time

	// WorkspaceID is the stable Linear workspace the token belongs to. It is
	// empty for API-key auth and for legacy credentials not yet identified.
	WorkspaceID string
	// WorkspaceName is the human-readable workspace name, when known.
	WorkspaceName string
	// Legacy reports that the token came from the pre-v2 credentials file and
	// still needs migrating into the multi-workspace store.
	Legacy bool
}
