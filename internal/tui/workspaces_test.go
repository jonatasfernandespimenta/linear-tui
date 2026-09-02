package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/roeyazroel/linear-tui/internal/auth"
	"github.com/roeyazroel/linear-tui/internal/auth/oauth"
	"github.com/roeyazroel/linear-tui/internal/config"
	"github.com/roeyazroel/linear-tui/internal/linearapi"
)

// workspaceFixture is a fake Linear workspace served by token.
type workspaceFixture struct {
	id       string
	name     string
	slug     string
	token    string
	teamID   string
	teamKey  string
	teamName string
	issueID  string
}

var (
	workspaceA = workspaceFixture{
		id: "ws-a", name: "PocketBooks", slug: "pocketbooks", token: "token-a",
		teamID: "team-a", teamKey: "PB", teamName: "Books", issueID: "PB-1",
	}
	workspaceB = workspaceFixture{
		id: "ws-b", name: "Resilion", slug: "resilion", token: "token-b",
		teamID: "team-b", teamKey: "RES", teamName: "Core", issueID: "RES-9",
	}
)

// newWorkspaceGraphQLServer serves workspace-scoped data chosen by the bearer
// token on the request, so responses prove which client issued the call.
func newWorkspaceGraphQLServer(t *testing.T, onRequest func(token, query string)) *httptest.Server {
	t.Helper()

	fixtures := map[string]workspaceFixture{
		"Bearer " + workspaceA.token: workspaceA,
		"Bearer " + workspaceB.token: workspaceB,
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode GraphQL request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		authHeader := r.Header.Get("Authorization")
		if onRequest != nil {
			onRequest(authHeader, request.Query)
		}
		fixture, ok := fixtures[authHeader]
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var data any
		query := strings.ToLower(request.Query)
		switch {
		case strings.Contains(query, "organization"):
			data = map[string]any{"organization": map[string]any{
				"id": fixture.id, "name": fixture.name, "urlKey": fixture.slug,
			}}
		case strings.Contains(query, "viewer"):
			data = map[string]any{"viewer": map[string]any{
				"id": "user-" + fixture.id, "name": "User", "displayName": "User", "email": "user@example.com",
			}}
		case strings.Contains(query, "teams"):
			data = map[string]any{"teams": map[string]any{"nodes": []any{
				map[string]any{"id": fixture.teamID, "key": fixture.teamKey, "name": fixture.teamName},
			}}}
		case strings.Contains(query, "projects"):
			data = map[string]any{"team": map[string]any{"projects": map[string]any{"nodes": []any{}}}}
		case strings.Contains(query, "states"):
			data = map[string]any{"team": map[string]any{"states": map[string]any{"nodes": []any{}}}}
		case strings.Contains(query, "cycles"):
			data = map[string]any{"team": map[string]any{"cycles": map[string]any{
				"nodes":    []any{},
				"pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""},
			}}}
		case strings.Contains(query, "issue(id:"):
			data = map[string]any{"issue": workspaceIssueNode(fixture)}
		case strings.Contains(query, "issueupdate"):
			// Only the fields issueMutationNode selects, so decoding succeeds.
			data = map[string]any{"issueUpdate": map[string]any{
				"success": true,
				"issue": map[string]any{
					"id":         fixture.issueID,
					"identifier": fixture.issueID,
					"title":      fixture.name + " issue",
					"state":      map[string]any{"id": "state-1", "name": "Todo"},
					"assignee":   nil,
					"priority":   1,
					"updatedAt":  "2025-01-01T00:00:00Z",
					"createdAt":  "2025-01-01T00:00:00Z",
					"team":       map[string]any{"id": fixture.teamID},
					"labels":     map[string]any{"nodes": []any{}},
					"url":        "https://linear.app/issue/" + fixture.issueID,
				},
			}}
		case strings.Contains(query, "issues"):
			data = map[string]any{"issues": map[string]any{
				"nodes":    []any{workspaceIssueNode(fixture)},
				"pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""},
			}}
		default:
			data = map[string]any{}
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
			t.Errorf("encode GraphQL response: %v", err)
		}
	}))
}

// workspaceIssueNode returns a minimal issue node for a workspace fixture.
func workspaceIssueNode(fixture workspaceFixture) map[string]any {
	return map[string]any{
		"id":         fixture.issueID,
		"identifier": fixture.issueID,
		"title":      fixture.name + " issue",
		"state":      map[string]any{"id": "state-1", "name": "Todo"},
		"assignee":   nil,
		"priority":   1,
		"updatedAt":  "2025-01-01T00:00:00Z",
		"createdAt":  "2025-01-01T00:00:00Z",
		"team":       map[string]any{"id": fixture.teamID},
		"labels":     map[string]any{"nodes": []any{}},
		"url":        "https://linear.app/issue/" + fixture.issueID,
		"children":   map[string]any{"nodes": []any{}},
	}
}

// workspaceProfileFor builds a saved profile with a long-lived access token.
func workspaceProfileFor(fixture workspaceFixture) auth.WorkspaceProfile {
	return auth.WorkspaceProfile{
		WorkspaceID:   fixture.id,
		WorkspaceName: fixture.name,
		WorkspaceSlug: fixture.slug,
		Auth: auth.WorkspaceAuth{
			Type:         auth.TokenSourceOAuth,
			AccessToken:  fixture.token,
			RefreshToken: "refresh-" + fixture.id,
			ExpiresAt:    time.Now().Add(time.Hour),
			UpdatedAt:    time.Now(),
		},
	}
}

// newWorkspaceStore writes a credentials store with both fixtures, activating
// workspace A.
func newWorkspaceStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := auth.NewStore()
	store.Put(workspaceProfileFor(workspaceA))
	store.Put(workspaceProfileFor(workspaceB))
	store.ActiveWorkspace = workspaceA.id
	if err := auth.SaveStore(path, store); err != nil {
		t.Fatalf("SaveStore() error: %v", err)
	}
	return path
}

// newWorkspaceTestApp builds an app authenticated against workspace A.
func newWorkspaceTestApp(t *testing.T, endpoint, storePath string) *App {
	t.Helper()
	cfg := config.Config{
		APIEndpoint: endpoint,
		CacheTTL:    time.Minute,
		PageSize:    10,
	}
	client := linearapi.NewClient(linearapi.ClientConfig{
		Token:     workspaceA.token,
		UseBearer: true,
		Endpoint:  endpoint,
	})
	app := NewApp(client, cfg, nil)
	// Run queued UI updates inline, and stop running them once the test ends so
	// background work cannot touch tview state while the next test builds an app.
	var (
		mu      sync.Mutex
		closed  bool
		pending sync.WaitGroup
	)
	app.queueUpdateDraw = func(f func()) {
		mu.Lock()
		if closed {
			mu.Unlock()
			return
		}
		pending.Add(1)
		mu.Unlock()
		defer pending.Done()
		f()
	}
	t.Cleanup(func() {
		mu.Lock()
		closed = true
		mu.Unlock()
		pending.Wait()
	})
	app.preloadTeamMetadataFunc = func(string) {}
	app.ConfigureWorkspaces(WorkspaceOptions{
		StorePath:     storePath,
		OAuthClient:   oauth.NewClient(oauth.ClientConfig{ClientID: "test-client"}),
		ClientID:      "test-client",
		WorkspaceID:   workspaceA.id,
		WorkspaceName: workspaceA.name,
	})
	return app
}

// currentIssueIDs returns the issue identifiers currently held by the app.
func currentIssueIDs(app *App) []string {
	app.issuesMu.RLock()
	defer app.issuesMu.RUnlock()
	ids := make([]string, 0, len(app.issues))
	for _, issue := range app.issues {
		ids = append(ids, issue.Identifier)
	}
	return ids
}

func TestConfigureWorkspacesLoadsSavedProfiles(t *testing.T) {
	server := newWorkspaceGraphQLServer(t, nil)
	defer server.Close()

	app := newWorkspaceTestApp(t, server.URL, newWorkspaceStore(t))

	if !app.WorkspacesEnabled() {
		t.Fatal("expected workspace switching to be enabled")
	}
	if app.ActiveWorkspaceID() != workspaceA.id || app.ActiveWorkspaceName() != workspaceA.name {
		t.Fatalf("active workspace = %q/%q", app.ActiveWorkspaceID(), app.ActiveWorkspaceName())
	}
	profiles := app.WorkspaceProfiles()
	if len(profiles) != 2 {
		t.Fatalf("profiles = %d, want 2", len(profiles))
	}
	if profiles[0].DisplayName() != workspaceA.name || profiles[1].DisplayName() != workspaceB.name {
		t.Fatalf("profiles = %+v", profiles)
	}
}

func TestSwitchWorkspaceSwapsClientCacheAndState(t *testing.T) {
	var mu sync.Mutex
	tokensSeen := map[string]int{}
	server := newWorkspaceGraphQLServer(t, func(token, _ string) {
		mu.Lock()
		tokensSeen[token]++
		mu.Unlock()
	})
	defer server.Close()

	storePath := newWorkspaceStore(t)
	app := newWorkspaceTestApp(t, server.URL, storePath)

	// Seed state belonging to workspace A.
	app.issuesMu.Lock()
	app.issues = []linearapi.Issue{{ID: workspaceA.issueID, Identifier: workspaceA.issueID, Title: "A issue"}}
	app.selectedIssue = &linearapi.Issue{ID: workspaceA.issueID, Identifier: workspaceA.issueID}
	app.issuesMu.Unlock()
	app.expandedState[workspaceA.issueID] = true
	app.searchQuery = "stale search"
	app.richFilters = IssueFilters{AssigneeID: "user-a", AssigneeName: "User A"}
	app.teamUsers = []linearapi.User{{ID: "user-a"}}
	app.workflowStates = []linearapi.WorkflowState{{ID: "state-a"}}
	app.teamProjects = []linearapi.Project{{ID: "project-a"}}

	oldClient := app.api
	oldCache := app.cache
	oldGeneration := app.WorkspaceGeneration()

	app.SwitchWorkspace(workspaceB.id)

	waitForCondition(t, 3*time.Second, func() bool {
		if app.ActiveWorkspaceID() != workspaceB.id {
			return false
		}
		for _, id := range currentIssueIDs(app) {
			if id == workspaceB.issueID {
				return true
			}
		}
		return false
	})

	if app.api == oldClient {
		t.Fatal("API client was not replaced on workspace switch")
	}
	if app.cache == oldCache {
		t.Fatal("metadata cache was not replaced on workspace switch")
	}
	if app.WorkspaceGeneration() == oldGeneration {
		t.Fatal("workspace generation did not change")
	}
	if app.ActiveWorkspaceName() != workspaceB.name {
		t.Fatalf("workspace name = %q, want %q", app.ActiveWorkspaceName(), workspaceB.name)
	}

	for _, id := range currentIssueIDs(app) {
		if id == workspaceA.issueID {
			t.Fatalf("issues from the previous workspace are still visible: %v", currentIssueIDs(app))
		}
	}
	app.issuesMu.RLock()
	selected := app.selectedIssue
	app.issuesMu.RUnlock()
	if selected != nil && selected.Identifier == workspaceA.issueID {
		t.Fatalf("selected issue survived the switch: %+v", selected)
	}
	if app.searchQuery != "" || !app.richFilters.Empty() {
		t.Fatalf("filters survived the switch: query=%q filters=%+v", app.searchQuery, app.richFilters)
	}
	if len(app.expandedState) != 0 {
		t.Fatalf("expanded sub-issues survived the switch: %+v", app.expandedState)
	}
	if len(app.teamUsers) != 0 || len(app.workflowStates) != 0 || len(app.teamProjects) != 0 {
		t.Fatal("team-scoped metadata survived the switch")
	}

	// Navigation shows the new workspace's teams only.
	waitForCondition(t, 3*time.Second, func() bool {
		root := app.navigationTree.GetRoot()
		if root == nil {
			return false
		}
		for _, child := range root.GetChildren() {
			if child.GetText() == workspaceB.teamName {
				return true
			}
		}
		return false
	})
	for _, child := range app.navigationTree.GetRoot().GetChildren() {
		if child.GetText() == workspaceA.teamName {
			t.Fatal("previous workspace team is still in the navigation tree")
		}
	}

	// The active workspace is persisted.
	store, err := auth.LoadStore(storePath)
	if err != nil {
		t.Fatalf("LoadStore() error: %v", err)
	}
	if store.ActiveWorkspace != workspaceB.id {
		t.Fatalf("persisted active workspace = %q, want %q", store.ActiveWorkspace, workspaceB.id)
	}

	// Mutations use the new workspace's client.
	before := requestsFor(&mu, tokensSeen, "Bearer "+workspaceA.token)
	if _, err := app.updateIssueFunc(context.Background(), linearapi.UpdateIssueInput{
		ID:      workspaceB.issueID,
		StateID: stringPtr("state-1"),
	}); err != nil {
		t.Fatalf("updateIssueFunc() error: %v", err)
	}
	if got := requestsFor(&mu, tokensSeen, "Bearer "+workspaceA.token); got != before {
		t.Fatal("mutation after switching used the previous workspace token")
	}
	if requestsFor(&mu, tokensSeen, "Bearer "+workspaceB.token) == 0 {
		t.Fatal("no requests were made with the new workspace token")
	}
}

// requestsFor reports how many requests carried the given auth header.
func requestsFor(mu *sync.Mutex, counts map[string]int, token string) int {
	mu.Lock()
	defer mu.Unlock()
	return counts[token]
}

func TestSwitchWorkspaceDiscardsInFlightIssuesFromPreviousWorkspace(t *testing.T) {
	server := newWorkspaceGraphQLServer(t, nil)
	defer server.Close()

	app := newWorkspaceTestApp(t, server.URL, newWorkspaceStore(t))

	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	app.fetchIssuesPage = func(_ context.Context, _ linearapi.FetchIssuesParams, _ *string) (linearapi.IssuePage, error) {
		once.Do(func() { close(started) })
		<-release
		return linearapi.IssuePage{
			Issues: []linearapi.Issue{{ID: workspaceA.issueID, Identifier: workspaceA.issueID, Title: "stale"}},
		}, nil
	}

	go app.refreshIssues()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the first fetch to start")
	}

	app.SwitchWorkspace(workspaceB.id)
	waitForCondition(t, 3*time.Second, func() bool {
		return app.ActiveWorkspaceID() == workspaceB.id
	})

	// The workspace A response arrives after the switch and must be dropped.
	close(release)
	waitForCondition(t, 3*time.Second, func() bool {
		for _, id := range currentIssueIDs(app) {
			if id == workspaceB.issueID {
				return true
			}
		}
		return false
	})
	time.Sleep(50 * time.Millisecond)

	for _, id := range currentIssueIDs(app) {
		if id == workspaceA.issueID {
			t.Fatalf("stale workspace issues leaked into the active workspace: %v", currentIssueIDs(app))
		}
	}
}

func TestQueueWorkspaceUpdateDropsStaleUpdates(t *testing.T) {
	app := newUXTestApp()

	generation := app.WorkspaceGeneration()
	applied := 0
	app.queueWorkspaceUpdate(generation, func() { applied++ })
	if applied != 1 {
		t.Fatalf("current-generation update applied %d times, want 1", applied)
	}

	app.workspaceGeneration.Add(1)
	app.queueWorkspaceUpdate(generation, func() { applied++ })
	if applied != 1 {
		t.Fatalf("stale update was applied: %d", applied)
	}
}

func TestSwitchWorkspaceDisabledByAPIKeyOverride(t *testing.T) {
	server := newWorkspaceGraphQLServer(t, nil)
	defer server.Close()

	storePath := newWorkspaceStore(t)
	app := newWorkspaceTestApp(t, server.URL, storePath)
	app.workspaceMu.Lock()
	app.workspaceEnvOverride = true
	app.workspaceMu.Unlock()

	app.SwitchWorkspace(workspaceB.id)

	if app.ActiveWorkspaceID() != workspaceA.id {
		t.Fatalf("active workspace changed to %q despite the API key override", app.ActiveWorkspaceID())
	}
	if !strings.Contains(app.statusMessage, "LINEAR_API_KEY") {
		t.Fatalf("status message = %q, want an override explanation", app.statusMessage)
	}

	// The switcher marks the environment-provided workspace.
	app.ShowWorkspaceSwitcher()
	first, _ := app.workspaceSwitcher.list.GetItemText(0)
	if !strings.Contains(first, "ENV") {
		t.Fatalf("first row = %q, want an ENV marker on the active workspace", first)
	}
	store, err := auth.LoadStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if store.ActiveWorkspace != workspaceA.id {
		t.Fatalf("persisted active workspace = %q, want it untouched", store.ActiveWorkspace)
	}
}

func TestConnectWorkspaceSavesProfileAndSwitches(t *testing.T) {
	server := newWorkspaceGraphQLServer(t, nil)
	defer server.Close()

	path := filepath.Join(t.TempDir(), "credentials.json")
	if _, err := auth.ConnectWorkspace(path, workspaceProfileFor(workspaceA)); err != nil {
		t.Fatal(err)
	}

	app := newWorkspaceTestApp(t, server.URL, path)
	app.workspaceLoginFunc = func(_ context.Context, opts auth.LoginOptions) (auth.WorkspaceProfile, error) {
		profile := workspaceProfileFor(workspaceB)
		if _, err := auth.ConnectWorkspace(opts.StorePath, profile); err != nil {
			return auth.WorkspaceProfile{}, err
		}
		return profile, nil
	}

	app.ConnectWorkspace()

	waitForCondition(t, 3*time.Second, func() bool {
		return app.ActiveWorkspaceID() == workspaceB.id
	})

	store, err := auth.LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Workspaces) != 2 {
		t.Fatalf("workspaces = %d, want the existing workspace kept alongside the new one", len(store.Workspaces))
	}
	if _, ok := store.Profile(workspaceA.id); !ok {
		t.Fatal("connecting a workspace removed the previously connected one")
	}
	if len(app.WorkspaceProfiles()) != 2 {
		t.Fatalf("app profiles = %+v", app.WorkspaceProfiles())
	}
}

func TestDisconnectActiveWorkspaceSwitchesToRemaining(t *testing.T) {
	server := newWorkspaceGraphQLServer(t, nil)
	defer server.Close()

	storePath := newWorkspaceStore(t)
	app := newWorkspaceTestApp(t, server.URL, storePath)

	app.disconnectWorkspaceConfirmed(workspaceA.id, workspaceA.name)

	waitForCondition(t, 3*time.Second, func() bool {
		return app.ActiveWorkspaceID() == workspaceB.id
	})

	store, err := auth.LoadStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Profile(workspaceA.id); ok {
		t.Fatal("disconnected workspace is still saved")
	}
	remaining, ok := store.Profile(workspaceB.id)
	if !ok || remaining.Auth.RefreshToken != "refresh-"+workspaceB.id {
		t.Fatalf("remaining workspace credentials changed: %+v", remaining)
	}
}

func TestWorkspaceSwitcherModalMarksActiveAndOffersConnect(t *testing.T) {
	server := newWorkspaceGraphQLServer(t, nil)
	defer server.Close()

	app := newWorkspaceTestApp(t, server.URL, newWorkspaceStore(t))
	app.ShowWorkspaceSwitcher()

	modal := app.workspaceSwitcher
	if !app.pages.HasPage(workspaceSwitcherPage) {
		t.Fatal("workspace switcher page was not shown")
	}
	if modal.list.GetItemCount() != 3 {
		t.Fatalf("list rows = %d, want two workspaces plus the connect row", modal.list.GetItemCount())
	}
	first, _ := modal.list.GetItemText(0)
	if !strings.Contains(first, workspaceA.name) || !strings.Contains(first, "●") {
		t.Fatalf("first row = %q, want the active workspace marked", first)
	}
	second, _ := modal.list.GetItemText(1)
	if strings.Contains(second, "●") {
		t.Fatalf("second row = %q, want no active marker", second)
	}
	connect, _ := modal.list.GetItemText(modal.connectIndex)
	if !strings.Contains(connect, "Connect another workspace") {
		t.Fatalf("connect row = %q", connect)
	}
	if modal.list.GetCurrentItem() != 0 {
		t.Fatalf("selection = %d, want the active workspace preselected", modal.list.GetCurrentItem())
	}

	// Enter on another workspace switches to it and closes the modal.
	modal.HandleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	modal.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if app.pages.HasPage(workspaceSwitcherPage) {
		t.Fatal("workspace switcher stayed open after Enter")
	}
	waitForCondition(t, 3*time.Second, func() bool {
		return app.ActiveWorkspaceID() == workspaceB.id
	})
}

func TestWorkspaceSwitcherEscapeClosesWithoutSwitching(t *testing.T) {
	server := newWorkspaceGraphQLServer(t, nil)
	defer server.Close()

	app := newWorkspaceTestApp(t, server.URL, newWorkspaceStore(t))
	app.ShowWorkspaceSwitcher()
	app.workspaceSwitcher.HandleKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))

	if app.pages.HasPage(workspaceSwitcherPage) {
		t.Fatal("Escape did not close the workspace switcher")
	}
	if app.ActiveWorkspaceID() != workspaceA.id {
		t.Fatalf("active workspace = %q, want it unchanged", app.ActiveWorkspaceID())
	}
}

func TestStatusBarShowsActiveWorkspace(t *testing.T) {
	server := newWorkspaceGraphQLServer(t, nil)
	defer server.Close()

	app := newWorkspaceTestApp(t, server.URL, newWorkspaceStore(t))
	app.updateStatusBar()

	if got := app.statusBar.GetText(true); !strings.Contains(got, "Workspace: "+workspaceA.name) {
		t.Fatalf("status bar = %q, want the active workspace", got)
	}
}

func TestWorkspacePaletteCommandsAreAvailable(t *testing.T) {
	app := newUXTestApp()

	want := map[string]string{
		"switch_workspace":     "Switch Workspace",
		"connect_workspace":    "Connect Workspace",
		"disconnect_workspace": "Disconnect Workspace",
	}
	found := map[string]bool{}
	for _, cmd := range app.paletteCtrl.commands {
		if title, ok := want[cmd.ID]; ok {
			if cmd.Title != title {
				t.Fatalf("command %s title = %q, want %q", cmd.ID, cmd.Title, title)
			}
			found[cmd.ID] = true
		}
	}
	for id := range want {
		if !found[id] {
			t.Fatalf("command %q is missing from the palette", id)
		}
	}

	// The palette finds the switcher by name, as the acceptance flow does.
	app.paletteCtrl.SetQuery("switch workspace")
	matches := app.paletteCtrl.Filtered()
	if len(matches) == 0 || matches[0].ID != "switch_workspace" {
		t.Fatalf("palette search returned %+v", matches)
	}
}
