package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/roeyazroel/linear-tui/internal/auth"
	"github.com/roeyazroel/linear-tui/internal/auth/oauth"
	"github.com/roeyazroel/linear-tui/internal/config"
	"github.com/roeyazroel/linear-tui/internal/linearapi"
	"github.com/roeyazroel/linear-tui/internal/logger"
	"github.com/roeyazroel/linear-tui/internal/tui"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run executes the CLI entrypoint and returns a process exit code.
func run(args []string) int {
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-v") {
		fmt.Println(VersionInfo())
		return 0
	}

	if len(args) > 0 && args[0] == "auth" {
		return runAuth(args[1:])
	}

	return runTUI()
}

// runAuth handles `linear-tui auth ...` subcommands.
func runAuth(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		auth.PrintAuthUsage(os.Stdout)
		return 0
	}

	storePath, err := auth.CredentialsPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving credentials path: %v\n", err)
		return 1
	}

	clientID := auth.ClientID(oauth.DefaultClientID)
	oauthClient := oauth.NewClient(oauth.ClientConfig{ClientID: clientID})
	ctx := context.Background()

	switch args[0] {
	case "login":
		if clientID == "" {
			fmt.Fprintf(os.Stderr, "Error: OAuth client id is not configured. Set %s.\n", config.LinearClientIDEnv)
			return 1
		}
		fmt.Println("Opening browser for Linear authorization...")
		profile, err := auth.Login(ctx, auth.LoginOptions{
			ClientID:    clientID,
			StorePath:   storePath,
			OAuthClient: oauthClient,
			Identify:    identifyWorkspace,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
			return 1
		}
		fmt.Printf("Connected workspace: %s\n", profile.DisplayName())
		fmt.Println("Credentials stored in", storePath)
		return 0
	case "list":
		return runAuthList(storePath)
	case "use":
		return runAuthUse(storePath, args[1:])
	case "remove":
		return runAuthRemove(ctx, storePath, oauthClient, args[1:])
	case "logout":
		if err := auth.Logout(ctx, auth.LogoutOptions{
			StorePath:   storePath,
			OAuthClient: oauthClient,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Logout failed: %v\n", err)
			return 1
		}
		fmt.Println("Logged out. Stored OAuth credentials removed for all workspaces.")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "Unknown auth command %q\n\n", args[0])
		auth.PrintAuthUsage(os.Stderr)
		return 1
	}
}

// identifyWorkspace resolves the workspace an access token belongs to.
func identifyWorkspace(token string) (auth.WorkspaceIdentity, error) {
	client := linearapi.NewClient(linearapi.ClientConfig{Token: token, UseBearer: true})
	workspace, err := client.GetWorkspace(context.Background())
	if err != nil {
		return auth.WorkspaceIdentity{}, err
	}
	return auth.WorkspaceIdentity{ID: workspace.ID, Name: workspace.Name, Slug: workspace.Slug}, nil
}

// runAuthList prints the connected workspaces and which one is active.
func runAuthList(storePath string) int {
	store, err := auth.LoadStore(storePath)
	if err != nil {
		if errors.Is(err, auth.ErrCredentialsNotFound) {
			fmt.Println("No workspaces connected. Run `linear-tui auth login`.")
			return 0
		}
		fmt.Fprintf(os.Stderr, "Error loading credentials: %v\n", err)
		return 1
	}

	if os.Getenv(config.LinearAPIKeyEnv) != "" {
		fmt.Printf("%s is set and overrides saved credentials for this process.\n\n", config.LinearAPIKeyEnv)
	}

	fmt.Printf("%-24s %s\n", "WORKSPACE", "STATUS")
	for _, profile := range store.List() {
		status := ""
		if profile.WorkspaceID == store.ActiveWorkspace {
			status = "active"
		}
		fmt.Printf("%-24s %s\n", profile.DisplayName(), status)
	}
	if store.Legacy {
		fmt.Println("\nCredentials are in the legacy single-workspace format; they migrate on next launch.")
	}
	return 0
}

// runAuthUse sets the active workspace by id, slug, or name.
func runAuthUse(storePath string, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: linear-tui auth use <workspace>")
		return 1
	}
	query := strings.Join(args, " ")

	var selected auth.WorkspaceProfile
	if _, err := auth.UpdateStore(storePath, func(store *auth.Store) error {
		profile, ok := store.Find(query)
		if !ok {
			return fmt.Errorf("workspace %q is not connected", query)
		}
		selected = profile
		store.SetActive(profile.WorkspaceID)
		return nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	fmt.Printf("Active workspace: %s\n", selected.DisplayName())
	return 0
}

// runAuthRemove disconnects one saved workspace, keeping the others.
func runAuthRemove(ctx context.Context, storePath string, oauthClient *oauth.Client, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: linear-tui auth remove <workspace>")
		return 1
	}
	query := strings.Join(args, " ")

	store, err := auth.LoadStore(storePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading credentials: %v\n", err)
		return 1
	}
	profile, ok := store.Find(query)
	if !ok {
		fmt.Fprintf(os.Stderr, "Workspace %q is not connected\n", query)
		return 1
	}

	updated, removed, err := auth.RemoveWorkspace(ctx, auth.LogoutOptions{
		StorePath:   storePath,
		OAuthClient: oauthClient,
	}, profile.WorkspaceID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error removing workspace: %v\n", err)
		return 1
	}

	fmt.Printf("Disconnected workspace: %s\n", removed.DisplayName())
	if active, ok := updated.ActiveProfile(); ok {
		fmt.Printf("Active workspace: %s\n", active.DisplayName())
	} else {
		fmt.Println("No workspaces remain connected. Run `linear-tui auth login`.")
	}
	return 0
}

// runTUI boots the interactive application with resolved credentials.
func runTUI() int {
	settingsPath, err := config.ConfigFilePath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error determining settings path: %v\n", err)
		return 1
	}

	settings, err := config.EnsureSettingsFile(settingsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading settings file: %v\n", err)
		return 1
	}

	storePath, err := auth.CredentialsPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving credentials path: %v\n", err)
		return 1
	}

	clientID := auth.ClientID(oauth.DefaultClientID)
	oauthClient := oauth.NewClient(oauth.ClientConfig{ClientID: clientID})
	ctx := context.Background()

	apiKey := os.Getenv(config.LinearAPIKeyEnv)
	resolved, err := auth.Resolve(ctx, apiKey, storePath, oauthClient)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading authentication: %v\n", err)
		return 1
	}

	cfg, err := config.ConfigFromSettings(resolved.Token, settings)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		return 1
	}

	logLevel := parseLogLevel(cfg.LogLevel)
	if err := logger.Init(cfg.LogFile, logLevel); err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing logger: %v\n", err)
		return 1
	}
	defer func() {
		if err := logger.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Error closing logger: %v\n", err)
		}
	}()

	logger.Info("app.main: application starting")
	logger.Debug("app.main: configuration endpoint=%s page_size=%d cache_ttl=%s auth_source=%s",
		cfg.APIEndpoint, cfg.PageSize, cfg.CacheTTL, resolved.Source)

	// Legacy single-workspace credentials migrate into the multi-workspace
	// store on first launch, without re-authenticating.
	if resolved.Legacy {
		if migrated, err := migrateLegacyCredentials(ctx, storePath, resolved, cfg); err != nil {
			logger.Warning("app.main: legacy credential migration deferred: %v", err)
		} else {
			resolved = migrated
		}
	}

	clientCfg := linearapi.ClientConfig{
		Token:     cfg.LinearAPIKey,
		UseBearer: resolved.Source == auth.TokenSourceOAuth,
		Endpoint:  cfg.APIEndpoint,
		Timeout:   cfg.Timeout,
	}
	if resolved.Source == auth.TokenSourceOAuth {
		clientCfg.OnUnauthorized = auth.NewRefreshFunc(storePath, resolved.WorkspaceID, oauthClient)
	}

	apiClient := linearapi.NewClient(clientCfg)

	promptTemplates := config.DefaultAgentPromptTemplates()
	promptsPath, err := config.PromptTemplatesFilePath()
	if err != nil {
		logger.Warning("app.main: failed to resolve prompts file path: %v", err)
	} else {
		templates, err := config.EnsurePromptTemplatesFile(promptsPath)
		if err != nil {
			logger.Warning("app.main: failed to load prompts file path=%s error=%v", promptsPath, err)
		} else {
			promptTemplates = templates
		}
	}

	app := tui.NewApp(apiClient, cfg, promptTemplates)
	app.ConfigureWorkspaces(tui.WorkspaceOptions{
		StorePath:      storePath,
		OAuthClient:    oauthClient,
		ClientID:       clientID,
		APIKeyOverride: resolved.Source == auth.TokenSourceAPIKey,
		WorkspaceID:    resolved.WorkspaceID,
		WorkspaceName:  resolved.WorkspaceName,
	})

	if err := app.Run(); err != nil {
		logger.ErrorWithErr(err, "app.main: application error")
		fmt.Fprintf(os.Stderr, "Error running application: %v\n", err)
		if closeErr := logger.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Error closing logger: %v\n", closeErr)
		}
		return 1
	}

	logger.Info("app.main: application shutdown")
	return 0
}

// migrateLegacyCredentials identifies the workspace behind legacy credentials
// and rewrites the credentials file in the multi-workspace format.
func migrateLegacyCredentials(ctx context.Context, storePath string, resolved auth.ResolvedAuth, cfg config.Config) (auth.ResolvedAuth, error) {
	client := linearapi.NewClient(linearapi.ClientConfig{
		Token:     resolved.Token,
		UseBearer: true,
		Endpoint:  cfg.APIEndpoint,
		Timeout:   cfg.Timeout,
	})
	workspace, err := client.GetWorkspace(ctx)
	if err != nil {
		return resolved, fmt.Errorf("identify workspace: %w", err)
	}

	if _, err := auth.MigrateLegacyCredentials(storePath, auth.WorkspaceIdentity{
		ID:   workspace.ID,
		Name: workspace.Name,
		Slug: workspace.Slug,
	}); err != nil {
		return resolved, err
	}

	resolved.WorkspaceID = workspace.ID
	resolved.WorkspaceName = workspace.Name
	resolved.Legacy = false
	return resolved, nil
}

// parseLogLevel converts a string log level to a logger.LogLevel.
func parseLogLevel(level string) logger.LogLevel {
	switch level {
	case "debug":
		return logger.LevelDebug
	case "info":
		return logger.LevelInfo
	case "warning":
		return logger.LevelWarning
	case "error":
		return logger.LevelError
	default:
		return logger.LevelWarning
	}
}
