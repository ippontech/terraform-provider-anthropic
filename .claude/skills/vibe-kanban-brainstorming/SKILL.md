---
name: vibe-kanban-brainstorming
description: >
  Automates planning and scaffolding for implementing Anthropic API endpoints
  as Terraform resources and data sources. Use when starting work on a new API
  (e.g. /v1/skills, /v1/environments) to generate fully cross-linked Vibe Kanban
  issues, GitHub issues, and branches — one per resource/data source — ready
  for parallel implementation.
model: sonnet
---

# vibe-kanban-brainstorming

Use this skill when you need to plan and scaffold the implementation of a new
Anthropic API as Terraform resources and data sources in this provider.

## Trigger

The user says something like:
- "I'd like to implement /v1/environments APIs"
- "Plan the work for the Files API"
- "Create issues for the Sessions endpoints"

## Workflow

### Step 1 — Discover the API

Fetch the API overview and all relevant endpoint pages:

```
WebFetch: https://platform.claude.com/docs/en/api/overview
```

Then fetch each individual endpoint page to collect the full schema (request
body, path params, query params, response fields). Run all fetches in parallel.

For each endpoint, identify:
- HTTP method + path
- Required and optional request fields with types
- Response fields with types
- Whether it maps to a **resource** (Create/Read/Delete or CRUD) or a
  **data source** (Read-only lookup or list)

Mapping heuristic:
- `POST` + `GET /{id}` + `DELETE /{id}` → **resource** (ForceNew if no PATCH/PUT)
- `POST` + `GET /{id}` + `PATCH /{id}` + `DELETE /{id}` → **resource** (with Update)
- `GET /{id}` only → **data source** (single lookup)
- `GET /` (list) → **data source** (list)

Also check the Go SDK for existing types:
```bash
find $(go env GOPATH)/pkg/mod/github.com/anthropics/anthropic-sdk-go* -name "beta*.go" | xargs grep -l "<Resource>"
```

### Step 2 — Plan issues

Create one issue per resource/data source. Each issue must include:

1. **API spec** — endpoint, SDK method signatures, request/response fields
2. **Terraform schema** — attribute table with types and R/W annotations
3. **Files to create/modify** — Go source, test file, example, template, native test
4. **Go acceptance tests** — list of `TestAcc*` function names with what they verify
5. **Terraform native test** — `.tftest.hcl` snippet with `parallel = true` and assert blocks

**Do NOT create a separate issue for tests.** Tests are part of every issue.

### Step 3 — Create Vibe Kanban issues

Use `mcp__vibe_kanban__create_issue` for each resource/data source as a
top-level issue (no `parent_issue_id`). Top-level issues are visible on the
kanban board; subtasks are not.

Run all `create_issue` calls in a **single parallel message**.

Priority guidelines:
- Resources: `high`
- Data sources: `medium`

### Step 4 — Create issue relationships

After all Vibe Kanban issues exist, analyze dependencies between them and call
`mcp__vibe_kanban__create_issue_relationship` for each pair. Use these rules:

| Situation | `relationship_type` |
|---|---|
| Issue A must be merged before work on B can start | `blocking` (A → B) |
| Resource and its corresponding data source(s) | `related` |
| Single-item data source and its list data source | `related` |

Run all `create_issue_relationship` calls in a **single parallel message**.

If no dependencies exist between the issues, skip this step.

### Step 5 — Create GitHub issues and branches

For each issue, in a **single parallel message**:
- `mcp__github__issue_write` (method: `create`) — mirror the Kanban description as a GitHub issue
- `mcp__github__create_branch` (from `main`) — one branch per issue

Branch naming convention:
- `anthropic_skill` resource → `feat/skill-resource`
- `anthropic_skills` data source → `feat/skills-data-source`
- `anthropic_skill_version` resource → `feat/skill-version-resource`
- `anthropic_skill_versions` data source → `feat/skill-versions-data-source`

### Step 6 — Cross-link everything

Update each Vibe Kanban issue description to add at the top:

```
GitHub Issue: https://github.com/ippontech/terraform-provider-anthropic/issues/<N>
Branch: feat/<name>
```

Run all `mcp__vibe_kanban__update_issue` calls in a **single parallel message**.

### Step 7 — Note on PRs

GitHub requires at least one commit before a PR can be opened. Always inform the user:

> "Branches are created and ready. Open a draft PR on each branch once the first commit is pushed."

## Implementation guidance

Each subtask description should reference:
- **Skill for implementation**: `ippon-cd-terraform-provider` — follow its conventions
  for schema definition, CRUD methods, ForceNew attributes, nested objects, examples,
  templates (with non-empty `subcategory`), and Terraform native tests.
- **Subagent for review**: after implementation is complete on a branch, launch the
  `terraform-provider-code-reviewer` subagent pointing at the changed files.

## Parallelization rules

| Step | Strategy |
|---|---|
| Fetch API docs pages | One `WebFetch` per page in a single message |
| Create Vibe Kanban issues | One `create_issue` per issue in a single message |
| Create issue relationships | One `create_issue_relationship` per pair in a single message |
| Create GitHub issues + branches | All `issue_write` + `create_branch` calls in a single message |
| Update Kanban issues with links | One `update_issue` per issue in a single message |
| Implementation | All issues are independent — can be parallelized across agents |

## Subtask description template

```markdown
GitHub Issue: <url>

<One-line summary of what this implements.>

## Branch
`<branch-name>` (open PR once first commit is pushed)

## API
- `METHOD /v1/<path>` — description

## SDK methods (anthropic-sdk-go)
- `client.Beta.<Service>.<Method>(ctx, params)` → `ResponseType`

## Terraform schema
| Attribute | Type | R/W |
|---|---|---|
| `id` | string | Computed |
| `...` | ... | ... |

## Files to create / modify
- `internal/provider/<name>_resource.go`
- `internal/provider/<name>_resource_test.go` — Go acceptance tests
- `internal/provider/provider.go` — register factory function
- `examples/resources/<name>/resource.tf` — expose relevant outputs
- `templates/resources/<name>.md.tmpl` — subcategory: "<Category>"
- `tests/<name>.tftest.hcl` — Terraform native test

## Go acceptance tests
- `TestAcc<Name>Resource_basic` — create with minimal config, check `id` is set
- `testAccCheck<Name>Destroyed` — destroy checker
- ImportState round-trip step

## Terraform native test (`tests/<name>.tftest.hcl`)
```hcl
run "<name>_resource_creates_<name>" {
  parallel = true
  module { source = "./examples/resources/<name>" }
  assert {
    condition     = output.<name>_id != ""
    error_message = "Expected <name>_id to be non-empty."
  }
}
```

Run `make` after implementation, then `make terraform-test`.
```

## Example invocation

User: "I'd like to implement /v1/environments APIs"

1. Fetch `https://platform.claude.com/docs/en/api/overview` + all Environments endpoint pages in parallel
2. Identify: `anthropic_environment` resource + `anthropic_environment` data source + `anthropic_environments` data source
3. Create 3 Kanban issues in parallel (all `create_issue` in one message, no `parent_issue_id`)
4. Create relationships in parallel: resource `related` to single data source, single data source `related` to list data source
5. Create 3 GitHub issues + 3 branches in parallel (all in one message)
6. Update 3 Kanban issues with links in parallel (all `update_issue` in one message)
7. Report summary table to the user and remind them to open draft PRs after first commits
