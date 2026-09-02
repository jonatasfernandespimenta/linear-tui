package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/roeyazroel/linear-tui/internal/auth/oauth"
)

// LogoutOptions configures credential revocation and local deletion.
type LogoutOptions struct {
	StorePath   string
	OAuthClient *oauth.Client
}

// Logout revokes stored tokens (best effort) and deletes the credentials file,
// disconnecting every saved workspace.
func Logout(ctx context.Context, opts LogoutOptions) error {
	if opts.StorePath == "" {
		return fmt.Errorf("credentials store path is empty")
	}

	store, err := LoadStore(opts.StorePath)
	if err != nil {
		if errors.Is(err, ErrCredentialsNotFound) {
			return nil
		}
		return err
	}

	for _, profile := range store.List() {
		revokeProfile(ctx, opts.OAuthClient, profile)
	}

	return DeleteCredentials(opts.StorePath)
}

// RemoveWorkspace revokes and deletes a single saved workspace, leaving every
// other connected workspace (and its refresh token) untouched. It returns the
// updated store and the removed profile.
func RemoveWorkspace(ctx context.Context, opts LogoutOptions, workspaceID string) (Store, WorkspaceProfile, error) {
	if opts.StorePath == "" {
		return Store{}, WorkspaceProfile{}, fmt.Errorf("credentials store path is empty")
	}

	var removed WorkspaceProfile
	store, err := UpdateStore(opts.StorePath, func(store *Store) error {
		profile, ok := store.Profile(workspaceID)
		if !ok {
			return fmt.Errorf("workspace %q is not connected", workspaceID)
		}
		removed = profile
		store.Remove(workspaceID)
		return nil
	})
	if err != nil {
		return Store{}, WorkspaceProfile{}, err
	}

	// Revoke only the removed workspace's grant.
	revokeProfile(ctx, opts.OAuthClient, removed)

	if len(store.Workspaces) == 0 {
		if err := DeleteCredentials(opts.StorePath); err != nil {
			return store, removed, err
		}
	}
	return store, removed, nil
}

// revokeProfile best-effort revokes one workspace's OAuth grant.
func revokeProfile(ctx context.Context, client *oauth.Client, profile WorkspaceProfile) {
	if client == nil || profile.Auth.AccessToken == "" {
		return
	}
	// Prefer revoking the refresh token so the grant is fully invalidated.
	token := profile.Auth.RefreshToken
	hint := "refresh_token"
	if token == "" {
		token = profile.Auth.AccessToken
		hint = "access_token"
	}
	_ = client.Revoke(ctx, token, hint)
}
