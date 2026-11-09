---
name: update-project-knowledge
description: >
  Reviews the work just done and updates CLAUDE.md, agent memories, and
  subagent MD files to reflect any new conventions, packages, resources, or
  patterns discovered. Run after completing any significant implementation task.
model: sonnet
---

# update-project-knowledge

Keep CLAUDE.md and agent memories accurate after each implementation task.

## Trigger

- Proactively at the end of any session where significant code was written
- Explicitly when the user says "update Claude config", "sync project knowledge", or similar

## Workflow

### Step 1 — Gather context

```bash
git rev-parse --abbrev-ref HEAD
git log origin/main..HEAD --oneline
git diff origin/main..HEAD --name-only
```

Also read the root `CLAUDE.md`, and the `internal/services/<service>/CLAUDE.md` of every service the branch touched, to know what is already documented.

### Step 2 — Identify gaps

Cross-reference changed files against the CLAUDE.md files. Check each category:

| If this changed | Check this |
|---|---|
| New `*_resource.go` or `*_data_source.go` | Its name on the service's line of the root "Services" list, and its load-bearing details in `internal/services/<service>/CLAUDE.md` (a new service directory gets its own file and a new line) |
| New `make` target in `GNUmakefile` | Root "Commands" section |
| New Go package under `internal/` | Root "Architecture" section |
| New provider-wide convention | Root "Provider coding conventions" section |

The root `CLAUDE.md` holds repo-wide rules only (its opening paragraph says so): per-resource notes go in the service's `CLAUDE.md`, never in the root file.

For agent memories, check if the session revealed a pattern that agents got wrong or a non-obvious project idiom — record it in the relevant agent's memory directory.

Skip anything already documented. Skip anything derivable by reading the code.

### Step 3 — Update

Edit only files where something is genuinely missing or stale. Prefer adding to existing sections.

Do NOT:
- Duplicate content already in CLAUDE.md or agent files
- Make agent MD files longer
- Record ephemeral session details

### Step 4 — Report

List each file updated and what was added (one line per change). If nothing needed updating, say so.

Then suppress the Stop hook reminder for this branch:

```bash
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null)
touch "/tmp/claude-knowledge-done-$(echo "$branch" | tr '/' '-')"
```
