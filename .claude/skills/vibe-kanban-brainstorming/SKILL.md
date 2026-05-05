---
name: vibe-kanban-brainstorming
description: >
  Plans and scaffolds Anthropic API endpoints as Terraform resources/data sources
  for Vibe Kanban. TRIGGER when: user says "implement GitHub issues", references
  a range of GitHub issues to implement (e.g. "issues 52 to 57"), asks to
  parallelize implementation of multiple resources/data sources, or says
  "implement all [API name] APIs". Creates Vibe Kanban issues, workspaces, and
  fires parallel agents — one per resource/data source.
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

One `mcp__vibe_kanban__create_issue` per resource/data source **plus one orchestration issue**, all in a **single parallel message**.

**All issues must be top-level: never set `parent_issue_id`.**

Priorities: orchestration `high`, resources `high`, data sources `medium`.

**Orchestration issue body:**
```markdown
Track parallel implementation of <API name> resources and data sources.

## Workspaces to monitor
- [ ] <Issue title A>
- [ ] <Issue title B>

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

### Step 4 — Create GitHub issues

First check for existing issues to avoid duplicates:
```bash
gh issue list --search "<resource name>" --json number,title,url
```

For each **implementation issue only** (orchestration has no GitHub issue), create via `gh` CLI in a **single parallel message**:
```bash
gh issue create --title "<title>" --body "<body>"
```

Do **not** create `feat/` branches here — the agent's `/ship` skill creates the branch automatically when it opens a PR.

### Step 5 — Cross-link

Update all Vibe Kanban issues in a **single parallel message**:
- Implementation issues: prepend `GitHub Issue: <url>`
- Orchestration issue: prepend a list of all GitHub issue URLs it tracks.

### Step 6 — Create implementation workspaces

Call `mcp__vibe_kanban__start_workspace` for every **implementation** issue in a **single parallel message**. Always pass `"branch": "main"` — vibe-kanban auto-generates a `vk/` branch internally.

```
mcp__vibe_kanban__start_workspace({
  name: "<resource-or-datasource-name>",
  executor: "CLAUDE_CODE",
  issue_id: "<vk-issue-id>",
  repositories: [{"repo_id": "<repo-id>", "branch": "main"}]
})
```

After all calls return a `workspace_id`, call `mcp__vibe_kanban__list_sessions` for each workspace to retrieve the session ID.

If any `start_workspace` call returns an error: do **not** call `run_session_prompt` for that workspace — delete it and recreate it before proceeding.

### Step 7 — Fire implementation prompts

Call `mcp__vibe_kanban__run_session_prompt` for all sessions in a **single parallel message**. Each prompt must include the `/terraform-provider` skill invocation followed by the full spec (API endpoints, SDK methods, Terraform schema, files to create).

### Step 8 — Inform the user

Report a summary table of issues, GitHub issue URLs, and workspace IDs. Remind: "Each agent will open a draft PR via `/ship` when it's ready."

## Invariants

- **All issues are top-level.** Never set `parent_issue_id` on any issue.
- **One orchestration issue per batch.** No branch, no PR, no commits. Never skip it.
- **`start_workspace` always uses `branch: "main"`.** Never pass a `feat/` or custom branch name — vibe-kanban generates its own `vk/` branch. Passing a non-existent branch causes a 400 error and leaves the worktree uninitialised, making subsequent `run_session_prompt` calls fail.
- **One GitHub issue per implementation ticket.** URL written back into the Vibe Kanban description.
- **One PR per ticket.** Never bundle multiple tickets. The agent's `/ship` skill creates the branch and PR.
- **No Vibe Kanban IDs outside Vibe Kanban** (not in commits, PR titles, or PR bodies — use `#N`).
- **Branch isolation.** Each agent commits only to its own worktree branch.

## Implementation issue template

```markdown
GitHub Issue: <url>

<One-line summary.>

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
4. Create **3** GitHub issues in parallel (implementation only; no branches).
5. Update **4** Kanban issues with GitHub links in parallel.
6. Start **3** workspaces in parallel; retrieve session IDs.
7. Fire **3** implementation prompts in parallel.
8. Report summary.
