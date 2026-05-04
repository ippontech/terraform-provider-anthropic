---
name: vibe-kanban-brainstorming
description: >
  Plans and scaffolds Anthropic API endpoints as Terraform resources/data sources
  for Vibe Kanban — a multi-agent CLI tool that spawns one Claude Code agent per
  issue, each isolated in its own git worktree, all running in parallel. Use when
  starting work on a new API (e.g. /v1/skills, /v1/environments) to generate
  cross-linked Vibe Kanban issues, GitHub issues, and branches — one per
  resource/data source — ready for agents to implement in parallel.
model: opus
---

# vibe-kanban-brainstorming

## Concepts

- **Issues** — kanban tasks; this skill creates them.
- **Workspaces** — isolated git worktrees, one per issue. The human operator creates only the **orchestration workspace** manually; the orchestration agent creates all implementation workspaces automatically.
- **Orchestration issue** — one required per batch; monitors parallel work on `main`, no code output. Implementation issues are `blocking` it so it stays open until all work is merged.

## Workflow

### Step 1 — Discover the API

Fetch `https://platform.claude.com/docs/en/api/overview` and all relevant endpoint pages in parallel. Collect HTTP method, path, request/response fields.

Endpoint → Terraform mapping:
- `POST` + `GET /{id}` + `DELETE /{id}` → resource (ForceNew)
- `POST` + `GET /{id}` + `PATCH /{id}` + `DELETE /{id}` → resource (with Update)
- `GET /{id}` only → data source (single)
- `GET /` → data source (list)

### Step 2 — Create Vibe Kanban issues

One `mcp__vibe_kanban__create_issue` per resource/data source **plus one orchestration issue**, all top-level (`no parent_issue_id`), in a **single parallel message**.

Priorities: orchestration `high`, resources `high`, data sources `medium`.

**Orchestration issue body:**
```markdown
Track parallel implementation of <API name> resources and data sources.

## Workspaces to monitor
- [ ] <Issue title A> — branch: feat/<name-a>
- [ ] <Issue title B> — branch: feat/<name-b>

## Agent instructions
Do NOT write code. Your job:
1. Create a workspace for each implementation issue above (the human has only created yours).
2. Monitor sibling workspaces; report progress and flag inconsistencies.
3. Close this issue once all sibling PRs are merged.
```

**Implementation issue body:** use the template at the bottom of this file.

### Step 3 — Create issue relationships

Call `mcp__vibe_kanban__create_issue_relationship` for all pairs in a **single parallel message**:
- Every implementation issue → orchestration issue: `blocking`
- Resource ↔ its data source(s): `related`
- Single data source ↔ list data source: `related`

### Step 4 — Create GitHub issues and branches

First check for existing issues to avoid duplicates:
```bash
gh issue list --search "<resource name>" --json number,title,url
```

For each **implementation issue only** (orchestration has no GitHub issue or branch), create via `gh` CLI:
```bash
gh issue create --title "<title>" --body "<body>"
gh api repos/{owner}/{repo}/git/refs -f ref="refs/heads/feat/<name>" -f sha="$(gh api repos/{owner}/{repo}/git/ref/heads/main --jq .object.sha)"
```

Branch naming: `feat/<resource-name>-resource` or `feat/<name>-data-source`.

### Step 5 — Cross-link

Update all Vibe Kanban issues in a **single parallel message**:
- Implementation issues: prepend `GitHub Issue: <url>\nBranch: feat/<name>`
- Orchestration issue: prepend a list of all GitHub issue URLs it tracks.

### Step 6 — Inform the user

Report a summary table. Remind: "Open a draft PR on each branch once the first commit is pushed."

## Invariants

- **One orchestration issue per batch.** No branch, no PR, no commits. Never skip it.
- **One GitHub issue per implementation ticket.** URL written back into the Vibe Kanban description.
- **One PR per ticket.** Never bundle multiple tickets.
- **No Vibe Kanban IDs outside Vibe Kanban** (not in commits, PR titles, or PR bodies — use `#N`).
- **Branch isolation.** Each agent commits only to its own worktree branch.

## Implementation issue template

```markdown
GitHub Issue: <url>

<One-line summary.>

## Branch
`feat/<name>` (open draft PR after first commit)

## API
- `METHOD /v1/<path>` — description

## SDK methods
- `client.<Service>.<Method>(ctx, params)` → `ResponseType`

## Terraform schema
| Attribute | Type | R/W |
|---|---|---|
| `id` | string | Computed |

## Implementation
Follow the `terraform-provider-implementer` agent conventions (files, tests, make, review).
```

## Example

User: "I'd like to implement /v1/environments APIs"

1. Fetch API docs in parallel → identify: `anthropic_environment` resource, `anthropic_environment` data source, `anthropic_environments` data source.
2. Create **4** Kanban issues in parallel: 1 orchestration + 3 implementation.
3. Create **5** relationships in parallel: 3× impl `blocking` orch, resource `related` single DS, single DS `related` list DS.
4. Create **3** GitHub issues + **3** branches in parallel (implementation only).
5. Update **4** Kanban issues with links in parallel.
6. Report summary; remind user to open draft PRs.
