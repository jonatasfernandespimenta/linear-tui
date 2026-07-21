# Linear Features & API vs linear-tui — Gap Report

**Date:** 2026-07-21  
**Scope:** Compare Linear product features and public GraphQL API surface with this repository’s TUI (`internal/tui`) and API client (`internal/linearapi`).  
**Sources:** Linear product docs (`linear.app/docs`), Linear developer docs (`developers.linear.app`), Linear SDK GraphQL documents (public schema operations), and current codebase inventory.  
**Note:** Linear MCP in this environment required authentication and was not used; comparison is based on published docs/schema plus local code.

This supersedes the earlier backlog notes in `docs/linear-feature-gap-audit.md` (many items listed there are now implemented).

---

## Executive summary

`linear-tui` is a strong **issue-centric terminal client**: browse teams/projects/cycles, manage core issue fields, collaborate via comments/relations/subscribers/attachments, and run local CLI agents against an issue.

Relative to Linear’s full product/API, the largest gaps are **non-issue workspace surfaces** (initiatives/roadmaps, project updates, documents, notifications, custom views, triage inbox UX, customers/Asks, releases/SLAs/insights) and several **issue lifecycle polish** items where the API client already has hooks but the TUI does not (set priority, edit description, unarchive, move between projects, create attachments/reactions).

| Layer | Rough coverage of Linear’s issue workflow | Coverage of Linear’s broader product |
| --- | --- | --- |
| TUI UX | High for day-to-day issue execution | Low |
| Go API client | Medium–high for issues/teams/cycles/projects/milestones | Low outside issue neighborhood |
| Linear GraphQL API | Full platform surface (~340 queries / ~323 mutations in SDK docs) | N/A (source of truth) |

---

## Methodology

1. Inventory TUI commands (`DefaultCommands` in `internal/tui/commands.go`), details pane, navigation tree, and planning/collaboration actions.
2. Inventory `internal/linearapi.Client` methods and issue model fields.
3. Map Linear conceptual model and major product features from docs.
4. Cross-check against Linear GraphQL operation catalog (initiatives, documents, notifications, customers, releases, agents, templates, custom views, reactions, webhooks, etc.).
5. Classify each area as **Covered**, **Partial**, or **Gap**, with API vs TUI distinction where they diverge.

---

## Linear conceptual model (product)

From [Linear Concepts](https://linear.app/docs/conceptual-model):

| Concept | Role |
| --- | --- |
| Issues | Atomic work items (status, assignee, labels, priority, cycle, project, comments) |
| Teams / sub-teams | Own workflows, cycles, labels, triage |
| Workflows | Ordered statuses; Triage as intake |
| Cycles | Repeating short-term planning windows |
| Projects + milestones | Outcome-oriented grouping and staged delivery |
| Initiatives (roadmaps) | Strategic grouping of projects |
| Views | Saved filtered perspectives (personal/team/workspace) |

Additional major product surfaces researched: Triage inbox actions, Documents, Project/Initiative updates, Notifications, Customers & customer needs, Linear Asks, SLAs, Releases & release pipelines, Insights, Favorites, Templates, Agent sessions/skills, Webhooks/OAuth apps, Git automations.

---

## Current linear-tui coverage

### Product / UX (TUI)

**Covered well**

- 3-pane layout: navigation tree → issue lists (My / Other) → details
- Command palette + vim-style navigation + mouse
- Teams → projects, statuses, cycles navigation
- Issue list search, sorting (updated/created/priority), rich filters (assignee, labels, status, project, cycle, due date, estimate, text)
- Issue create (title, description, assignee, cycle, priority; project from nav context)
- Issue edit: title, labels, status, assignee, cycle, due date, estimate, milestone
- Parent/sub-issue tree (create, set/remove parent, expand/collapse)
- Comments: view + create
- Relations: blocking / blocked by / related / duplicate / similar (add/remove + details display)
- Subscribers: subscribe/unsubscribe + details display
- Attachments: list in details; open URL / copy URL
- Archive issue; open in browser; copy ID/URL
- Local agent runs (Claude / Cursor Agent) with prompt templates and streaming output
- Themes, density, settings modal, logging

**Partial**

- Priority: shown/sorted/settable on **create**; **no** post-create “set priority” command
- Description: rendered (markdown); **no** edit-description command (API client supports update)
- Milestones: assign/clear/list for issues with a project; **no** milestone CRUD; **no** dedicated milestone nav/filter command (filter param exists in API client)
- Projects: browse + filter + create-into-project; **no** project property editing, updates, or move-issue-between-projects
- Cycles: browse/filter/set/clear; **no** cycle create/edit/shift/admin
- Attachments: read/open only; **no** link/create/delete
- Comments: create only; **no** edit/delete/resolve/reactions
- Labels: assign existing; **no** create/retire label admin
- Archive only; API has unarchive, TUI does not expose it
- Auth: personal `LINEAR_API_KEY` only (no OAuth app flow)

### API client (`internal/linearapi`)

**Implemented**

| Area | Methods / support |
| --- | --- |
| Auth | Personal API key via `Authorization` header |
| Teams / users / states / labels | `ListTeams`, `ListUsers`, `GetCurrentUser`, `ListWorkflowStates`, label list helpers |
| Projects / milestones / cycles | `ListProjects`, `ListProjectMilestones`, `ListCycles` |
| Issues read | Paginated `FetchIssues` / `searchIssues`, `FetchIssueByID`, filters incl. due/estimate/milestone |
| Issues write | `CreateIssue`, `UpdateIssue` (title, description, state, cycle, assignee, priority, labels, parent, due, estimate, milestone), `ArchiveIssue`, `UnarchiveIssue` |
| Collaboration | `CreateComment`, `CreateIssueRelation`, `DeleteIssueRelation`, `SubscribeToIssue`, `UnsubscribeFromIssue` |
| Issue model extras | Relations, subscribers, attachments parsed on fetch |

**Not implemented in client (despite Linear API support)**

- Initiatives / roadmaps / initiative updates
- Project CRUD, project updates, project labels/relations
- Documents
- Notifications / favorites / reminders
- Custom views / saved filters as entities
- Issue templates / team templates
- Customers / customer needs / tiers
- Releases / release pipelines / release notes
- SLAs / triage responsibility configuration
- Reactions; attachment create/link/delete
- Comment update/delete/resolve
- Webhooks; OAuth; file upload
- Linear Agent sessions/skills (platform agents — distinct from local CLI agent feature)
- Realtime sync via webhooks or subscriptions (poll/refresh only)

---

## Gap matrix

Legend: **C** = covered, **P** = partial, **G** = gap, **—** = intentionally out of scope for a TUI / N/A.

### A. Core issue execution

| Capability | Linear product | Linear API | TUI | API client | Notes |
| --- | --- | --- | --- | --- | --- |
| List/filter/search issues | C | C | C | C | Cursor pagination + progress in client |
| Create issue | C | C | C | C | |
| Edit title / labels / status / assignee | C | C | C | C | |
| Set priority after create | C | C | **G** | C (`UpdateIssue.Priority`) | Easy win |
| Edit description | C | C | **G** | C | Easy win |
| Due date / estimate | C | C | C | C | |
| Parent / sub-issues | C | C | C | C | |
| Relations / dependencies | C | C | C | C | |
| Comments create | C | C | C | C | |
| Comments edit/delete/resolve | C | C | **G** | **G** | |
| Reactions on comments | C | C | **G** | **G** | `reactionCreate` / `reactionDelete` |
| Attachments view/open | C | C | C | C (read) | |
| Attachments create/link | C | C | **G** | **G** | Many `attachmentLink*` mutations |
| Subscribe / unsubscribe | C | C | C | C | |
| Archive | C | C | C | C | |
| Unarchive | C | C | **G** | C | Wire TUI command |
| Move issue to another project | C | C | **G** | **G** | `UpdateIssue` lacks `ProjectID` |
| Issue history / activity timeline | C | C | **G** | **G** | |
| Issue reminders | C | C | **G** | **G** | `issueReminder` |
| Issue SLA field | C | C | **G** | **G** | Enterprise-oriented |
| Delegates / agent assignment fields | C | C | **G** | **G** | Platform agent model |

### B. Planning hierarchy

| Capability | Linear product | Linear API | TUI | API client | Notes |
| --- | --- | --- | --- | --- | --- |
| Teams browse | C | C | C | C | Sub-team inheritance UX not modeled |
| Cycles browse / assign | C | C | C | C | |
| Cycle admin (create/shift) | C | C | **G** | **G** | |
| Projects browse / filter | C | C | C | C | |
| Project create/update/properties | C | C | **G** | **G** | |
| Project milestones assign | C | C | C | C | |
| Milestone CRUD | C | C | **G** | **G** | |
| Filter nav by milestone | C | C | **P** | C | Client filter exists; no TUI command |
| Initiatives / sub-initiatives | C | C | **G** | **G** | Large strategic surface |
| Roadmaps (legacy API still present) | C | C | **G** | **G** | Docs: roadmaps → initiatives |
| Project updates / initiative updates | C | C | **G** | **G** | Async status posts |
| Releases / pipelines / notes | C | C | **G** | **G** | Newer product area |

### C. Intake, views, and workspace UX

| Capability | Linear product | Linear API | TUI | API client | Notes |
| --- | --- | --- | --- | --- | --- |
| Triage inbox + accept/decline/snooze | C | P (via status/workflow) | **G** | **G** | Dedicated triage UX missing |
| Triage responsibility / rules | C | C | **G** | **G** | Business+ |
| Custom views (saved) | C | C | **G** | **G** | TUI has ad-hoc rich filters only |
| Favorites / sidebar pins | C | C | **G** | **G** | |
| Issue templates | C | C | **G** | **G** | Local agent prompt templates ≠ Linear templates |
| Notifications inbox / snooze / mark read | C | C | **G** | **G** | |
| Documents | C | C | **G** | **G** | |
| Customers / needs / tiers | C | C | **G** | **G** | |
| Linear Asks intake | C | P | **G** | **G** | Product + integrations |
| Insights / analytics | C | — | **G** | — | UI analytics; not primary API goal |
| Board / timeline displays | C | — | **G** | — | TUI is list-first by design |

### D. Platform, sync, and auth

| Capability | Linear product | Linear API | TUI | API client | Notes |
| --- | --- | --- | --- | --- | --- |
| Personal API key | C | C | C | C | |
| OAuth2 for multi-user apps | C | C | **G** | **G** | |
| Webhooks / realtime push | C | C | **G** | **G** | Refresh/poll only |
| File upload | C | C | **G** | **G** | Auth’d image hosting noted in API docs |
| Rate-limit / complexity handling | C | C | **P** | **P** | Basic HTTP client; no explicit backoff UX |
| Linear Agent sessions/skills | C | C | **G** | **G** | Distinct from local CLI agent |
| Local CLI agent on issue | — | — | C | — | TUI differentiator |

---

## Priority recommendations

Ordered for value vs invasiveness for an issue-first TUI (not aiming for full Linear parity).

### P0 — Close obvious issue CRUD holes (API mostly ready)

1. **Set / clear priority** command (client already updates `priority`).
2. **Edit description** modal (client already updates `description`).
3. **Unarchive issue** command (client already implements `UnarchiveIssue`).
4. **Move issue to project** (extend `UpdateIssueInput` with `ProjectID`, then picker).

### P1 — Collaboration depth

1. Comment edit/delete (and optionally resolve threads).
2. Reactions on comments.
3. Create/link attachment URL (`attachmentLinkURL` / `attachmentCreate`).
4. Issue activity/history read-only section in details.

### P2 — Planning surfaces users expect after issues

1. Filter-by-milestone command (client filter already present).
2. Project detail pane (status, target dates, progress) — read-only first.
3. Project updates: list + create markdown update.
4. Initiatives: browse + list member projects (read-only first).

### P3 — Intake & personal workspace

1. Triage navigation node + accept/decline/snooze-style actions.
2. Notifications list (mark read / archive / snooze).
3. Load Linear issue templates into create-issue flow.
4. Persist/load custom views from API (or export rich filters as views).

### P4 — Platform / enterprise (opt-in slices)

1. Customers & customer needs on issues.
2. Releases on issues + filter by release.
3. SLA indicators + filter.
4. OAuth + webhooks for multi-user / near-realtime sync.
5. Linear Agent session integration (vs local CLI agents).

---

## API surface size vs client surface

Linear’s public API is intentionally the same GraphQL used by Linear’s own apps. The SDK document catalog exposes on the order of **hundreds** of query/mutation operations across domains (issues, projects, initiatives, documents, notifications, customers, releases, agents, templates, views, webhooks, etc.).

This repository’s client currently concentrates on roughly:

- **~20** public client methods focused on teams, projects, milestones, cycles, users, states, labels, issues, comments, relations, subscriptions, archive/unarchive.

That is appropriate for an issue TUI MVP+, but means **most Linear domains have zero client bindings**, so TUI gaps in those domains are blocked on API work first.

---

## Intentional non-goals (suggested)

Unless product direction changes, treat these as **acceptable permanent gaps** for a terminal client:

- Full board / timeline / Insights charting
- Pixel-parity sidebar IA and display options
- Admin settings for workflow design, SLA rule builders, SCIM/SAML
- Slack/GitHub integration configuration UIs (consume attachment links instead)

Keep investing where the TUI is already strong: **fast keyboard issue ops**, **filters**, and **local agent loops**.

---

## Suggested next doc / tracking

- Track implementation slices against the P0–P4 list above.
- When closing gaps, update this report’s matrix cells rather than resurrecting `docs/linear-feature-gap-audit.md`.
- Optional: add `.docs/<slice>.md` plans per RIPER-5 before EXECUTE on each P0 item.

---

## References

- [Linear Concepts](https://linear.app/docs/conceptual-model)
- [Projects](https://linear.app/docs/projects) · [Initiatives](https://linear.app/docs/initiatives) · [Triage](https://linear.app/docs/triage)
- [Asks](https://linear.app/docs/linear-asks) · [SLAs](https://linear.app/docs/sla) · [Releases](https://linear.app/docs/releases) · [Insights](https://linear.app/docs/insights)
- [API & Webhooks](https://linear.app/docs/api-and-webhooks)
- [GraphQL getting started](https://linear.app/developers/graphql) · [Pagination](https://linear.app/developers/pagination) · [Webhooks](https://linear.app/developers/webhooks)
- [Apollo schema explorer](https://studio.apollographql.com/public/Linear-API/variant/current/home)
- Code: `internal/tui/commands.go`, `internal/tui/planning_collaboration_actions.go`, `internal/linearapi/client.go`
