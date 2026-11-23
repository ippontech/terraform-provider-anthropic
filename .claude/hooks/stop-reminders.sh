#!/usr/bin/env bash
# Stop hook (see .claude/settings.json): end-of-task reminders for the user.
#
# It never blocks Claude from stopping and never runs or publishes anything on
# its own: it prints at most one systemMessage, shown to the user, when
#   1. Go sources, docs templates or examples changed or were deleted after
#      docs/ was last generated (run `make`, which includes `make generate`),
#      whether the change is uncommitted or was committed without running
#      make, on any branch, main and a detached HEAD included;
#   2. the branch has an open PR and commits newer than the last
#      /update-pr-description or /ship run on this machine;
#   3. the branch changes Go or Terraform files and has a commit of its own
#      touching them newer than the last /update-project-knowledge run
#      (committing the CLAUDE.md edits that skill writes, or merging main,
#      does not bring the reminder back).
# The skills mark 2 and 3 as done by touching /tmp/claude-pr-desc-done-<branch>
# and /tmp/claude-knowledge-done-<branch>; the hook touches the first itself
# when GitHub reports no open PR for the branch. The branch is compared with
# main of the remote that points to ippontech/terraform-provider-anthropic
# (origin in a standard clone, upstream in a fork clone); with no such remote,
# the checks on committed changes stay silent. Any failure (missing git, gh or
# jq, no network) ends silently with exit 0. Tests: stop-reminders_test.sh.

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

# The upstream main the branch is compared with: <remote>/main of the remote
# whose URL is ippontech/terraform-provider-anthropic, as the ship skill finds
# it, since a fork clone's origin is the fork and its main is often stale.
# Empty when no remote matches or its main was never fetched.
base=""
base_remote=$(git remote -v 2>/dev/null |
  awk '$3 == "(fetch)" && $2 ~ /[\/:]ippontech\/terraform-provider-anthropic(\.git)?$/ {print $1; exit}')
if [ -n "$base_remote" ] && git rev-parse --verify -q "refs/remotes/$base_remote/main" >/dev/null; then
  base="$base_remote/main"
fi

# Modification time in seconds since the epoch (GNU stat, then BSD stat).
mtime() {
  stat -c %Y "$1" 2>/dev/null || stat -f %m "$1" 2>/dev/null || echo 0
}

# True when a non-test Go file, template or example ($1) is newer than
# docs/index.md (make generate rewrites every file under docs/). A deleted
# file has no time of its own: the nearest directory still there changed when
# the entry went away.
newer_than_docs() {
  local path=$1
  case "$path" in
    *_test.go) return 1 ;;
  esac
  while [ ! -e "$path" ]; do path=$(dirname "$path"); done
  [ ! -f docs/index.md ] || [ "$path" -nt docs/index.md ]
}

# 1. Docs generation: an uncommitted change newer than docs/index.md, or one
#    the branch committed ($base..HEAD) without running make since:
#    both the file and its commit are newer than docs/index.md. The commit
#    time leaves out a fresh checkout, which writes the files after
#    docs/index.md but after their commits too.
docs_stale=false
while IFS= read -r line; do
  path=${line:3}
  path=${path##* -> }
  if newer_than_docs "$path"; then
    docs_stale=true
    break
  fi
done < <(git status --porcelain --untracked-files=all -- '*.go' templates examples 2>/dev/null)
if [ "$docs_stale" = false ] && [ -n "$base" ]; then
  docs_generated=$(mtime docs/index.md)
  commit_time=0
  while IFS= read -r line; do
    case "$line" in
      "") continue ;;
      @*) commit_time=${line#@}; continue ;;
    esac
    if [ "$commit_time" -ge "$docs_generated" ] && newer_than_docs "$line"; then
      docs_stale=true
      break
    fi
  done < <(git log --no-merges --format=@%ct --name-only "$base..HEAD" -- '*.go' templates examples 2>/dev/null)
fi
if [ "$docs_stale" = true ]; then
  reminders+=("Go sources, templates or examples changed after docs/ was last generated: run \`make\` (it includes \`make generate\`) and commit what it changes in docs/.")
fi

# 2 and 3 are about a work branch: none on main or a detached HEAD.
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null) || finish
case "$branch" in
  "" | HEAD | main) finish ;;
esac
slug=$(printf '%s' "$branch" | tr '/' '-')
last_commit=$(git log -1 --format=%ct 2>/dev/null) || finish
# The branch's own last commit touching Go or Terraform files: not the main
# commits a merge brings in, nor the merge itself. Empty without a base.
last_code_commit=""
if [ -n "$base" ]; then
  last_code_commit=$(git log -1 --format=%ct --no-merges "$base..HEAD" -- '*.go' '*.tf' 2>/dev/null || true)
fi

# True when the sentinel ($1) is missing or older than the commit time $2.
older_than() {
  [ ! -f "$1" ] || [ "$(mtime "$1")" -lt "${2:-0}" ]
}

# 2. PR description: only query GitHub when the sentinel is older than the
#    last commit, so a stop without new commits costs no network call. An
#    answer of no open PR (none found, or only closed ones) touches the
#    sentinel too, so the next lookup waits for a new commit; a failed lookup
#    (no network, not logged in) does not, and the next stop tries again.
if older_than "/tmp/claude-pr-desc-done-$slug" "$last_commit" && command -v gh >/dev/null 2>&1; then
  lookup=$(gh pr view "$branch" --json number,state \
    --jq '"pr=" + (if .state == "OPEN" then (.number | tostring) else "" end)' 2>&1 || true)
  pr=$(printf '%s\n' "$lookup" | sed -n 's/^pr=\([0-9][0-9]*\)$/\1/p')
  if [ -n "$pr" ]; then
    reminders+=("PR #$pr on $branch has commits its description may not cover yet: run /update-pr-description to draft an update (nothing is published without your approval).")
  elif printf '%s\n' "$lookup" | grep -qE '^(pr=$|no pull requests found)'; then
    touch "/tmp/claude-pr-desc-done-$slug" 2>/dev/null || true
  fi
fi

# 3. Project knowledge: the branch touches Go or Terraform files, and a commit
#    touching them is newer than the last run.
if [ -n "$base" ] &&
  older_than "/tmp/claude-knowledge-done-$slug" "$last_code_commit" &&
  git diff --name-only "$base...HEAD" 2>/dev/null | grep -qE '\.(go|tf)$'; then
  reminders+=("Code changed on $branch: run /update-project-knowledge if this work showed a convention or a trap worth recording.")
fi

finish
