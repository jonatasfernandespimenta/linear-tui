package auth_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roeyazroel/linear-tui/internal/auth"
	"github.com/roeyazroel/linear-tui/internal/auth/oauth"
)

// legacyCredentialsFile writes a pre-v2 single-workspace credentials file.
func legacyCredentialsFile(t *testing.T, dir string, creds auth.Credentials) string {
	t.Helper()
	path := filepath.Join(dir, "credentials.json")
	if err := auth.SaveCredentials(path, creds); err != nil {
		t.Fatalf("SaveCredentials() error: %v", err)
	}
	return path
}

// profile builds a workspace profile with a long-lived access token.
func profile(id, name, slug, access, refresh string) auth.WorkspaceProfile {
	return auth.WorkspaceProfile{
		WorkspaceID:   id,
		WorkspaceName: name,
		WorkspaceSlug: slug,
		Auth: auth.WorkspaceAuth{
			Type:         auth.TokenSourceOAuth,
			AccessToken:  access,
			RefreshToken: refresh,
			ExpiresAt:    time.Now().Add(time.Hour),
			UpdatedAt:    time.Now(),
		},
	}
}

func TestLoadStoreReadsLegacyCredentials(t *testing.T) {
	t.Parallel()

	path := legacyCredentialsFile(t, t.TempDir(), auth.Credentials{
		AccessToken:  "legacy-access",
		RefreshToken: "legacy-refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	store, err := auth.LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore() error: %v", err)
	}
	if !store.Legacy {
		t.Fatal("expected legacy store")
	}
	active, ok := store.ActiveProfile()
	if !ok {
		t.Fatal("expected an active profile")
	}
	if active.Auth.AccessToken != "legacy-access" || active.Auth.RefreshToken != "legacy-refresh" {
		t.Fatalf("active profile = %+v", active)
	}
}

func TestMigrateLegacyCredentialsPreservesTokens(t *testing.T) {
	t.Parallel()

	expiry := time.Now().Add(90 * time.Minute).UTC().Truncate(time.Second)
	path := legacyCredentialsFile(t, t.TempDir(), auth.Credentials{
		AccessToken:  "legacy-access",
		RefreshToken: "legacy-refresh",
		TokenType:    "Bearer",
		Scope:        "read,write",
		ExpiresAt:    expiry,
	})

	store, err := auth.MigrateLegacyCredentials(path, auth.WorkspaceIdentity{
		ID:   "ws-pocketbooks",
		Name: "PocketBooks",
		Slug: "pocketbooks",
	})
	if err != nil {
		t.Fatalf("MigrateLegacyCredentials() error: %v", err)
	}
	if store.Legacy {
		t.Fatal("expected migrated store")
	}
	if store.ActiveWorkspace != "ws-pocketbooks" {
		t.Fatalf("active workspace = %q", store.ActiveWorkspace)
	}

	reloaded, err := auth.LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore() after migration error: %v", err)
	}
	if reloaded.Version != auth.StoreVersion {
		t.Fatalf("version = %d, want %d", reloaded.Version, auth.StoreVersion)
	}
	migrated, ok := reloaded.Profile("ws-pocketbooks")
	if !ok {
		t.Fatalf("migrated workspace missing: %+v", reloaded)
	}
	if migrated.Auth.AccessToken != "legacy-access" || migrated.Auth.RefreshToken != "legacy-refresh" {
		t.Fatalf("tokens not preserved: %+v", migrated.Auth)
	}
	if !migrated.Auth.ExpiresAt.Equal(expiry) {
		t.Fatalf("expiry = %s, want %s", migrated.Auth.ExpiresAt, expiry)
	}
	if migrated.WorkspaceName != "PocketBooks" || migrated.WorkspaceSlug != "pocketbooks" {
		t.Fatalf("identity not stored: %+v", migrated)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %o, want 0600", info.Mode().Perm())
	}

	// Migrating an already-migrated store is a no-op.
	again, err := auth.MigrateLegacyCredentials(path, auth.WorkspaceIdentity{ID: "ws-other", Name: "Other"})
	if err != nil {
		t.Fatalf("second MigrateLegacyCredentials() error: %v", err)
	}
	if len(again.Workspaces) != 1 || again.ActiveWorkspace != "ws-pocketbooks" {
		t.Fatalf("store changed on repeat migration: %+v", again)
	}
}

func TestMigratedLegacyCredentialsStillResolve(t *testing.T) {
	t.Parallel()

	path := legacyCredentialsFile(t, t.TempDir(), auth.Credentials{
		AccessToken:  "legacy-access",
		RefreshToken: "legacy-refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	if _, err := auth.MigrateLegacyCredentials(path, auth.WorkspaceIdentity{ID: "ws-1", Name: "PocketBooks"}); err != nil {
		t.Fatalf("MigrateLegacyCredentials() error: %v", err)
	}

	resolved, err := auth.Resolve(context.Background(), "", path, oauth.NewClient(oauth.ClientConfig{ClientID: "c"}))
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if resolved.Token != "legacy-access" || resolved.Source != auth.TokenSourceOAuth {
		t.Fatalf("resolved = %+v", resolved)
	}
	if resolved.WorkspaceID != "ws-1" || resolved.WorkspaceName != "PocketBooks" {
		t.Fatalf("resolved workspace = %+v", resolved)
	}
}

func TestStoreSavesAndRestoresMultipleWorkspaces(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "credentials.json")
	if _, err := auth.ConnectWorkspace(path, profile("ws-1", "PocketBooks", "pocketbooks", "a1", "r1")); err != nil {
		t.Fatalf("ConnectWorkspace(PocketBooks) error: %v", err)
	}
	if _, err := auth.ConnectWorkspace(path, profile("ws-2", "Resilion", "resilion", "a2", "r2")); err != nil {
		t.Fatalf("ConnectWorkspace(Resilion) error: %v", err)
	}

	store, err := auth.LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore() error: %v", err)
	}
	if len(store.Workspaces) != 2 {
		t.Fatalf("workspaces = %d, want 2", len(store.Workspaces))
	}
	// Connecting makes the new workspace active.
	if store.ActiveWorkspace != "ws-2" {
		t.Fatalf("active = %q, want ws-2", store.ActiveWorkspace)
	}

	// Listing is name-ordered and stable.
	names := []string{}
	for _, p := range store.List() {
		names = append(names, p.DisplayName())
	}
	if strings.Join(names, ",") != "PocketBooks,Resilion" {
		t.Fatalf("list order = %v", names)
	}

	// Active workspace selection persists across loads.
	if _, err := auth.UpdateStore(path, func(s *auth.Store) error {
		if !s.SetActive("ws-1") {
			t.Fatal("SetActive(ws-1) = false")
		}
		return nil
	}); err != nil {
		t.Fatalf("UpdateStore() error: %v", err)
	}
	reloaded, err := auth.LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ActiveWorkspace != "ws-1" {
		t.Fatalf("active after reload = %q", reloaded.ActiveWorkspace)
	}
	active, _ := reloaded.ActiveProfile()
	if active.DisplayName() != "PocketBooks" {
		t.Fatalf("active profile = %+v", active)
	}
}

func TestConnectWorkspaceTwiceUpdatesProfile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "credentials.json")
	if _, err := auth.ConnectWorkspace(path, profile("ws-1", "PocketBooks", "pocketbooks", "a1", "r1")); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.ConnectWorkspace(path, profile("ws-2", "Resilion", "resilion", "a2", "r2")); err != nil {
		t.Fatal(err)
	}
	// Reconnecting the same workspace (renamed upstream) must merge, not duplicate.
	if _, err := auth.ConnectWorkspace(path, profile("ws-1", "PocketBooks Inc", "pocketbooks", "a1-new", "r1-new")); err != nil {
		t.Fatal(err)
	}

	store, err := auth.LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Workspaces) != 2 {
		t.Fatalf("workspaces = %d, want 2", len(store.Workspaces))
	}
	updated, _ := store.Profile("ws-1")
	if updated.Auth.AccessToken != "a1-new" || updated.Auth.RefreshToken != "r1-new" {
		t.Fatalf("profile not updated: %+v", updated.Auth)
	}
	if updated.WorkspaceName != "PocketBooks Inc" {
		t.Fatalf("name not updated: %q", updated.WorkspaceName)
	}
	other, _ := store.Profile("ws-2")
	if other.Auth.RefreshToken != "r2" {
		t.Fatalf("other workspace modified: %+v", other.Auth)
	}
}

func TestRemoveWorkspacePreservesOthers(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "credentials.json")
	for _, p := range []auth.WorkspaceProfile{
		profile("ws-1", "PocketBooks", "pocketbooks", "a1", "r1"),
		profile("ws-2", "Resilion", "resilion", "a2", "r2"),
		profile("ws-3", "Personal", "personal", "a3", "r3"),
	} {
		if _, err := auth.ConnectWorkspace(path, p); err != nil {
			t.Fatal(err)
		}
	}

	var revoked []string
	revokeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		values, _ := url.ParseQuery(string(body))
		revoked = append(revoked, values.Get("token"))
	}))
	defer revokeServer.Close()

	client := oauth.NewClient(oauth.ClientConfig{
		ClientID:   "c",
		HTTPClient: revokeServer.Client(),
		RevokeURL:  revokeServer.URL,
	})

	// ws-3 is active (connected last); removing it must pick another workspace.
	store, removed, err := auth.RemoveWorkspace(context.Background(), auth.LogoutOptions{StorePath: path, OAuthClient: client}, "ws-3")
	if err != nil {
		t.Fatalf("RemoveWorkspace() error: %v", err)
	}
	if removed.DisplayName() != "Personal" {
		t.Fatalf("removed = %+v", removed)
	}
	if len(store.Workspaces) != 2 {
		t.Fatalf("remaining = %d, want 2", len(store.Workspaces))
	}
	if _, ok := store.ActiveProfile(); !ok {
		t.Fatalf("expected a new active workspace, got %q", store.ActiveWorkspace)
	}
	if len(revoked) != 1 || revoked[0] != "r3" {
		t.Fatalf("revoked tokens = %v, want only the removed workspace refresh token", revoked)
	}

	reloaded, err := auth.LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := reloaded.Profile("ws-1")
	two, _ := reloaded.Profile("ws-2")
	if one.Auth.RefreshToken != "r1" || two.Auth.RefreshToken != "r2" {
		t.Fatalf("remaining credentials altered: %+v %+v", one.Auth, two.Auth)
	}

	// Removing the last workspaces deletes the file entirely.
	if _, _, err := auth.RemoveWorkspace(context.Background(), auth.LogoutOptions{StorePath: path}, "ws-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := auth.RemoveWorkspace(context.Background(), auth.LogoutOptions{StorePath: path}, "ws-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.LoadStore(path); err == nil {
		t.Fatal("expected credentials file to be removed")
	}
}

func TestRefreshIsolatedToActiveWorkspace(t *testing.T) {
	t.Parallel()

	var gotRefresh string
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		values, _ := url.ParseQuery(string(body))
		gotRefresh = values.Get("refresh_token")
		_, _ = w.Write([]byte(`{"access_token":"new-access","token_type":"Bearer","expires_in":3600,"refresh_token":"new-refresh"}`))
	}))
	defer tokenServer.Close()

	path := filepath.Join(t.TempDir(), "credentials.json")
	stale := profile("ws-1", "PocketBooks", "pocketbooks", "a1", "r1")
	stale.Auth.ExpiresAt = time.Now().Add(time.Minute) // inside the refresh skew
	if _, err := auth.ConnectWorkspace(path, stale); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.ConnectWorkspace(path, profile("ws-2", "Resilion", "resilion", "a2", "r2")); err != nil {
		t.Fatal(err)
	}

	client := oauth.NewClient(oauth.ClientConfig{
		ClientID:   "c",
		HTTPClient: tokenServer.Client(),
		TokenURL:   tokenServer.URL,
	})

	resolved, err := auth.ResolveWorkspace(context.Background(), path, "ws-1", client)
	if err != nil {
		t.Fatalf("ResolveWorkspace() error: %v", err)
	}
	if resolved.Token != "new-access" || resolved.WorkspaceID != "ws-1" {
		t.Fatalf("resolved = %+v", resolved)
	}
	if gotRefresh != "r1" {
		t.Fatalf("refresh_token sent = %q, want r1", gotRefresh)
	}

	store, err := auth.LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	refreshed, _ := store.Profile("ws-1")
	if refreshed.Auth.RefreshToken != "new-refresh" || refreshed.Auth.AccessToken != "new-access" {
		t.Fatalf("rotated tokens not persisted: %+v", refreshed.Auth)
	}
	untouched, _ := store.Profile("ws-2")
	if untouched.Auth.RefreshToken != "r2" || untouched.Auth.AccessToken != "a2" {
		t.Fatalf("other workspace credentials changed: %+v", untouched.Auth)
	}
}

func TestNewRefreshFuncOnlyRotatesItsWorkspace(t *testing.T) {
	t.Parallel()

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"rotated","token_type":"Bearer","expires_in":3600,"refresh_token":"rotated-refresh"}`))
	}))
	defer tokenServer.Close()

	path := filepath.Join(t.TempDir(), "credentials.json")
	if _, err := auth.ConnectWorkspace(path, profile("ws-1", "PocketBooks", "pocketbooks", "a1", "r1")); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.ConnectWorkspace(path, profile("ws-2", "Resilion", "resilion", "a2", "r2")); err != nil {
		t.Fatal(err)
	}

	client := oauth.NewClient(oauth.ClientConfig{
		ClientID:   "c",
		HTTPClient: tokenServer.Client(),
		TokenURL:   tokenServer.URL,
	})

	token, err := auth.NewRefreshFunc(path, "ws-2", client)(context.Background())
	if err != nil {
		t.Fatalf("refresh func error: %v", err)
	}
	if token != "rotated" {
		t.Fatalf("token = %q", token)
	}

	store, err := auth.LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := store.Profile("ws-1"); p.Auth.AccessToken != "a1" || p.Auth.RefreshToken != "r1" {
		t.Fatalf("ws-1 credentials changed: %+v", p.Auth)
	}
	if p, _ := store.Profile("ws-2"); p.Auth.RefreshToken != "rotated-refresh" {
		t.Fatalf("ws-2 refresh token = %q", p.Auth.RefreshToken)
	}
}

func TestResolveAPIKeyDoesNotTouchStore(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "credentials.json")
	if _, err := auth.ConnectWorkspace(path, profile("ws-1", "PocketBooks", "pocketbooks", "a1", "r1")); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := auth.Resolve(context.Background(), "env-key", path, oauth.NewClient(oauth.ClientConfig{ClientID: "c"}))
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if resolved.Source != auth.TokenSourceAPIKey || resolved.Token != "env-key" {
		t.Fatalf("resolved = %+v", resolved)
	}
	if resolved.WorkspaceID != "" {
		t.Fatalf("api key auth must not claim a saved workspace: %+v", resolved)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("credentials file changed while LINEAR_API_KEY was in use")
	}
}

func TestLoadStoreDropsMalformedProfiles(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "credentials.json")
	raw := `{
	  "version": 2,
	  "active_workspace": "ws-broken",
	  "workspaces": {
	    "ws-broken": {"workspace_id": "ws-broken", "workspace_name": "Broken", "auth": {"type": "oauth"}},
	    "ws-ok": {"workspace_id": "ws-ok", "workspace_name": "Fine", "auth": {"type": "oauth", "access_token": "a"}}
	  }
	}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := auth.LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore() error: %v", err)
	}
	if len(store.Workspaces) != 1 {
		t.Fatalf("workspaces = %+v", store.Workspaces)
	}
	if store.ActiveWorkspace != "ws-ok" {
		t.Fatalf("active = %q, want fallback to ws-ok", store.ActiveWorkspace)
	}
}

func TestLoadStoreCorruptFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.LoadStore(path); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestSaveStoreWritesValidJSONAtomically(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	store := auth.NewStore()
	store.Put(profile("ws-1", "PocketBooks", "pocketbooks", "a1", "r1"))
	if err := auth.SaveStore(path, store); err != nil {
		t.Fatalf("SaveStore() error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("stored file is not valid JSON: %v", err)
	}
	if decoded["version"] != float64(auth.StoreVersion) {
		t.Fatalf("version = %v", decoded["version"])
	}

	// No temp files left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("leftover temp file %s", entry.Name())
		}
	}
}

func TestConnectSecondWorkspaceKeepsLegacyCredentials(t *testing.T) {
	t.Parallel()

	path := legacyCredentialsFile(t, t.TempDir(), auth.Credentials{
		AccessToken:  "legacy-access",
		RefreshToken: "legacy-refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	// Connecting another workspace before the legacy file was identified must
	// not discard the credentials already stored.
	if _, err := auth.ConnectWorkspace(path, profile("ws-2", "Resilion", "resilion", "a2", "r2")); err != nil {
		t.Fatalf("ConnectWorkspace() error: %v", err)
	}

	store, err := auth.LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore() error: %v", err)
	}
	if len(store.Workspaces) != 2 {
		t.Fatalf("workspaces = %+v, want the legacy credentials kept", store.Workspaces)
	}
	var kept bool
	for _, saved := range store.List() {
		if saved.Auth.AccessToken == "legacy-access" && saved.Auth.RefreshToken == "legacy-refresh" {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("legacy credentials were dropped: %+v", store.Workspaces)
	}
	if store.ActiveWorkspace != "ws-2" {
		t.Fatalf("active workspace = %q, want the newly connected one", store.ActiveWorkspace)
	}
}

func TestMigrateLegacyIfNeededIdentifiesThenConnects(t *testing.T) {
	t.Parallel()

	path := legacyCredentialsFile(t, t.TempDir(), auth.Credentials{
		AccessToken:  "legacy-access",
		RefreshToken: "legacy-refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	identify := func(token string) (auth.WorkspaceIdentity, error) {
		if token != "legacy-access" {
			t.Fatalf("identify called with %q", token)
		}
		return auth.WorkspaceIdentity{ID: "ws-1", Name: "PocketBooks", Slug: "pocketbooks"}, nil
	}
	if err := auth.MigrateLegacyIfNeeded(path, identify); err != nil {
		t.Fatalf("MigrateLegacyIfNeeded() error: %v", err)
	}
	if _, err := auth.ConnectWorkspace(path, profile("ws-2", "Resilion", "resilion", "a2", "r2")); err != nil {
		t.Fatal(err)
	}

	store, err := auth.LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Workspaces) != 2 {
		t.Fatalf("workspaces = %+v, want two named workspaces", store.Workspaces)
	}
	first, ok := store.Profile("ws-1")
	if !ok || first.DisplayName() != "PocketBooks" || first.Auth.RefreshToken != "legacy-refresh" {
		t.Fatalf("migrated workspace = %+v", first)
	}

	// A second call is a no-op once the store is in the new format.
	if err := auth.MigrateLegacyIfNeeded(path, func(string) (auth.WorkspaceIdentity, error) {
		t.Fatal("identify must not run for an already-migrated store")
		return auth.WorkspaceIdentity{}, nil
	}); err != nil {
		t.Fatalf("second MigrateLegacyIfNeeded() error: %v", err)
	}
}
