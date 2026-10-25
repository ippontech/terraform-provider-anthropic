#!/usr/bin/env bash
# Stop hook (see .claude/settings.json): end-of-task reminders for the user.
#
# It never blocks Claude from stopping and never runs or publishes anything on
# its own: it prints at most one systemMessage, shown to the user, when
#   1. Go sources, docs templates or examples changed after docs/ was last
#      generated (run `make`, which includes `make generate`);
#   2. the branch has an open PR and commits newer than the last
#      /update-pr-description or /ship run on this machine;
#   3. the branch changes Go or Terraform files and has commits newer than the
#      last /update-project-knowledge run.
# The skills mark 2 and 3 as done by touching /tmp/claude-pr-desc-done-<branch>
# and /tmp/claude-knowledge-done-<branch>. Any failure (missing git, gh or jq,
# no network) ends silently with exit 0.

set -u

# The hook input (JSON on stdin) is not needed.
cat >/dev/null 2>&1 || true

command -v jq >/dev/null 2>&1 || exit 0
cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null || exit 0

branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null) || exit 0
case "$branch" in
  "" | HEAD | main) exit 0 ;;
esac
slug=$(printf '%s' "$branch" | tr '/' '-')
last_commit=$(git log -1 --format=%ct 2>/dev/null) || exit 0

# Modification time in seconds since the epoch (GNU stat, then BSD stat).
mtime() {
  stat -c %Y "$1" 2>/dev/null || stat -f %m "$1" 2>/dev/null || echo 0
}

# True when the sentinel is missing or older than the last commit.
older_than_last_commit() {
  [ ! -f "$1" ] || [ "$(mtime "$1")" -lt "$last_commit" ]
}

reminders=()

# 1. Docs generation: an uncommitted change to a non-test Go file, a template
#    or an example that is newer than docs/index.md (make generate rewrites
#    every file under docs/).
docs_stale=false
while IFS= read -r line; do
  path=${line:3}
  path=${path##* -> }
  case "$path" in
    *_test.go) continue ;;
  esac
  if [ ! -f docs/index.md ] || { [ -f "$path" ] && [ "$path" -nt docs/index.md ]; }; then
    docs_stale=true
    break
  fi
done < <(git status --porcelain --untracked-files=all -- '*.go' templates examples 2>/dev/null)
if [ "$docs_stale" = true ]; then
  reminders+=("Go sources, templates or examples changed after docs/ was last generated: run \`make\` (it includes \`make generate\`) before committing.")
fi

# 2. PR description: only query GitHub when the sentinel is older than the
#    last commit, so a stop without new commits costs no network call.
if older_than_last_commit "/tmp/claude-pr-desc-done-$slug" && command -v gh >/dev/null 2>&1; then
  pr=$(gh pr view "$branch" --json number,state --jq 'select(.state == "OPEN") | .number' 2>/dev/null || true)
  if [ -n "$pr" ]; then
    reminders+=("PR #$pr on $branch has commits its description may not cover yet: run /update-pr-description to draft an update (nothing is published without your approval).")
  fi
fi

# 3. Project knowledge: the branch touches Go or Terraform files.
if older_than_last_commit "/tmp/claude-knowledge-done-$slug" &&
  git rev-parse --verify -q origin/main >/dev/null &&
  git diff --name-only origin/main...HEAD 2>/dev/null | grep -qE '\.(go|tf)$'; then
  reminders+=("Code changed on $branch: run /update-project-knowledge if this work showed a convention or a trap worth recording.")
fi

if [ "${#reminders[@]}" -gt 0 ]; then
  message=$(printf '%s\n' "${reminders[@]}")
  jq -n --arg m "$message" '{systemMessage: $m}'
fi
exit 0
