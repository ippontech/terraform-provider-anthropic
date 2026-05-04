---
name: ship
description: >
  Creates a branch named from the work done, commits all changes with a
  detailed conventional-commit message, pushes to GitHub, and opens a PR
  (or updates the existing PR description if one already exists) whose
  title and body come directly from the commit message.
model: sonnet
---

# ship

Turn local changes into a PR-ready branch in four steps: branch → commit → push → PR.

## Trigger

- Explicitly when the user says "ship", "push this", "create a PR", or similar
- When significant work is done on main or a scratch branch and needs to be wrapped into a PR

## Workflow

### Step 1 — Understand the changes

Run in a **single parallel message**:

```bash
git status
git diff HEAD
git log main..HEAD --oneline
```

Read any changed files you haven't already seen in this session. The goal is to understand *what* changed and *why* well enough to write an accurate commit message.

### Step 2 — Create a branch

If already on a non-main/non-master branch, skip this step.

Derive a branch name from the work:
- Format: `<type>/<short-description>` (e.g. `fix/environment-updated-at-plan-modifier`)
- All lowercase, kebab-case, under 50 characters
- Use conventional commit types: `feat`, `fix`, `refactor`, `docs`, `test`, `ci`, `chore`

```bash
git checkout -b <branch-name>
```

### Step 3 — Commit

Stage all relevant changed files (be explicit — avoid `git add -A` if untracked files might be sensitive) and create a commit with a conventional commit message:

- **Title** (line 1): `<type>(<optional scope>): <imperative summary>` — 72 characters max
- **Blank line**
- **Body**: explain *why* the change was made and what problem it solves; use bullets for multiple changes; 80-character line wrap

```bash
git add <files>
git commit -m "$(cat <<'EOF'
<type>(<scope>): <summary>

<body>

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
EOF
)"
```

### Step 4 — Push and open or update the PR

Push the branch:

```bash
git push -u origin <branch-name>
```

Then check whether an open PR already exists for this branch:

```bash
gh pr list --head "$(git rev-parse --abbrev-ref HEAD)" --state open --json number,url \
  --jq 'if length > 0 then "\(.[0].number) \(.[0].url)" else "" end'
```

**If no open PR exists** — write the body to a temp file, then create the PR. The title must be the commit title (first line, without the `Co-Authored-By` trailer). The body must be the commit body. **Write each bullet as a single unbroken line — do not hard-wrap at 80 chars, as continuation lines render as visible line breaks on GitHub.**

```bash
cat > /tmp/pr_body.txt << 'ENDBODY'
<commit body>

🤖 Generated with [Claude Code](https://claude.com/claude-code)
ENDBODY
gh pr create \
  --title "<commit title>" \
  --body-file /tmp/pr_body.txt
```

**If an open PR already exists** — synthesise an updated description from all commits on this branch since main, then update the PR via the GitHub API (`gh pr edit` can fail with a Projects classic deprecation error). **Write each bullet as a single unbroken line — do not hard-wrap at 80 chars.**

```bash
git log main..HEAD --format="%s%n%n%b"   # all commit titles + bodies

cat > /tmp/pr_body.txt << 'ENDBODY'
<consolidated summary of all changes>

🤖 Generated with [Claude Code](https://claude.com/claude-code)
ENDBODY
gh api repos/{owner}/{repo}/pulls/<number> \
  --method PATCH \
  --raw-field title="<title reflecting all commits>" \
  --raw-field body="$(cat /tmp/pr_body.txt)" \
  --jq '.html_url'
```

Report the PR URL to the user.

Then write the sentinel file to suppress the stop hook (which would otherwise ask you to run `/update-pr-description` on a PR whose description is already correct):

```bash
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null)
touch "/tmp/claude-pr-desc-done-$(echo "$branch" | tr '/' '-')"
```
