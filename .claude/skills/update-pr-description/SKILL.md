---
name: update-pr-description
description: >
  Drafts an updated description for the open GitHub pull request of the
  current branch from the work done in the session, and publishes it only
  after the user approves the draft. Use when the user asks to update, rewrite
  or refresh a PR description, after a meaningful change (feature, fix,
  refactor, docs) to a branch that has an open PR, or when the end-of-task
  reminder suggests it, including in Vibe Kanban worktrees on vk/* branches.
  Not for opening a new PR (ship).
model: sonnet
---

# update-pr-description

A PR description is public and may have been written by a person, so this skill always shows a draft and changes nothing on GitHub until the user approves it.

## Step 1: Find the open PR

On a normal branch:

```bash
gh pr list --repo ippontech/terraform-provider-anthropic --head "$(git branch --show-current)" \
  --state open --json number,url,headRepositoryOwner \
  --jq '.[] | "number=\(.number) owner=\(.headRepositoryOwner.login) url=\(.url)"'
```

`--head` matches the branch name in every fork: keep only the PR whose owner is the owner of the repository the branch is pushed to (`ippontech`, or the user's fork). If more than one remains, ask the user which one to update.

In a Vibe Kanban worktree (branch `vk/<id>-<name>`), the branch has no PR of its own:
1. Read the current issue with `mcp__vibe_kanban__get_context` when the Vibe Kanban tools are available.
2. Collect the branch names the issue body references (lines such as `feat/...`, `fix/...`, `chore/...`, or a `## Branch` section).
3. For each branch, look for an open PR with the GitHub MCP tools when they are available, otherwise with the `gh pr list` command above and `--head <branch>`.
4. Run steps 2 to 5 for each open PR found, one draft per PR describing what changed in that PR.

For example, an issue body with `## Branch` followed by `feat/skill-version-resource` leads to `gh pr list --repo ippontech/terraform-provider-anthropic --head feat/skill-version-resource --state open`.

If no open PR is found, tell the user in one line and stop.

## Step 2: Gather context

```bash
gh pr view <number> --repo ippontech/terraform-provider-anthropic --json title,body,commits,files
```

The session is the main source: what was discussed, changed and verified. The commits and files complete it; the current body shows what a person already wrote.

## Step 3: Write the draft

Keep from the current body what the session does not contradict: issue references (`Closes #N`), checklists, and notes a person wrote. Add or rewrite only what the new work changes, because replacing a reviewer-facing description with a session summary loses context the author put there on purpose.

```markdown
## Summary

<1-3 bullets: what changed and why, the why first>

## What changed

<Optional detail when the summary is not enough; omit otherwise.>
```

- One bullet per logical change, not one per file.
- Only claims about behaviour observed in this session or stated in the commits.
- Short enough for a reviewer to understand the PR in under 30 seconds.
- Each bullet on a single unbroken line, because GitHub renders hard line breaks.
- End with the PR footer your session instructions specify, if any.

## Step 4: Ask for approval

Show the user the full draft and, when the PR already had a body, what it adds, removes and keeps. Ask whether to publish it as is, edit it, or leave the PR unchanged, and wait for the answer. Only an explicit go-ahead leads to step 5.

Whatever the answer, mark the description as handled, so the end-of-task reminder stays quiet until the next commit:

```bash
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null)
touch "/tmp/claude-pr-desc-done-$(echo "$branch" | tr '/' '-')"
```

## Step 5: Publish the approved draft

Use the GitHub API, because `gh pr edit` can fail with a Projects (classic) deprecation error. `mktemp` gives each run its own file, so parallel sessions never overwrite each other's draft.

```bash
body=$(mktemp)
cat > "$body" <<'ENDBODY'
<approved body>
ENDBODY
gh api repos/ippontech/terraform-provider-anthropic/pulls/<number> \
  --method PATCH \
  --raw-field body="$(cat "$body")" \
  --jq '.html_url'
rm -f "$body"
```

Report the PR URL to the user.
