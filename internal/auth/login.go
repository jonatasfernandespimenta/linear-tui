package auth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/roeyazroel/linear-tui/internal/auth/oauth"
)

// LoginOptions configures the browser PKCE login flow.
type LoginOptions struct {
	ClientID     string
	StorePath    string
	Scopes       string
	Timeout      time.Duration
	RedirectURI  string
	ListenAddr   string
	AuthorizeURL string
	OpenBrowser  func(url string) error
	OAuthClient  *oauth.Client

	// Identify resolves the workspace the new token belongs to. When set, the
	// credentials are saved as a workspace profile in the multi-workspace store
	// (adding to, never replacing, previously connected workspaces). When nil,
	// the legacy single-workspace file is written instead.
	Identify IdentifyFunc
}

// Login runs the OAuth PKCE loopback flow and stores credentials on success.
// It returns the connected workspace profile (empty when no Identify function
// is configured and the legacy credentials file was written).
func Login(ctx context.Context, opts LoginOptions) (WorkspaceProfile, error) {
	if opts.ClientID == "" {
		return WorkspaceProfile{}, fmt.Errorf("oauth client id is empty; set LINEAR_CLIENT_ID or embed DefaultClientID")
	}
	if opts.StorePath == "" {
		return WorkspaceProfile{}, fmt.Errorf("credentials store path is empty")
	}
	if opts.OAuthClient == nil {
		return WorkspaceProfile{}, fmt.Errorf("oauth client is nil")
	}

	scopes := opts.Scopes
	if scopes == "" {
		scopes = oauth.DefaultScopes
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = oauth.LoginTimeout
	}
	redirectURI := opts.RedirectURI
	if redirectURI == "" {
		redirectURI = oauth.RedirectURI()
	}
	listenAddr := opts.ListenAddr
	if listenAddr == "" {
		listenAddr = oauth.ListenAddr()
	}
	authorizeURL := opts.AuthorizeURL
	if authorizeURL == "" {
		authorizeURL = oauth.AuthorizeURL
	}
	openBrowser := opts.OpenBrowser
	if openBrowser == nil {
		openBrowser = OpenURL
	}

	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return WorkspaceProfile{}, err
	}
	state, err := GenerateState()
	if err != nil {
		return WorkspaceProfile{}, err
	}

	authURL, err := buildAuthorizeURL(authorizeURL, opts.ClientID, redirectURI, scopes, state, challenge)
	if err != nil {
		return WorkspaceProfile{}, err
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return WorkspaceProfile{}, fmt.Errorf("listen on %s: %w (is another login in progress?)", listenAddr, err)
	}
	defer func() { _ = ln.Close() }()

	type callbackResult struct {
		code string
		err  error
	}
	resultCh := make(chan callbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(oauth.RedirectPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errParam := q.Get("error"); errParam != "" {
			desc := q.Get("error_description")
			http.Error(w, "Authorization failed. You can close this window.", http.StatusBadRequest)
			select {
			case resultCh <- callbackResult{err: fmt.Errorf("authorization error: %s (%s)", errParam, desc)}:
			default:
			}
			return
		}
		if q.Get("state") != state {
			http.Error(w, "Invalid state. You can close this window.", http.StatusBadRequest)
			select {
			case resultCh <- callbackResult{err: fmt.Errorf("oauth state mismatch")}:
			default:
			}
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "Missing code. You can close this window.", http.StatusBadRequest)
			select {
			case resultCh <- callbackResult{err: fmt.Errorf("oauth callback missing code")}:
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Login successful. You can close this window and return to the terminal."))
		select {
		case resultCh <- callbackResult{code: code}:
		default:
		}
	})

	server := &http.Server{Handler: mux}
	go func() {
		_ = server.Serve(ln)
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if err := openBrowser(authURL); err != nil {
		return WorkspaceProfile{}, fmt.Errorf("open browser to %s: %w", authURL, err)
	}

	loginCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var code string
	select {
	case <-loginCtx.Done():
		return WorkspaceProfile{}, fmt.Errorf("login timed out after %s: %w", timeout, loginCtx.Err())
	case res := <-resultCh:
		if res.err != nil {
			return WorkspaceProfile{}, res.err
		}
		code = res.code
	}

	token, err := opts.OAuthClient.ExchangeCode(ctx, code, redirectURI, verifier)
	if err != nil {
		return WorkspaceProfile{}, fmt.Errorf("exchange authorization code: %w", err)
	}
	if token.RefreshToken == "" {
		return WorkspaceProfile{}, fmt.Errorf("token response missing refresh_token; enable refresh tokens on the OAuth app")
	}

	creds := CredentialsFromTokenResponse(token, time.Now())
	if opts.Identify == nil {
		return WorkspaceProfile{}, SaveCredentials(opts.StorePath, creds)
	}

	identity, err := opts.Identify(creds.AccessToken)
	if err != nil {
		// Leave saved workspaces untouched when the workspace cannot be identified.
		return WorkspaceProfile{}, fmt.Errorf("identify workspace: %w", err)
	}
	profile := ProfileFromCredentials(identity, creds)
	if _, err := ConnectWorkspace(opts.StorePath, profile); err != nil {
		return WorkspaceProfile{}, err
	}
	return profile, nil
}

// buildAuthorizeURL constructs the Linear authorize URL with PKCE parameters.
func buildAuthorizeURL(base, clientID, redirectURI, scopes, state, challenge string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse authorize url: %w", err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", scopes)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	// Linear recommends prompt=consent so the authorization screen always lets
	// the user pick which workspace to grant access to (multi-workspace setups).
	q.Set("prompt", "consent")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// PrintAuthUsage writes auth subcommand help to the given builder-like writer.
func PrintAuthUsage(w interface{ Write([]byte) (int, error) }) {
	msg := strings.TrimSpace(`
Usage:
  linear-tui auth login             Connect a Linear workspace via browser OAuth
  linear-tui auth list              List connected workspaces
  linear-tui auth use <workspace>   Set the active workspace
  linear-tui auth remove <ws>       Disconnect a saved workspace
  linear-tui auth logout            Revoke and remove all stored credentials

"auth login" adds a workspace; previously connected workspaces are kept.
LINEAR_API_KEY overrides stored OAuth credentials when set.
`) + "\n"
	_, _ = w.Write([]byte(msg))
}
