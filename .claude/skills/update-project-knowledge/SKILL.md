---
name: update-project-knowledge
description: >
  Reviews the work just done and records in the CLAUDE.md files and agent
  memories the conventions, traps and measured API behaviours that the code
  alone does not show. Use after a significant implementation task, when the
  user asks to update or sync project knowledge or the Claude configuration,
  or when the end-of-task reminder suggests it. Not for describing a change in
  its PR (update-pr-description).
model: sonnet
---

# update-project-knowledge

Keep the `CLAUDE.md` files and agent memories accurate and short. The root `CLAUDE.md` is read at the start of every session and a nested one each time its directory is touched, so a line earns its place only if an agent would get something wrong without it.

## Step 1: Gather context

```bash
git rev-parse --abbrev-ref HEAD
git log origin/main..HEAD --oneline
git diff origin/main..HEAD --name-only
wc -c CLAUDE.md internal/services/*/CLAUDE.md
```

Read the root `CLAUDE.md` and the `internal/services/<service>/CLAUDE.md` of each service the branch touches, to know what is already written. Keep the `wc -c` output: step 4 compares against it.

## Step 2: Decide what to record

Record a fact only when an agent reading the code and the existing `CLAUDE.md` files would still get it wrong:
- a measured API behaviour, with its date and how it was measured;
- a convention that neither the compiler nor the tests enforce, with its reason;
- a trap that caused a failure, with its cause and its fix.

Skip what the code shows: attribute lists, file paths and function names that `grep` finds, signatures, line numbers, the content of the diff, and anything already written in a `CLAUDE.md` file or an agent prompt.

For example, "`anthropic_memory` validates `path` against control characters" is visible in the schema validators: skip it. "The memories API answers 409 both for a failed precondition and for a path collision; only `Type() == memory_precondition_failed_error` means an out-of-band change" is invisible at the call site: record it.

## Step 3: Choose the place

| Knowledge | Place |
|---|---|
| A rule that holds for the whole repository (architecture, a convention shared by services, test layout, commands) | root `CLAUDE.md`, in the section that covers it |
| A detail of one service (an endpoint quirk, a load-bearing behaviour of one resource) | `internal/services/<service>/CLAUDE.md`, created if absent |
| A pattern an agent got wrong that is no repository rule | that agent's memory, `.claude/agent-memory/<agent>/`, one file per topic, listed in its `MEMORY.md` |

Agent prompts (`.claude/agents/*.md`) change only to fix something wrong in them, never to grow.

## Step 4: Stay under the ceiling

- Per run, the root `CLAUDE.md` grows by at most 1,000 bytes and each nested `CLAUDE.md` by at most 1,500 bytes, measured with `wc -c` against step 1. An entry is at most three sentences.
- An agent memory file stays under 40 lines; `MEMORY.md` keeps one line per file. Each memory entry carries its date and the command that re-checks it, because agents read these notes as true on every run.
- When new knowledge does not fit, first replace or shorten stale text in the same section. If it still does not fit, stop and propose to the user what to cut or move instead of going over.

## Step 5: Edit and report

Edit only what is missing or stale, preferring to update a line over adding one, and leave out session details that will not matter next week. Report each file changed with one line per change, and the `wc -c` before and after of each `CLAUDE.md` file touched. If nothing qualified, say so.

Then mark the review as done, so the end-of-task reminder stays quiet until the next commit:

```bash
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null)
touch "/tmp/claude-knowledge-done-$(echo "$branch" | tr '/' '-')"
```
