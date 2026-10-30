# Vibe Kanban variant

Read at step 4 of `SKILL.md`, after the user approved the plan. The work is tracked as Vibe Kanban issues linked to GitHub issues, and each implementation issue runs in its own Vibe Kanban workspace.

## Concepts

- **Issues**: Vibe Kanban tasks; this variant creates them.
- **Workspaces**: isolated git worktrees, one per issue. The human operator creates only the orchestration workspace by hand; the orchestration agent creates every implementation workspace.
- **Orchestration issue**: exactly one per batch. It monitors the parallel work and produces no code; every implementation issue is `blocking` it, so it stays open until all the work is merged.

## Rules

- **All issues are top-level**: never set `parent_issue_id`; the batch is tied together by the `blocking` relationships to the orchestration issue, not by nesting.
- **One orchestration issue per batch**, with no branch, PR or commit.
- **`start_workspace` always uses `"branch": "main"`**, because Vibe Kanban creates its own `vk/` branch; a `feat/` or other branch name that does not exist returns a 400 and leaves the worktree uninitialised, so every later `run_session_prompt` on it fails.
- **One GitHub issue per implementation issue**, its URL written back into the Vibe Kanban description.
- **No Vibe Kanban IDs outside Vibe Kanban**: commits, PR titles and PR bodies reference the GitHub issue (`#N`), because a Vibe Kanban ID means nothing to a reviewer.

## Steps

1. **Create the Vibe Kanban issues.** One `mcp__vibe_kanban__create_issue` per resource or data source plus the orchestration issue, in one parallel message. Priorities: orchestration `high`, resources `high`, data sources `medium`. Implementation issue body: the issue template of `references/issue-template.md` without its *Branch* section. Orchestration issue body:

   ```markdown
   Track parallel implementation of <API name> resources and data sources.

   ## Workspaces to monitor
   - [ ] <Issue title A>
   - [ ] <Issue title B>

   ## Agent instructions
   Write no code. Your job:
   1. Create a workspace for each implementation issue above (the human has only created yours).
   2. Monitor sibling workspaces; report progress and flag inconsistencies.
   3. Close this issue once all sibling PRs are merged.
   ```

2. **Link the issues.** `mcp__vibe_kanban__create_issue_relationship` for every pair, in one parallel message: each implementation issue `blocking` the orchestration issue; a resource `related` to its data sources; a single data source `related` to its list data source.

3. **Create the GitHub issues**, for implementation issues only and only those that do not exist yet: the GitHub MCP tools when available, otherwise `gh issue create --repo ippontech/terraform-provider-anthropic --title "<title>" --body-file <file>`, in one parallel message. Create no `feat/` branch here: `/ship` creates and pushes the branch when the workspace agent opens its PR.

4. **Cross-link.** Update every Vibe Kanban issue in one parallel message: prepend `GitHub Issue: <url>` to each implementation issue, and the list of all GitHub issue URLs to the orchestration issue.

5. **Create the implementation workspaces.** `mcp__vibe_kanban__start_workspace` for every implementation issue, in one parallel message, then `mcp__vibe_kanban__list_sessions` for each workspace to get its session ID:

   ```
   mcp__vibe_kanban__start_workspace({
     name: "<resource-or-datasource-name>",
     executor: "CLAUDE_CODE",
     issue_id: "<vk-issue-id>",
     repositories: [{"repo_id": "<repo-id>", "branch": "main"}]
   })
   ```

   If a `start_workspace` call returns an error, send no prompt to that workspace: delete it and create it again first.

6. **Start the implementation.** `mcp__vibe_kanban__run_session_prompt` for every session, in one parallel message. Each prompt invokes `/terraform-provider` and then gives the full spec: the implementation issue body with its GitHub issue URL.

7. **Report** the table of issues, GitHub issue URLs and workspace IDs, and tell the user: "Each agent opens a draft PR with `/ship` when it is ready."

## Example

Plan approved for `anthropic_environment` (resource), `anthropic_environment` and `anthropic_environments` (data sources):
1. Create four Vibe Kanban issues in parallel: one orchestration and three implementation issues.
2. Create five relationships in parallel: three implementation issues `blocking` the orchestration issue, the resource `related` to the single data source, the single data source `related` to the list data source.
3. Create three GitHub issues in parallel (implementation only, no branch).
4. Update the four Vibe Kanban issues with the GitHub links in parallel.
5. Start three workspaces in parallel and retrieve their session IDs.
6. Send the three implementation prompts in parallel.
7. Report the summary.
