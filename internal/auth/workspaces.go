package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// StoreVersion is the schema version of the multi-workspace credentials file.
const StoreVersion = 2

// legacyWorkspaceKey is the map key used for credentials loaded from the
// pre-v2 single-workspace file. It is never written to disk: a legacy store is
// saved back in the legacy format until the workspace has been identified.
const legacyWorkspaceKey = ""

// storeMu serializes read-modify-write cycles on the credentials file within
// this process, so a token refresh never clobbers another workspace's profile.
// ponytail: one process-wide lock; credential writes are rare enough.
var storeMu sync.Mutex

// WorkspaceAuth holds the authentication context of a single workspace.
type WorkspaceAuth struct {
	Type         TokenSource `json:"type"`
	AccessToken  string      `json:"access_token"`
	RefreshToken string      `json:"refresh_token,omitempty"`
	TokenType    string      `json:"token_type,omitempty"`
	Scope        string      `json:"scope,omitempty"`
	ExpiresAt    time.Time   `json:"expires_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

// WorkspaceProfile is a saved Linear workspace and its authentication context.
type WorkspaceProfile struct {
	WorkspaceID   string        `json:"workspace_id"`
	WorkspaceName string        `json:"workspace_name"`
	WorkspaceSlug string        `json:"workspace_slug"`
	Auth          WorkspaceAuth `json:"auth"`
}

// DisplayName returns the human-readable workspace name for UI and CLI output.
func (p WorkspaceProfile) DisplayName() string {
	switch {
	case strings.TrimSpace(p.WorkspaceName) != "":
		return p.WorkspaceName
	case strings.TrimSpace(p.WorkspaceSlug) != "":
		return p.WorkspaceSlug
	case strings.TrimSpace(p.WorkspaceID) != "":
		return p.WorkspaceID
	default:
		return "Current workspace"
	}
}

// Credentials returns the profile's tokens in the legacy credentials shape.
func (p WorkspaceProfile) Credentials() Credentials {
	return Credentials{
		AccessToken:  p.Auth.AccessToken,
		RefreshToken: p.Auth.RefreshToken,
		TokenType:    p.Auth.TokenType,
		Scope:        p.Auth.Scope,
		ExpiresAt:    p.Auth.ExpiresAt,
		UpdatedAt:    p.Auth.UpdatedAt,
	}
}

// WorkspaceIdentity identifies a Linear workspace (organization in the API).
type WorkspaceIdentity struct {
	ID   string
	Name string
	Slug string
}

// IdentifyFunc resolves the workspace an access token belongs to.
type IdentifyFunc func(token string) (WorkspaceIdentity, error)

// identifyAttempts is how many times workspace identification is tried before
// giving up. A completed OAuth authorization is expensive to redo, so a slow or
// flaky API response must not waste it.
const identifyAttempts = 3

// identifyWithRetry resolves a token's workspace, retrying transient failures.
func identifyWithRetry(identify IdentifyFunc, token string) (WorkspaceIdentity, error) {
	var err error
	for attempt := range identifyAttempts {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		var identity WorkspaceIdentity
		identity, err = identify(token)
		if err == nil {
			return identity, nil
		}
	}
	return WorkspaceIdentity{}, err
}

// Store is the persisted set of workspace profiles under credentials.json.
type Store struct {
	Version         int                         `json:"version"`
	ActiveWorkspace string                      `json:"active_workspace"`
	Workspaces      map[string]WorkspaceProfile `json:"workspaces"`

	// Legacy reports that the store was read from the pre-v2 single-workspace
	// file and has not been identified/migrated yet. Not persisted.
	Legacy bool `json:"-"`
}

// NewStore returns an empty multi-workspace store.
func NewStore() Store {
	return Store{Version: StoreVersion, Workspaces: map[string]WorkspaceProfile{}}
}

// storeFromCredentials wraps legacy credentials as an unidentified store.
func storeFromCredentials(creds Credentials) Store {
	return Store{
		Version:         StoreVersion,
		ActiveWorkspace: legacyWorkspaceKey,
		Workspaces: map[string]WorkspaceProfile{
			legacyWorkspaceKey: {Auth: authFromCredentials(creds)},
		},
		Legacy: true,
	}
}

// authFromCredentials maps legacy credentials into a workspace auth context.
func authFromCredentials(creds Credentials) WorkspaceAuth {
	return WorkspaceAuth{
		Type:         TokenSourceOAuth,
		AccessToken:  creds.AccessToken,
		RefreshToken: creds.RefreshToken,
		TokenType:    creds.TokenType,
		Scope:        creds.Scope,
		ExpiresAt:    creds.ExpiresAt,
		UpdatedAt:    creds.UpdatedAt,
	}
}

// ProfileFromCredentials builds a workspace profile for an identified token.
func ProfileFromCredentials(identity WorkspaceIdentity, creds Credentials) WorkspaceProfile {
	return WorkspaceProfile{
		WorkspaceID:   identity.ID,
		WorkspaceName: identity.Name,
		WorkspaceSlug: identity.Slug,
		Auth:          authFromCredentials(creds),
	}
}

// List returns the saved profiles ordered by display name.
func (s Store) List() []WorkspaceProfile {
	profiles := make([]WorkspaceProfile, 0, len(s.Workspaces))
	for _, profile := range s.Workspaces {
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i, j int) bool {
		left, right := profiles[i].DisplayName(), profiles[j].DisplayName()
		if strings.EqualFold(left, right) {
			return profiles[i].WorkspaceID < profiles[j].WorkspaceID
		}
		return strings.ToLower(left) < strings.ToLower(right)
	})
	return profiles
}

// Profile returns the profile stored for a workspace ID.
func (s Store) Profile(workspaceID string) (WorkspaceProfile, bool) {
	profile, ok := s.Workspaces[workspaceID]
	return profile, ok
}

// ActiveProfile returns the currently active workspace profile.
func (s Store) ActiveProfile() (WorkspaceProfile, bool) {
	return s.Profile(s.ActiveWorkspace)
}

// Find returns the profile whose ID, slug, or name matches the query
// (case-insensitive), for CLI arguments where names are more convenient.
func (s Store) Find(query string) (WorkspaceProfile, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return WorkspaceProfile{}, false
	}
	if profile, ok := s.Profile(query); ok {
		return profile, true
	}
	for _, profile := range s.List() {
		if strings.EqualFold(profile.WorkspaceSlug, query) || strings.EqualFold(profile.WorkspaceName, query) {
			return profile, true
		}
	}
	return WorkspaceProfile{}, false
}

// Put inserts or updates a profile, keyed by its stable workspace ID.
// Connecting an already-saved workspace updates it instead of duplicating it.
func (s *Store) Put(profile WorkspaceProfile) {
	if s.Workspaces == nil {
		s.Workspaces = map[string]WorkspaceProfile{}
	}
	s.Version = StoreVersion
	if profile.WorkspaceID != legacyWorkspaceKey {
		// An unidentified legacy profile is superseded only when this is the
		// same credential being identified. Otherwise it is kept, so connecting
		// another workspace never discards the one already authenticated.
		if legacy, ok := s.Workspaces[legacyWorkspaceKey]; ok && legacy.Auth.AccessToken == profile.Auth.AccessToken {
			delete(s.Workspaces, legacyWorkspaceKey)
		}
		s.Legacy = false
		if s.ActiveWorkspace == legacyWorkspaceKey {
			s.ActiveWorkspace = profile.WorkspaceID
		}
	}
	s.Workspaces[profile.WorkspaceID] = profile
	if s.ActiveWorkspace == "" || len(s.Workspaces) == 1 {
		s.ActiveWorkspace = profile.WorkspaceID
	}
}

// SetActive marks a saved workspace as active. It reports whether it exists.
func (s *Store) SetActive(workspaceID string) bool {
	if _, ok := s.Workspaces[workspaceID]; !ok {
		return false
	}
	s.ActiveWorkspace = workspaceID
	return true
}

// Remove deletes a saved workspace, selecting a new active workspace when the
// removed one was active. It reports whether the workspace existed.
func (s *Store) Remove(workspaceID string) bool {
	if _, ok := s.Workspaces[workspaceID]; !ok {
		return false
	}
	delete(s.Workspaces, workspaceID)
	s.Legacy = false
	if s.ActiveWorkspace != workspaceID {
		return true
	}
	s.ActiveWorkspace = ""
	if remaining := s.List(); len(remaining) > 0 {
		s.ActiveWorkspace = remaining[0].WorkspaceID
	}
	return true
}

// LoadStore reads the credentials file, accepting both the multi-workspace
// format and the legacy single-workspace format.
func LoadStore(path string) (Store, error) {
	if path == "" {
		return Store{}, fmt.Errorf("credentials path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Store{}, ErrCredentialsNotFound
		}
		return Store{}, fmt.Errorf("read credentials: %w", err)
	}
	return parseStore(data)
}

// parseStore decodes credentials file bytes in either supported format.
func parseStore(data []byte) (Store, error) {
	var probe struct {
		Version     int             `json:"version"`
		Workspaces  json.RawMessage `json:"workspaces"`
		AccessToken string          `json:"access_token"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Store{}, fmt.Errorf("parse credentials: %w", err)
	}

	if len(probe.Workspaces) == 0 {
		// Legacy single-workspace file.
		var creds Credentials
		if err := json.Unmarshal(data, &creds); err != nil {
			return Store{}, fmt.Errorf("parse credentials: %w", err)
		}
		if creds.AccessToken == "" {
			return Store{}, fmt.Errorf("credentials missing access_token")
		}
		return storeFromCredentials(creds), nil
	}

	var store Store
	if err := json.Unmarshal(data, &store); err != nil {
		return Store{}, fmt.Errorf("parse credentials: %w", err)
	}
	if store.Workspaces == nil {
		store.Workspaces = map[string]WorkspaceProfile{}
	}
	// Drop profiles that cannot authenticate rather than failing the whole file.
	for key, profile := range store.Workspaces {
		if profile.Auth.AccessToken == "" {
			delete(store.Workspaces, key)
			continue
		}
		if profile.WorkspaceID == "" {
			profile.WorkspaceID = key
			store.Workspaces[key] = profile
		}
	}
	if len(store.Workspaces) == 0 {
		return Store{}, fmt.Errorf("credentials contain no usable workspaces")
	}
	if _, ok := store.Workspaces[store.ActiveWorkspace]; !ok {
		store.ActiveWorkspace = store.List()[0].WorkspaceID
	}
	store.Version = StoreVersion
	return store, nil
}

// SaveStore writes the store atomically with mode 0600. A store still in the
// legacy (unidentified) shape is written back in the legacy format so older
// binaries keep working until the workspace has been identified.
func SaveStore(path string, store Store) error {
	if path == "" {
		return fmt.Errorf("credentials path is empty")
	}
	if store.Legacy {
		profile, ok := store.ActiveProfile()
		if !ok {
			return fmt.Errorf("legacy store has no credentials")
		}
		return SaveCredentials(path, profile.Credentials())
	}

	store.Version = StoreVersion
	if store.Workspaces == nil {
		store.Workspaces = map[string]WorkspaceProfile{}
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	return writeFileAtomic(path, append(data, '\n'))
}

// UpdateStore applies mutate to the on-disk store and writes the result back
// atomically. The store is always re-read from disk first, so concurrent
// updates never overwrite another workspace's tokens.
func UpdateStore(path string, mutate func(store *Store) error) (Store, error) {
	storeMu.Lock()
	defer storeMu.Unlock()

	store, err := LoadStore(path)
	if err != nil {
		if !errors.Is(err, ErrCredentialsNotFound) {
			return Store{}, err
		}
		store = NewStore()
	}
	if err := mutate(&store); err != nil {
		return Store{}, err
	}
	if err := SaveStore(path, store); err != nil {
		return Store{}, err
	}
	return store, nil
}

// ConnectWorkspace saves (or updates) a workspace profile and makes it active.
func ConnectWorkspace(path string, profile WorkspaceProfile) (Store, error) {
	if profile.WorkspaceID == "" {
		return Store{}, fmt.Errorf("workspace id is empty")
	}
	return UpdateStore(path, func(store *Store) error {
		store.Put(profile)
		store.ActiveWorkspace = profile.WorkspaceID
		return nil
	})
}

// MigrateLegacyIfNeeded identifies and migrates a legacy credentials file so a
// second workspace can be connected alongside it. Stores already in the
// multi-workspace format are left untouched.
func MigrateLegacyIfNeeded(path string, identify IdentifyFunc) error {
	store, err := LoadStore(path)
	if err != nil {
		if errors.Is(err, ErrCredentialsNotFound) {
			return nil
		}
		return err
	}
	if !store.Legacy || identify == nil {
		return nil
	}
	profile, ok := store.ActiveProfile()
	if !ok {
		return fmt.Errorf("legacy store has no credentials")
	}
	identity, err := identifyWithRetry(identify, profile.Auth.AccessToken)
	if err != nil {
		return fmt.Errorf("identify existing workspace: %w", err)
	}
	_, err = MigrateLegacyCredentials(path, identity)
	return err
}

// MigrateLegacyCredentials rewrites a legacy credentials file into the
// multi-workspace format, preserving tokens and expiry, and marking the
// migrated workspace active. Files already in the new format are left as-is.
func MigrateLegacyCredentials(path string, identity WorkspaceIdentity) (Store, error) {
	if identity.ID == "" {
		return Store{}, fmt.Errorf("workspace identity is empty")
	}
	return UpdateStore(path, func(store *Store) error {
		if !store.Legacy {
			return nil
		}
		profile, ok := store.ActiveProfile()
		if !ok {
			return fmt.Errorf("legacy store has no credentials")
		}
		delete(store.Workspaces, legacyWorkspaceKey)
		profile.WorkspaceID = identity.ID
		profile.WorkspaceName = identity.Name
		profile.WorkspaceSlug = identity.Slug
		store.Put(profile)
		store.ActiveWorkspace = identity.ID
		return nil
	})
}
