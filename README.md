# linear-tui

A terminal user interface (TUI) for Linear built with Go and tview.

## Screenshots

![Main interface](docs/main.jpeg)

![Create issue](docs/create.png)

![Assign issue](docs/assign.png)

## Demo

![Agent demo](docs/agent-demo.gif)

## Features

- 3-pane layout (navigation tree + issues list + details view)
- Command palette for quick actions with keyboard shortcuts
- Vim-style keyboard navigation (j/k, h/l, g/G)
- Mouse support (click to focus, scroll to navigate)
- Issue descriptions with markdown rendering
- Sub-issues support (expand/collapse, create, view parent)
- Issue management (create, edit title, edit labels, archive)
- Comments (view and add)
- Status management (change status, assign/unassign)
- Multiple Linear workspaces with in-app switching (`:` -> `Switch Workspace`)
- Search and filtering
- Sorting (by updated, created, or priority)
- My Issues vs Other Issues sections
- Agent runs via command palette (Claude or Cursor Agent)
- Agent prompt templates and streaming output with copy/resume
- Real-time issue fetching from Linear API
- Comprehensive logging system for debugging
- Settings modal with live config updates
- Themes (linear, high_contrast, color_blind) and density modes
- Status bar with context and search info
- Clipboard actions (issue ID, issue URL, agent output)

## Requirements

- Linear authentication via either:
  - `linear-tui auth login` (OAuth, recommended, once per workspace), or
  - `LINEAR_API_KEY` environment variable (personal API key override)
- Agent CLI for the agent command:
  - Claude provider: `claude`
  - Cursor provider: `cursor-agent` (preferred) or `agent`

## Configuration

- Prefer `linear-tui auth login` to store OAuth credentials in `~/.linear-tui/credentials.json` (mode `0600`).
- Credentials hold one profile per connected workspace; see [Multiple workspaces](#multiple-workspaces).
- `LINEAR_API_KEY` overrides stored OAuth credentials when set.
- Use `linear-tui auth logout` to revoke (best effort) and delete stored credentials for every workspace.
- Settings are stored in `~/.linear-tui/config.json` and created on first start.
- Use the Settings modal from the command palette (`:` -> `Settings`) to edit and apply settings immediately.
- UI settings in `config.json`: `theme` (`linear`, `high_contrast`, `color_blind`) and `density` (`comfortable`, `compact`).
- Search settings in `config.json`: `search_debounce` controls the live search debounce delay (default `300ms`).
- Agent settings live in `config.json`: `agent_provider` (`cursor` or `claude`), `agent_sandbox` (`enabled` or `disabled`), `agent_model` (optional), and `agent_workspace` (optional).
- Prompt templates are stored in `~/.linear-tui/prompts.json` and edited via the "Edit agent prompt templates" command.
- `agent_workspace` is the default workspace for agent runs and can be overridden per run in the Ask Agent modal (overrides are not persisted).
- Startup view settings in `config.json`: `default_team` opens the app with that team selected (matched by team key or name, case-insensitive), and `default_project` narrows it to a project within that team (matched by name). Both are optional; blank opens All Issues. If a value doesn't match, the app falls back to the standard view and shows a status-bar warning.

Example `~/.linear-tui/config.json`:

```json
{
  "api_endpoint": "https://api.linear.app/graphql",
  "timeout": "30s",
  "page_size": 50,
  "cache_ttl": "5m",
  "search_debounce": "300ms",
  "log_file": "/Users/you/.linear-tui/app.log",
  "log_level": "warning",
  "theme": "linear",
  "density": "comfortable",
  "agent_provider": "cursor",
  "agent_sandbox": "enabled",
  "agent_model": "",
  "agent_workspace": "",
  "default_team": "",
  "default_project": ""
}
```

## Installation

### Homebrew (macOS)

```bash
brew install roeyazroel/linear-tui/linear-tui
```

### From Source

Requires Go 1.24 or later:

```bash
go install github.com/roeyazroel/linear-tui/cmd/linear-tui@latest
```

Or clone and build locally:

```bash
git clone https://github.com/roeyazroel/linear-tui.git
cd linear-tui
go build ./cmd/linear-tui
```

### Download Binary

Download pre-built binaries from the [Releases](https://github.com/roeyazroel/linear-tui/releases) page.

## Usage

Authenticate once, then start the app.

### OAuth login (recommended)

```bash
linear-tui auth login
linear-tui
```

### Personal API key override

```bash
export LINEAR_API_KEY="your-api-key-here"
linear-tui
```

### Homebrew

```bash
linear-tui auth login
linear-tui
```

### Local Build

If you cloned the repository and built the binary locally, run the local executable:

```bash
./linear-tui auth login
./linear-tui
```

To log out and remove stored OAuth credentials:

```bash
linear-tui auth logout
```

### Advanced Configuration

Example `~/.linear-tui/config.json`:

```json
{
  "api_endpoint": "https://api.linear.app/graphql",
  "timeout": "30s",
  "page_size": 50,
  "cache_ttl": "5m",
  "search_debounce": "300ms",
  "log_file": "/Users/you/.linear-tui/app.log",
  "log_level": "warning",
  "theme": "linear",
  "density": "comfortable",
  "agent_provider": "cursor",
  "agent_sandbox": "enabled",
  "agent_model": "",
  "agent_workspace": "",
  "default_team": "",
  "default_project": ""
}
```

### Disable Logging

To disable logging, set `log_file` to an empty string in the settings file or via the Settings modal:

```json
{
  "log_file": ""
}
```

## Multiple workspaces

linear-tui can stay connected to several Linear workspaces at once and switch
between them without restarting, re-authenticating, or editing files.

### Switch workspace

Press `:` to open the command palette and choose **Switch Workspace** (or press
`O` then `W`):

```text
┌──────────────── Switch Workspace ────────────────┐
│ Active: PocketBooks                              │
│                                                  │
│ > PocketBooks                              ●     │
│   Resilion                                       │
│   Personal                                       │
│                                                  │
│   + Connect another workspace                    │
│                                                  │
│ Enter switch | n connect | d disconnect | Esc    │
└──────────────────────────────────────────────────┘
```

The active workspace is marked with `●` and shown in the status bar as
`Workspace: PocketBooks`. Selecting another workspace swaps the API client,
clears every workspace-scoped cache and selection, and loads the new
workspace's teams, projects, issues, users, labels, statuses, and cycles in
place.

### Connect workspace

`:` -> **Connect Workspace** (or `n` in the switcher) runs the browser OAuth
flow for an additional workspace. Linear's consent screen is always shown so
you can pick which workspace to authorize. Reconnecting a workspace that is
already saved updates that profile instead of creating a duplicate; other
workspaces are never touched.

### Disconnect workspace

`:` -> **Disconnect Workspace** (or `d` in the switcher) asks for confirmation
and then removes the saved local credentials for that workspace only. It does
not delete anything from Linear. Disconnecting the active workspace switches to
another saved workspace when one exists.

### CLI

```bash
linear-tui auth login              # connect a workspace (keeps existing ones)
linear-tui auth list               # list connected workspaces
linear-tui auth use Resilion       # set the active workspace
linear-tui auth remove Resilion    # disconnect one workspace
linear-tui auth logout             # revoke and remove every workspace
```

`auth list` prints the saved workspaces and marks the active one:

```text
WORKSPACE                STATUS
PocketBooks              active
Resilion
Personal
```

Workspaces can be referenced by name, URL slug, or workspace ID; identity is
tracked internally by the stable Linear workspace ID, so renames are safe.

### LINEAR_API_KEY precedence

`LINEAR_API_KEY` remains an explicit, process-level override:

- it takes precedence over every saved workspace,
- it is never written to the credentials file,
- workspace switching is disabled while it is set, and the switcher shows
  `● ENV` next to the active workspace.

```text
Workspace switching is unavailable while LINEAR_API_KEY overrides saved credentials.
```

Unset the variable and restart linear-tui to switch between saved workspaces.

### Credential migration

Credentials from earlier versions (a single workspace at
`~/.linear-tui/credentials.json`) keep working. On the first launch after
upgrading, linear-tui identifies the workspace behind the existing token and
rewrites the file in the multi-workspace format, preserving the access token,
refresh token, and expiry, and marking that workspace active. No re-login is
required; if the workspace cannot be reached the old file is left untouched and
migration is retried on the next launch. Writes are atomic and the file keeps
mode `0600`. Each workspace refreshes its own OAuth tokens independently.

## Keyboard Shortcuts

### Navigation

- `j` / `↓` - Move down
- `k` / `↑` - Move up
- `h` / `←` - Focus left pane
- `l` / `→` - Focus right pane
- `g` - Jump to top
- `G` - Jump to bottom
- `Tab` / `Shift+Tab` - Cycle between panes
- `Space` - Toggle expand/collapse sub-issues
- `Enter` - Select issue / Execute command
- `Esc` - Close palette / Cancel / Clear search
- `q` - Quit

### Command Palette

- `:` - Open command palette
- `/` - Open search palette
- `O` then `W` - Switch workspace
- `ask agent` - Run a terminal agent on the selected issue
- `switch workspace` / `connect workspace` / `disconnect workspace` - Manage Linear workspaces

### Quick Commands

- `r` - Refresh issues
- `n` - Create new issue
- `e` - Edit issue title
- `g` - Edit issue labels
- `s` - Change status
- `a` - Assign to user
- `m` - Assign to me
- `u` - Unassign issue
- `t` - Add comment
- `o` - Open in browser
- `y` - Copy issue ID
- `w` - Copy issue URL
- `x` - Archive issue
- `b` - Create sub-issue
- `p` - View parent issue
- `i` - Set parent issue
- `d` - Remove parent
- `]` - Expand all sub-issues
- `[` - Collapse all sub-issues

## Development

Run tests:

```bash
go test ./...
```

Build:

```bash
go build ./cmd/linear-tui
```
