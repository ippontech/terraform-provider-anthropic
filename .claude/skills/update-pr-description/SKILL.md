---
name: update-pr-description
description: >
  Updates the GitHub PR description for the current branch based on the work
  done in the current Claude session. Use after any meaningful change to a PR
  (feature, fix, refactor, docs). Invoked automatically when Claude stops if
  an open PR exists for the current branch.
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

Use `mcp__github__pull_request_read` (method: `get`) with the current branch
name to retrieve the PR number and existing description.

If no open PR exists for this branch, stop silently.

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

### Step 4 — Update the PR

Use the GitHub API to update the PR (`gh pr edit` can fail with a Projects
classic deprecation error):

```bash
gh api repos/{owner}/{repo}/pulls/<number> \
  --method PATCH \
  --field body="<new body>" \
  --jq '.html_url'
```

Report the PR URL back to the user.

Then write the sentinel file to suppress the Stop hook for 5 minutes:

```bash
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null)
touch "/tmp/claude-pr-desc-done-$(echo "$branch" | tr '/' '-')"
```
