#!/usr/bin/env bash
# Stop hook (see .claude/settings.json): end-of-task reminders for the user.
#
# It never blocks Claude from stopping and never runs or publishes anything on
# its own: it prints at most one systemMessage, shown to the user, when
#   1. Go sources, docs templates or examples changed or were deleted after
#      docs/ was last generated (run `make`, which includes `make generate`),
#      on any branch, main and a detached HEAD included;
#   2. the branch has an open PR and commits newer than the last
#      /update-pr-description or /ship run on this machine;
#   3. the branch changes Go or Terraform files and has a commit of its own
#      touching them newer than the last /update-project-knowledge run
#      (committing the CLAUDE.md edits that skill writes, or merging main,
#      does not bring the reminder back).
# The skills mark 2 and 3 as done by touching /tmp/claude-pr-desc-done-<branch>
# and /tmp/claude-knowledge-done-<branch>. Any failure (missing git, gh or jq,
# no network) ends silently with exit 0.

set -u

# The hook input (JSON on stdin) is not needed.
cat >/dev/null 2>&1 || true

command -v jq >/dev/null 2>&1 || exit 0
cd "${CLAUDE_PROJECT_DIR:-.}" 2>/dev/null || exit 0

reminders=()

# Prints the reminders gathered so far, if any, and ends the hook.
finish() {
  if [ "${#reminders[@]}" -gt 0 ]; then
    message=$(printf '%s\n' "${reminders[@]}")
    jq -n --arg m "$message" '{systemMessage: $m}'
  fi
  exit 0
}

# 1. Docs generation: an uncommitted change to a non-test Go file, a template
#    or an example that is newer than docs/index.md (make generate rewrites
#    every file under docs/). A deleted file has no time of its own: the
#    nearest directory still there changed when the entry went away.
docs_stale=false
while IFS= read -r line; do
  path=${line:3}
  path=${path##* -> }
  case "$path" in
    *_test.go) continue ;;
  esac
  while [ ! -e "$path" ]; do path=$(dirname "$path"); done
  if [ ! -f docs/index.md ] || [ "$path" -nt docs/index.md ]; then
    docs_stale=true
    break
  fi
done < <(git status --porcelain --untracked-files=all -- '*.go' templates examples 2>/dev/null)
if [ "$docs_stale" = true ]; then
  reminders+=("Go sources, templates or examples changed after docs/ was last generated: run \`make\` (it includes \`make generate\`) before committing.")
fi

# 2 and 3 are about a work branch: none on main or a detached HEAD.
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null) || finish
case "$branch" in
  "" | HEAD | main) finish ;;
esac
slug=$(printf '%s' "$branch" | tr '/' '-')
last_commit=$(git log -1 --format=%ct 2>/dev/null) || finish
# The branch's own last commit touching Go or Terraform files: not the main
# commits a merge brings in, nor the merge itself. Empty without origin/main.
last_code_commit=$(git log -1 --format=%ct --no-merges origin/main..HEAD -- '*.go' '*.tf' 2>/dev/null || true)

# Modification time in seconds since the epoch (GNU stat, then BSD stat).
mtime() {
  stat -c %Y "$1" 2>/dev/null || stat -f %m "$1" 2>/dev/null || echo 0
}

# True when the sentinel ($1) is missing or older than the commit time $2.
older_than() {
  [ ! -f "$1" ] || [ "$(mtime "$1")" -lt "${2:-0}" ]
}

# 2. PR description: only query GitHub when the sentinel is older than the
#    last commit, so a stop without new commits costs no network call.
if older_than "/tmp/claude-pr-desc-done-$slug" "$last_commit" && command -v gh >/dev/null 2>&1; then
  pr=$(gh pr view "$branch" --json number,state --jq 'select(.state == "OPEN") | .number' 2>/dev/null || true)
  if [ -n "$pr" ]; then
    reminders+=("PR #$pr on $branch has commits its description may not cover yet: run /update-pr-description to draft an update (nothing is published without your approval).")
  fi
fi

# 3. Project knowledge: the branch touches Go or Terraform files, and a commit
#    touching them is newer than the last run.
if older_than "/tmp/claude-knowledge-done-$slug" "$last_code_commit" &&
  git rev-parse --verify -q origin/main >/dev/null &&
  git diff --name-only origin/main...HEAD 2>/dev/null | grep -qE '\.(go|tf)$'; then
  reminders+=("Code changed on $branch: run /update-project-knowledge if this work showed a convention or a trap worth recording.")
fi

finish
