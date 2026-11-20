#!/usr/bin/env bash
# Tests for stop-reminders.sh: bash .claude/hooks/stop-reminders_test.sh
#
# Each case builds throwaway git repositories under a temporary directory
# (an upstream ippontech/terraform-provider-anthropic, a contributor's fork,
# a clone of either), runs the hook in the clone and checks the reminders it
# prints. Nothing touches the network: a stub `gh` answers every lookup with
# an error, which the hook treats as a failed lookup and records nothing for.

set -euo pipefail

hook="$(cd "$(dirname "$0")" && pwd)/stop-reminders.sh"

for tool in git jq; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "$tool is required to run these tests" >&2
    exit 1
  }
done

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Keep the contributor's git configuration (signing, hooks, default branch)
# out of the throwaway repositories.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.com
export GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.com

mkdir "$tmp/bin"
cat >"$tmp/bin/gh" <<'EOF'
#!/usr/bin/env bash
echo "stub: offline" >&2
exit 1
EOF
chmod +x "$tmp/bin/gh"
export PATH="$tmp/bin:$PATH"

# The hook's sentinels live under /tmp and are keyed by branch: a name of
# this run's own keeps a real sentinel from hiding a reminder.
run_id="stop-reminders-test-$$"

failures=0

# Runs the hook in the clone $1 and stores what it prints in $output.
run_hook() {
  output=$(CLAUDE_PROJECT_DIR="$1" bash "$hook" </dev/null)
}

# check <case> <yes|no> <text>: whether the hook output contains <text>.
check() {
  local name=$1 want=$2 text=$3 got=no
  case "$output" in *"$text"*) got=yes ;; esac
  if [ "$got" = "$want" ]; then
    echo "ok   $name"
  else
    echo "FAIL $name: expected '$text' present=$want, output: ${output:-<none>}"
    failures=$((failures + 1))
  fi
}

# commit <repo> <file> <content> <message>
commit() {
  mkdir -p "$(dirname "$1/$2")"
  printf '%s\n' "$3" >"$1/$2"
  git -C "$1" add "$2"
  git -C "$1" commit -q -m "$4"
}

# docs/ as last generated long ago, so any commit counts as newer.
age_docs() {
  touch -t 200101010000 "$1/docs/index.md"
}

docs_reminder="docs/ was last generated"
code_reminder="Code changed on"

# Upstream: one initial commit, then main moves on with a Go change, the
# commit a fork's stale main does not have.
upstream="$tmp/remotes/ippontech/terraform-provider-anthropic.git"
fork="$tmp/remotes/contributor/terraform-provider-anthropic.git"
git init -q --bare --initial-branch=main "$upstream"
git init -q --bare --initial-branch=main "$fork"
seed="$tmp/seed"
git init -q --initial-branch=main "$seed"
commit "$seed" docs/index.md "docs" "initial"
commit "$seed" main.go "package main" "initial go"
git -C "$seed" push -q "$fork" main
commit "$seed" internal/upstream.go "package internal" "upstream change"
git -C "$seed" push -q "$upstream" main

# A standard clone: origin is the upstream repository.
clone="$tmp/clone"
git clone -q "$upstream" "$clone"
git -C "$clone" switch -q -c "$run_id-standard"
commit "$clone" internal/feature.go "package internal // feature" "feature"
age_docs "$clone"
run_hook "$clone"
check "standard clone, Go commit: knowledge reminder" yes "$code_reminder"
check "standard clone, Go commit: make reminder" yes "$docs_reminder"

# A fork clone (gh repo fork --clone): origin is the fork, whose main stayed
# at the initial commit, and upstream is ippontech, which main follows.
forked="$tmp/forked"
git clone -q "$fork" "$forked"
git -C "$forked" remote add upstream "$upstream"
git -C "$forked" fetch -q upstream
git -C "$forked" reset -q --hard upstream/main

git -C "$forked" switch -q -c "$run_id-docs-only"
commit "$forked" README.md "readme" "docs only"
age_docs "$forked"
run_hook "$forked"
check "fork clone, docs-only branch: no knowledge reminder" no "$code_reminder"
check "fork clone, docs-only branch: no make reminder" no "$docs_reminder"

git -C "$forked" switch -q -c "$run_id-fork-code" upstream/main
commit "$forked" internal/feature.go "package internal // feature" "feature"
age_docs "$forked"
run_hook "$forked"
check "fork clone, Go commit: knowledge reminder" yes "$code_reminder"
check "fork clone, Go commit: make reminder" yes "$docs_reminder"

# No remote points to ippontech: no base to compare the branch with, so the
# committed-change checks stay silent.
git -C "$forked" remote remove upstream
run_hook "$forked"
check "no upstream remote, Go commit: no knowledge reminder" no "$code_reminder"
check "no upstream remote, Go commit: no make reminder" no "$docs_reminder"

# An uncommitted change needs no base.
printf '%s\n' "package internal // edited" >"$forked/internal/feature.go"
run_hook "$forked"
check "no upstream remote, uncommitted Go change: make reminder" yes "$docs_reminder"

if [ "$failures" -gt 0 ]; then
  echo "$failures check(s) failed"
  exit 1
fi
echo "all checks passed"
