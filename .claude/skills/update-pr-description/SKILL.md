---
name: update-pr-description
description: >
  Updates the GitHub PR description for the current branch based on the work
  done in the current Claude session. Use after any meaningful change to a PR
  (feature, fix, refactor, docs). Invoked automatically when Claude stops if
  an open PR exists for the current branch. Also works inside Vibe Kanban
  worktrees (vk/* branches) by resolving the PR via the VK issue context.
model: sonnet
---

# update-pr-description

Use this skill to rewrite the PR description so it accurately reflects what
was implemented during this session.

## Trigger

- Automatically at the end of any session where an open PR exists for the
  current branch
- Explicitly when the user says "update the PR description" or similar

## Workflow

### Step 1 — Gather context

Run in a **single parallel message**:

```bash
git rev-parse --abbrev-ref HEAD      # current branch
git log main..HEAD --oneline         # commits on this branch
git diff main...HEAD --stat          # files changed vs main
```

### Step 2 — Find the open PR

#### Normal branch

```bash
gh pr list --head "$(git rev-parse --abbrev-ref HEAD)" --state open --json number,url \
  --jq 'if length > 0 then "number=\(.[0].number) url=\(.[0].url)" else "" end'
```

If an open PR exists, proceed to Step 3.

#### Vibe Kanban worktree (branch starts with `vk/`)

Vibe Kanban sessions run in worktrees on `vk/<id>-<name>` branches that have
no direct PR. In this case:

1. Call `mcp__vibe_kanban__get_context` to get the current workspace/issue.
2. From the returned issue body, extract all branch names referenced (look for
   lines matching `feat/...`, `fix/...`, `chore/...`, or a `## Branch` section).
3. For each extracted branch, call `mcp__github__pull_request_read` (method:
   `get`) to check for an open PR.
4. Collect all open PRs found and run Steps 3–4 for **each one** — write a
   separate description per PR reflecting what changed in that PR specifically.

If `get_context` is unavailable or returns no useful issue, stop silently.

#### Fallback

If no open PR is found by any method above, stop silently.

### Step 3 — Write the updated description

Synthesise the new body from two sources:
1. **Session context** — what was actually discussed and changed during this
   Claude session (the primary source of truth)
2. **Git context** — commit messages and changed files for completeness

Use this structure:

```markdown
## Summary

<1-3 bullet points: what changed and why — lead with the why>

## What changed

<Optional: additional detail if the summary bullets are not sufficient.
 Omit this section if the summary covers everything.>

🤖 Generated with [Claude Code](https://claude.com/claude-code)
```

Guidelines:
- Preserve any existing "Closes #N" or issue references from the old description
- One bullet per logical change — do not list individual files
- Do not invent claims about behaviour you have not observed in this session
- Keep it concise: a reviewer should understand the PR in under 30 seconds
- **Write each bullet as a single unbroken line — do not hard-wrap at 80 chars**, as continuation lines render as visible line breaks on GitHub

### Step 4 — Update the PR

Use the GitHub API to update the PR (`gh pr edit` can fail with a Projects
classic deprecation error):

```bash
cat > /tmp/pr_body.txt << 'ENDBODY'
<new body>
ENDBODY
gh api repos/{owner}/{repo}/pulls/<number> \
  --method PATCH \
  --raw-field body="$(cat /tmp/pr_body.txt)" \
  --jq '.html_url'
```

Report the PR URL back to the user.

Then write the sentinel file to suppress the Stop hook for 5 minutes:

```bash
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null)
touch "/tmp/claude-pr-desc-done-$(echo "$branch" | tr '/' '-')"
```
