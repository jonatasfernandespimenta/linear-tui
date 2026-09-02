package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/roeyazroel/linear-tui/internal/auth/oauth"
	"github.com/roeyazroel/linear-tui/internal/config"
)

// Resolve selects an API token: LINEAR_API_KEY overrides stored credentials.
// Without an API key it resolves the active workspace of the credentials store.
func Resolve(ctx context.Context, apiKey string, storePath string, oauthClient *oauth.Client) (ResolvedAuth, error) {
	if apiKey != "" {
		return ResolvedAuth{Token: apiKey, Source: TokenSourceAPIKey}, nil
	}

	store, err := LoadStore(storePath)
	if err != nil {
		if errors.Is(err, ErrCredentialsNotFound) {
			return ResolvedAuth{}, fmt.Errorf("not authenticated: run `linear-tui auth login` or set %s", config.LinearAPIKeyEnv)
		}
		return ResolvedAuth{}, err
	}

	return ResolveWorkspace(ctx, storePath, store.ActiveWorkspace, oauthClient)
}

// ResolveWorkspace returns a usable access token for one saved workspace,
// refreshing and persisting only that workspace's credentials when needed.
func ResolveWorkspace(ctx context.Context, storePath, workspaceID string, oauthClient *oauth.Client) (ResolvedAuth, error) {
	var resolved ResolvedAuth

	_, err := UpdateStore(storePath, func(store *Store) error {
		profile, ok := store.Profile(workspaceID)
		if !ok {
			return fmt.Errorf("workspace %q is not connected: run `linear-tui auth login`", workspaceID)
		}

		updated, refreshed, err := EnsureAccessToken(ctx, profile.Credentials(), oauthClient, time.Now(), oauth.RefreshSkew, false)
		if err != nil {
			return fmt.Errorf("refresh oauth credentials: %w (re-run `linear-tui auth login`)", err)
		}
		if refreshed {
			profile.Auth = authFromCredentials(updated)
			store.Put(profile)
		}

		expiresAt := profile.Auth.ExpiresAt
		resolved = ResolvedAuth{
			Token:         profile.Auth.AccessToken,
			Source:        TokenSourceOAuth,
			ExpiresAt:     &expiresAt,
			WorkspaceID:   profile.WorkspaceID,
			WorkspaceName: profile.DisplayName(),
			Legacy:        store.Legacy,
		}
		return nil
	})
	if err != nil {
		return ResolvedAuth{}, err
	}
	return resolved, nil
}

// EnsureAccessToken refreshes credentials when force is set or the access token
// expires within skew of now. Returns the (possibly updated) credentials and
// whether a refresh occurred.
func EnsureAccessToken(
	ctx context.Context,
	creds Credentials,
	oauthClient *oauth.Client,
	now time.Time,
	skew time.Duration,
	force bool,
) (Credentials, bool, error) {
	if oauthClient == nil {
		return Credentials{}, false, fmt.Errorf("oauth client is nil")
	}
	if creds.RefreshToken == "" {
		return Credentials{}, false, fmt.Errorf("credentials missing refresh_token")
	}

	needsRefresh := force || !now.Add(skew).Before(creds.ExpiresAt)
	if !needsRefresh {
		return creds, false, nil
	}

	token, err := oauthClient.Refresh(ctx, creds.RefreshToken)
	if err != nil {
		return Credentials{}, false, err
	}
	updated := CredentialsFromTokenResponse(token, now)
	return updated, true, nil
}

// CredentialsFromTokenResponse maps a token endpoint response to stored credentials.
func CredentialsFromTokenResponse(token oauth.TokenResponse, now time.Time) Credentials {
	expiresIn := token.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 86400
	}
	return Credentials{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		Scope:        token.Scope,
		ExpiresAt:    now.Add(time.Duration(expiresIn) * time.Second).UTC(),
		UpdatedAt:    now.UTC(),
	}
}

// NewRefreshFunc returns a callback that force-refreshes one workspace's stored
// OAuth credentials. Suitable for linearapi unauthorized retry wiring.
func NewRefreshFunc(storePath, workspaceID string, oauthClient *oauth.Client) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		var token string
		_, err := UpdateStore(storePath, func(store *Store) error {
			profile, ok := store.Profile(workspaceID)
			if !ok {
				return fmt.Errorf("workspace %q is not connected", workspaceID)
			}
			updated, _, err := EnsureAccessToken(ctx, profile.Credentials(), oauthClient, time.Now(), 0, true)
			if err != nil {
				return err
			}
			profile.Auth = authFromCredentials(updated)
			store.Put(profile)
			token = updated.AccessToken
			return nil
		})
		if err != nil {
			return "", err
		}
		return token, nil
	}
}
