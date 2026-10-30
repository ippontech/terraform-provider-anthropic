---
name: api-planning
description: >
  Plans the implementation of an Anthropic API (for example /v1/skills or
  /v1/environments) in this Terraform provider as one issue per resource or
  data source, tracked on GitHub or on Vibe Kanban, ready for sequential or
  parallel implementation. Use when the user wants to implement or plan a new
  API or all the endpoints of an API, to implement a range of GitHub issues
  (for example "issues 52 to 57"), or to parallelize several resources or data
  sources. Not for implementing a single resource or data source
  (terraform-provider).
model: opus
---

# api-planning

Turns an Anthropic API into implementation issues for this provider: one issue per Terraform resource or data source, each complete enough for the agent that will implement it. Two variants share the planning and differ only in where the work is tracked and how implementation starts.

## Choose the variant

- **Vibe Kanban**: when the Vibe Kanban MCP tools (`mcp__vibe_kanban__*`) are available in this session and the user works with Vibe Kanban (they say so, or the session runs in a Vibe Kanban workspace). Read `references/vibe-kanban.md` before step 4.
- **GitHub**: in every other case, and by default. Read `references/github.md` before step 4.

When the user names a variant, use it. Both variants create GitHub issues: use the GitHub MCP tools when they are available in the session, otherwise the `gh` commands given in each reference.

## Step 1: Discover the API

Fetch `https://platform.claude.com/docs/en/api/overview` and every endpoint page of the API in one parallel message. For each endpoint, note the method and path, the path and query parameters, the request fields (type, required or optional) and the response fields.

Map the endpoints to Terraform objects:

| Endpoints | Terraform object |
|---|---|
| create, get, delete, no update | resource whose inputs are all `RequiresReplace` |
| create, get, update, delete | resource with `Update` (updates are often `POST /{id}` in this API, not only `PATCH`) |
| an archive endpoint next to delete | an `archive_on_destroy` attribute on the resource (pattern in `CLAUDE.md`), never a separate resource |
| get by ID | single data source |
| list | list data source |

Then check that the pinned SDK (version in `go.mod`) has the service and methods, because the SDK is pinned and a missing method changes the plan:

```bash
grep -rln "<Resource>" "$(go env GOMODCACHE)"/github.com/anthropics/anthropic-sdk-go@<version>/*.go
```

When the SDK lacks the endpoint, say so in the issue rather than planning around it.

## Step 2: Check what exists

```bash
gh issue list --repo ippontech/terraform-provider-anthropic --state all \
  --search "<object name> in:title" --json number,title,state,url
grep -n "<ObjectName>" internal/provider/provider.go
```

Skip an object that is already implemented or has an open issue, and tell the user. When the user names existing issues ("implement issues 52 to 57"), read them with `gh issue view <number>` and use them as the plan; an issue written before the move to `internal/services/` may list `internal/provider/` paths, and `CLAUDE.md` wins over the issue.

## Step 3: Plan the issues and get approval

Write one issue per resource or data source from `references/issue-template.md`. Tests belong to each issue, never to a separate one, because the implementing agent needs them to know when it is done.

Branch names follow the Terraform name without `anthropic_`, in kebab-case: the `anthropic_skill_version` resource gives `feat/skill-version-resource`, the `anthropic_skills` data source gives `feat/skills-data-source`.

Before creating anything, show the plan as a table (title, resource or data source, branch, dependencies between issues) and wait for the user's go-ahead, because issues, branches and workspaces are visible to the whole team and tedious to clean up.

## Step 4: Create and start

Follow the reference of the chosen variant, then report a summary table (issue, URL, branch or workspace) to the user.

## Rules for implementation

**One PR per issue.** Each issue leads to exactly one PR, because reviewers review and revert one logical unit at a time. For example, the `anthropic_skill` resource and the `anthropic_skills` data source are two issues and two PRs, even when one agent writes both on the same day.

**Branch isolation.** During parallel implementation, each agent commits only the files of its own issue, on its own branch and in its own worktree (the `provider-implementer` agent runs in an isolated worktree, and Vibe Kanban gives each workspace one), because a file left in a shared checkout ends up in a sibling's PR. For example, the agent implementing `anthropic_skill_version` leaves `skill_resource.go` alone, even to fix a typo, and reports the typo instead.

## Example

User: "I'd like to implement the /v1/environments APIs".

1. Fetch the overview and the Environments endpoint pages in parallel.
2. Identify the `anthropic_environment` resource (with `archive_on_destroy`, since the API has an archive endpoint), the `anthropic_environment` data source and the `anthropic_environments` data source.
3. Search the existing issues: none found.
4. Show the plan of three issues; the user approves.
5. GitHub variant: create three issues and three branches, then report the table and how to start the implementation.
