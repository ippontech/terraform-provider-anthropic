---
name: ship
description: >
  Commits the current changes on a branch with a conventional-commit message
  and publishes them as a GitHub pull request, or adds them to the branch's
  open pull request. Use when the user says "ship", "push this", "create a
  PR" or "open a pull request", or wants finished work on main or a scratch
  branch turned into a PR. Not for rewriting the description of an existing
  PR (update-pr-description).
model: sonnet
---

# ship

Turn local changes into a pull request on `ippontech/terraform-provider-anthropic`. Invoking this skill is the user's request to publish, so pushing and opening the PR need no further confirmation, with two exceptions: ask when the push remote is ambiguous (step 1), and leave any rewrite of an existing PR description to `update-pr-description`, which asks before publishing.

## Step 1: Find the remotes

Shell variables do not survive between commands, so note the values found here (`<base-remote>`, `<push-remote>`, `<head-owner>`) and write them into the later commands. `<branch>` is the branch that gets pushed: the current branch, or, when that is `main`, the branch Step 3 creates.

The base remote is the one that points to the upstream repository (`origin` in a standard clone):

```bash
git branch --show-current
git remote -v | awk '/ippontech\/terraform-provider-anthropic(\.git)? \(fetch\)/ {print $1; exit}'
git fetch <base-remote> main
```

The push remote is the first of these that gives an answer:
1. `git config --get branch.<branch>.pushRemote` (skip it on `main`: the branch Step 3 creates has none yet), then `git config --get remote.pushDefault`;
2. the base remote, when `gh api repos/ippontech/terraform-provider-anthropic --jq .permissions.push` prints `true`;
3. your fork: the remote whose URL ends with `<login>/terraform-provider-anthropic` or `<login>/terraform-provider-anthropic.git`, where `<login>` is `gh api user --jq .login`. Match the whole repository name: a remote of another repository whose name merely starts the same way is not the fork.

```bash
git remote -v | awk -v l="<login>" '$3 == "(push)" && $2 ~ ("[/:]" l "/terraform-provider-anthropic(\\.git)?$") {print $1}'
```

If none answers, or several remotes match, show `git remote -v` and ask the user which remote to push to, because a wrong guess either fails or publishes to an unexpected repository. Contributors without write access to the upstream repository push to their fork.

`<head-owner>` is the owner in the push remote's URL: `ippontech` for the base remote, `<login>` for your fork.

## Step 2: Understand the changes

Run in a single parallel message:

```bash
git status
git diff HEAD
git log <base-remote>/main..HEAD --oneline
```

Read any changed file you have not seen in this session, so the commit message says accurately what changed and why.

## Step 3: Create a branch

Skip this step when already on a branch other than `main`. Otherwise derive the name from the work: `<type>/<short-description>` in lowercase kebab-case, under 50 characters, with a conventional-commit type (`feat`, `fix`, `refactor`, `docs`, `test`, `ci`, `chore`), for example `fix/environment-updated-at-plan-modifier`.

```bash
git switch -c <branch-name>
```

From here on, `<branch>` is `<branch-name>`.

## Step 4: Run the checks

`CLAUDE.md` requires both before a commit:

```bash
make               # fmt, tidy-check, lint, unit tests, install, docs generation
pre-commit run -a  # repository hooks
```

- Both can modify files (formatting, regenerated `docs/`): review those changes and include them in the commit.
- When `pre-commit` changed an example or a template (`terraform_fmt`, `trailing-whitespace`, `end-of-file-fixer`), run `make generate` again: `docs/` embeds them as `make` generated it, and the CI `generate` job fails on stale docs.
- `make` can be skipped when no Go, Terraform, template or example file changed (a change limited to `.claude/` or Markdown, for example); say so in the report.
- If a check still fails after fixing what belongs to the change, stop and report the failing command with its error instead of committing, because a red branch costs the reviewer a round trip.

## Step 5: Commit

Stage the files explicitly (`git add <paths>`, not `git add -A`, so untracked local files are never published) and commit with a conventional-commit message:
- title: `<type>(<optional scope>): <imperative summary>`, 72 characters at most;
- blank line, then a body that explains why the change was made and what it solves, with one bullet per change, wrapped at 80 characters;
- end with the trailer lines your session instructions specify, such as an attribution line, if any. Write no model name otherwise, because a hard-coded one goes stale.

```bash
git add <paths>
git commit -F - <<'EOF'
<type>(<scope>): <summary>

<body>
EOF
```

## Step 6: Push and open the PR

Push the branch you are on, never `main`: `git branch --show-current` must print `<branch>`. `--head` matches the branch name in every fork, so keep only the PR whose head is in your push remote's repository.

```bash
git branch --show-current
git push -u <push-remote> <branch>
gh pr list --repo ippontech/terraform-provider-anthropic --head <branch> --state open \
  --json number,url,headRepositoryOwner \
  --jq '.[] | select(.headRepositoryOwner.login == "<head-owner>") | "\(.number) \(.url)"'
```

If no open PR exists, create one. The title is the commit title; the body is the commit body, with each bullet on a single unbroken line because GitHub renders hard line breaks, followed by the PR footer your session instructions specify, if any. `<head>` is `<branch>` when pushing to the base remote, `<head-owner>:<branch>` when pushing to a fork.

```bash
body=$(mktemp)
cat > "$body" <<'ENDBODY'
<commit body>
ENDBODY
gh pr create --repo ippontech/terraform-provider-anthropic --base main \
  --head "<head>" --title "<commit title>" --body-file "$body"
rm -f "$body"
```

`mktemp` gives each run its own file, so two sessions shipping at the same time never overwrite each other's body.

If an open PR already exists, the push has added the commits to it. Leave its description as is, and tell the user that `/update-pr-description` drafts an update for their approval when the new commits change what the PR does.

Report the PR URL. After creating a PR, mark its description as current so the Stop hook does not suggest updating it:

```bash
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null)
touch "/tmp/claude-pr-desc-done-$(echo "$branch" | tr '/' '-')"
```
