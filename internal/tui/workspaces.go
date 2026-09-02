package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/roeyazroel/linear-tui/internal/auth"
	"github.com/roeyazroel/linear-tui/internal/auth/oauth"
	"github.com/roeyazroel/linear-tui/internal/cache"
	"github.com/roeyazroel/linear-tui/internal/linearapi"
	"github.com/roeyazroel/linear-tui/internal/logger"
)

// envOverrideMessage explains why switching is unavailable with LINEAR_API_KEY.
const envOverrideMessage = "Workspace switching is unavailable while LINEAR_API_KEY overrides saved credentials."

// WorkspaceOptions wires persisted multi-workspace authentication into the app.
type WorkspaceOptions struct {
	// StorePath is the credentials file holding the workspace profiles.
	StorePath string
	// OAuthClient refreshes and revokes workspace tokens.
	OAuthClient *oauth.Client
	// ClientID is the Linear OAuth client id used to connect new workspaces.
	ClientID string
	// APIKeyOverride reports that LINEAR_API_KEY supplied the active token,
	// which disables switching to persisted credentials for this process.
	APIKeyOverride bool
	// WorkspaceID and WorkspaceName describe the workspace resolved at startup.
	WorkspaceID   string
	WorkspaceName string
}

// ConfigureWorkspaces enables the in-app workspace switcher. Without it the app
// keeps operating on the single client it was constructed with.
func (a *App) ConfigureWorkspaces(opts WorkspaceOptions) {
	a.workspaceMu.Lock()
	a.workspaceStorePath = opts.StorePath
	a.workspaceOAuth = opts.OAuthClient
	a.workspaceClientID = opts.ClientID
	a.workspaceEnvOverride = opts.APIKeyOverride
	a.workspaceID = opts.WorkspaceID
	a.workspaceName = opts.WorkspaceName
	a.workspaceMu.Unlock()

	a.reloadWorkspaceProfiles()
	a.updateStatusBar()
}

// WorkspacesEnabled reports whether persisted workspace profiles are wired up.
func (a *App) WorkspacesEnabled() bool {
	a.workspaceMu.RLock()
	defer a.workspaceMu.RUnlock()
	return a.workspaceStorePath != ""
}

// ActiveWorkspaceID returns the stable ID of the active workspace.
func (a *App) ActiveWorkspaceID() string {
	a.workspaceMu.RLock()
	defer a.workspaceMu.RUnlock()
	return a.workspaceID
}

// ActiveWorkspaceName returns the display name of the active workspace.
func (a *App) ActiveWorkspaceName() string {
	a.workspaceMu.RLock()
	defer a.workspaceMu.RUnlock()
	return a.workspaceName
}

// WorkspaceEnvOverride reports whether LINEAR_API_KEY overrides saved credentials.
func (a *App) WorkspaceEnvOverride() bool {
	a.workspaceMu.RLock()
	defer a.workspaceMu.RUnlock()
	return a.workspaceEnvOverride
}

// WorkspaceProfiles returns the saved workspaces, ordered by name.
func (a *App) WorkspaceProfiles() []auth.WorkspaceProfile {
	a.workspaceMu.RLock()
	defer a.workspaceMu.RUnlock()
	return append([]auth.WorkspaceProfile(nil), a.workspaceProfiles...)
}

// WorkspaceGeneration identifies the current workspace session. Background work
// started before a switch must discard its results when the value changed.
func (a *App) WorkspaceGeneration() int64 {
	return a.workspaceGeneration.Load()
}

// queueWorkspaceUpdate queues a UI update that is dropped when the workspace
// changed after the background work was started.
func (a *App) queueWorkspaceUpdate(generation int64, f func()) {
	a.QueueUpdateDraw(func() {
		if generation != a.workspaceGeneration.Load() {
			logger.Debug("tui.workspace: dropping stale UI update generation=%d", generation)
			return
		}
		f()
	})
}

// reloadWorkspaceProfiles refreshes the cached list of saved workspaces.
func (a *App) reloadWorkspaceProfiles() {
	a.workspaceMu.RLock()
	storePath := a.workspaceStorePath
	a.workspaceMu.RUnlock()
	if storePath == "" {
		return
	}

	store, err := auth.LoadStore(storePath)
	if err != nil {
		if !errors.Is(err, auth.ErrCredentialsNotFound) {
			logger.Warning("tui.workspace: failed to load workspaces: %v", err)
		}
		a.workspaceMu.Lock()
		a.workspaceProfiles = nil
		a.workspaceMu.Unlock()
		return
	}

	profiles := store.List()
	a.workspaceMu.Lock()
	a.workspaceProfiles = profiles
	// Adopt the persisted identity when startup could not supply one.
	if a.workspaceID == "" && !a.workspaceEnvOverride {
		a.workspaceID = store.ActiveWorkspace
	}
	if a.workspaceName == "" {
		if active, ok := store.ActiveProfile(); ok {
			a.workspaceName = active.DisplayName()
		}
	}
	a.workspaceMu.Unlock()
}

// loadWorkspaceIdentity resolves the workspace behind the active token so the
// status bar can name it (including for LINEAR_API_KEY overrides).
func (a *App) loadWorkspaceIdentity(ctx context.Context) {
	generation := a.WorkspaceGeneration()
	workspace, err := a.api.GetWorkspace(ctx)
	if err != nil {
		logger.Warning("tui.workspace: failed to identify workspace: %v", err)
		return
	}

	a.queueWorkspaceUpdate(generation, func() {
		a.workspaceMu.Lock()
		a.workspaceName = workspace.Name
		if !a.workspaceEnvOverride {
			a.workspaceID = workspace.ID
		}
		a.workspaceMu.Unlock()
		a.updateStatusBar()
	})

	// Keep the stored profile name in sync when the workspace was renamed.
	a.workspaceMu.RLock()
	storePath, envOverride := a.workspaceStorePath, a.workspaceEnvOverride
	a.workspaceMu.RUnlock()
	if storePath == "" || envOverride {
		return
	}
	if _, err := auth.UpdateStore(storePath, func(store *auth.Store) error {
		profile, ok := store.Profile(workspace.ID)
		if !ok || (profile.WorkspaceName == workspace.Name && profile.WorkspaceSlug == workspace.Slug) {
			return errNoWorkspaceChange
		}
		profile.WorkspaceName = workspace.Name
		profile.WorkspaceSlug = workspace.Slug
		store.Put(profile)
		return nil
	}); err != nil && !errors.Is(err, errNoWorkspaceChange) {
		logger.Warning("tui.workspace: failed to update workspace name: %v", err)
	}
	a.reloadWorkspaceProfiles()
}

// errNoWorkspaceChange aborts a store update that would not change anything.
var errNoWorkspaceChange = errors.New("no workspace change")

// SwitchWorkspace activates another saved workspace without restarting the app.
func (a *App) SwitchWorkspace(workspaceID string) {
	if a.WorkspaceEnvOverride() {
		a.flashStatus(envOverrideMessage)
		return
	}
	if !a.WorkspacesEnabled() {
		a.flashStatus("Workspace switching is unavailable")
		return
	}
	if workspaceID == a.ActiveWorkspaceID() {
		a.flashStatus(fmt.Sprintf("Already on %s", a.ActiveWorkspaceName()))
		return
	}

	a.workspaceMu.RLock()
	storePath, oauthClient := a.workspaceStorePath, a.workspaceOAuth
	a.workspaceMu.RUnlock()

	target := a.workspaceDisplayName(workspaceID)
	a.flashStatus(fmt.Sprintf("Switching to %s...", target))

	go func() {
		ctx := context.Background()
		resolved, err := auth.ResolveWorkspace(ctx, storePath, workspaceID, oauthClient)
		if err != nil {
			logger.ErrorWithErr(err, "tui.workspace: failed to resolve workspace workspace_id=%s", workspaceID)
			a.QueueUpdateDraw(func() {
				a.showWorkspaceError(target, err)
			})
			return
		}

		if _, err := auth.UpdateStore(storePath, func(store *auth.Store) error {
			if !store.SetActive(workspaceID) {
				return fmt.Errorf("workspace is no longer connected")
			}
			return nil
		}); err != nil {
			logger.ErrorWithErr(err, "tui.workspace: failed to persist active workspace workspace_id=%s", workspaceID)
			a.QueueUpdateDraw(func() {
				a.showWorkspaceError(target, err)
			})
			return
		}

		a.QueueUpdateDraw(func() {
			a.applyWorkspace(resolved)
			a.flashStatus(fmt.Sprintf("Workspace: %s", a.ActiveWorkspaceName()))
		})
	}()
}

// applyWorkspace swaps the authentication context to the resolved workspace,
// clears all workspace-scoped state and caches, and reloads the default view.
func (a *App) applyWorkspace(resolved auth.ResolvedAuth) {
	generation := a.workspaceGeneration.Add(1)

	a.workspaceMu.Lock()
	a.workspaceID = resolved.WorkspaceID
	if resolved.WorkspaceName != "" {
		a.workspaceName = resolved.WorkspaceName
	}
	storePath, oauthClient := a.workspaceStorePath, a.workspaceOAuth
	a.workspaceMu.Unlock()

	clientCfg := linearapi.ClientConfig{
		Token:     resolved.Token,
		UseBearer: resolved.Source == auth.TokenSourceOAuth,
		Endpoint:  a.config.APIEndpoint,
		Timeout:   a.config.Timeout,
	}
	if resolved.Source == auth.TokenSourceOAuth && storePath != "" {
		clientCfg.OnUnauthorized = auth.NewRefreshFunc(storePath, resolved.WorkspaceID, oauthClient)
	}
	a.setAPIClient(linearapi.NewClient(clientCfg))

	// Workspace-scoped state must not survive the switch.
	a.resetCachedState()
	a.resetNavigationTree()
	a.reloadWorkspaceProfiles()
	a.updateDetailsView()
	a.updateStatusBar()

	logger.Info("tui.workspace: switched workspace workspace_id=%s generation=%d", resolved.WorkspaceID, generation)
	a.loadInitialData()
}

// setAPIClient swaps the API client and every dependency bound to it.
func (a *App) setAPIClient(client *linearapi.Client) {
	a.api = client
	// A fresh cache guarantees no team, project, user, label, status, cycle, or
	// milestone data can leak between workspaces.
	a.cache = cache.NewTeamCache(client, a.config.CacheTTL)
	a.fetchIssuesPage = client.FetchIssuesPage
	a.fetchIssueByID = client.FetchIssueByID
	a.updateIssueFunc = client.UpdateIssue
	a.createIssueRelationFunc = client.CreateIssueRelation
	a.deleteIssueRelationFunc = client.DeleteIssueRelation
	a.subscribeIssueFunc = client.SubscribeToIssue
	a.unsubscribeIssueFunc = client.UnsubscribeFromIssue
	a.fetchProjectsFunc = a.cache.GetProjects
	a.fetchWorkflowStatesFunc = a.cache.GetWorkflowStates
	a.fetchCyclesFunc = a.cache.GetCycles
}

// resetNavigationTree clears the navigation tree so the previous workspace's
// teams and projects are never shown while the new ones load.
func (a *App) resetNavigationTree() {
	if a.navigationTree == nil {
		return
	}
	a.rebuildNavigationTree(nil)
}

// ConnectWorkspace runs the OAuth flow for an additional workspace and switches
// to it once connected.
func (a *App) ConnectWorkspace() {
	if a.WorkspaceEnvOverride() {
		a.flashStatus(envOverrideMessage)
		return
	}

	a.workspaceMu.RLock()
	storePath := a.workspaceStorePath
	oauthClient := a.workspaceOAuth
	clientID := a.workspaceClientID
	a.workspaceMu.RUnlock()

	if storePath == "" || clientID == "" {
		a.flashStatus("Connecting workspaces is unavailable: OAuth is not configured")
		return
	}

	login := a.workspaceLoginFunc
	if login == nil {
		login = auth.Login
	}
	identify := a.identifyWorkspaceFunc
	if identify == nil {
		identify = a.identifyWorkspaceToken
	}

	a.flashStatus("Opening browser to connect a workspace...")

	go func() {
		profile, err := login(context.Background(), auth.LoginOptions{
			ClientID:    clientID,
			StorePath:   storePath,
			OAuthClient: oauthClient,
			Identify:    identify,
		})
		if err != nil {
			logger.ErrorWithErr(err, "tui.workspace: connect workspace failed")
			a.QueueUpdateDraw(func() {
				a.showWorkspaceError("the new workspace", err)
			})
			return
		}

		a.reloadWorkspaceProfiles()
		a.QueueUpdateDraw(func() {
			a.flashStatus(fmt.Sprintf("Connected workspace: %s", profile.DisplayName()))
		})
		a.SwitchWorkspace(profile.WorkspaceID)
	}()
}

// identifyWorkspaceToken resolves the workspace a freshly issued token belongs to.
func (a *App) identifyWorkspaceToken(token string) (auth.WorkspaceIdentity, error) {
	client := linearapi.NewClient(linearapi.ClientConfig{
		Token:     token,
		UseBearer: true,
		Endpoint:  a.config.APIEndpoint,
		Timeout:   a.config.Timeout,
	})
	workspace, err := client.GetWorkspace(context.Background())
	if err != nil {
		return auth.WorkspaceIdentity{}, err
	}
	return auth.WorkspaceIdentity{ID: workspace.ID, Name: workspace.Name, Slug: workspace.Slug}, nil
}

// DisconnectWorkspace removes a saved workspace after confirmation.
func (a *App) DisconnectWorkspace(workspaceID string) {
	if !a.WorkspacesEnabled() {
		a.flashStatus("Workspace management is unavailable")
		return
	}
	name := a.workspaceDisplayName(workspaceID)

	message := fmt.Sprintf(
		"Disconnect %q?\n\nThis removes the saved local authentication for this\nworkspace. It does not delete anything from Linear.",
		name,
	)
	a.confirmationModal.Show(" Disconnect Workspace ", message, "Disconnect", func() {
		a.disconnectWorkspaceConfirmed(workspaceID, name)
	})
}

// disconnectWorkspaceConfirmed removes the workspace credentials and, when the
// active workspace was removed, switches to another saved workspace.
func (a *App) disconnectWorkspaceConfirmed(workspaceID, name string) {
	a.workspaceMu.RLock()
	storePath, oauthClient := a.workspaceStorePath, a.workspaceOAuth
	a.workspaceMu.RUnlock()

	wasActive := workspaceID == a.ActiveWorkspaceID()

	go func() {
		store, _, err := auth.RemoveWorkspace(context.Background(), auth.LogoutOptions{
			StorePath:   storePath,
			OAuthClient: oauthClient,
		}, workspaceID)
		if err != nil {
			logger.ErrorWithErr(err, "tui.workspace: failed to remove workspace workspace_id=%s", workspaceID)
			a.QueueUpdateDraw(func() {
				a.showWorkspaceError(name, err)
			})
			return
		}

		a.reloadWorkspaceProfiles()
		a.QueueUpdateDraw(func() {
			a.flashStatus(fmt.Sprintf("Disconnected workspace: %s", name))
			a.updateStatusBar()
		})

		if !wasActive {
			return
		}
		if next, ok := store.ActiveProfile(); ok {
			a.SwitchWorkspace(next.WorkspaceID)
			return
		}
		a.QueueUpdateDraw(func() {
			a.workspaceMu.Lock()
			a.workspaceID = ""
			a.workspaceName = ""
			a.workspaceMu.Unlock()
			a.resetCachedState()
			a.resetNavigationTree()
			a.updateDetailsView()
			a.flashStatus("No workspaces connected. Use \"Connect Workspace\" to sign in.")
		})
	}()
}

// workspaceDisplayName returns the human-readable name of a saved workspace.
func (a *App) workspaceDisplayName(workspaceID string) string {
	for _, profile := range a.WorkspaceProfiles() {
		if profile.WorkspaceID == workspaceID {
			return profile.DisplayName()
		}
	}
	if workspaceID == a.ActiveWorkspaceID() {
		if name := a.ActiveWorkspaceName(); name != "" {
			return name
		}
	}
	return "workspace"
}

// showWorkspaceError reports a workspace failure using the status bar, with a
// hint on how to recover from expired or revoked authorization.
func (a *App) showWorkspaceError(name string, err error) {
	message := fmt.Sprintf("Unable to switch to %s: %v", name, err)
	if isAuthExpiredError(err) {
		message = fmt.Sprintf(
			"Unable to switch to %s: authentication has expired. Run \"Connect Workspace\" to authenticate again.",
			name,
		)
	}
	a.updateStatusBarWithError(errors.New(message))
}

// isAuthExpiredError reports whether err indicates lost authorization.
func isAuthExpiredError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{"invalid_grant", "unauthorized", "status 401", "status 403", "refresh oauth credentials", "missing refresh_token"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
