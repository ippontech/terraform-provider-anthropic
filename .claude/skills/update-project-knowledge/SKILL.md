---
name: update-project-knowledge
description: >
  Reviews the work just done and updates CLAUDE.md and agent memories to
  reflect any new conventions, packages, resources, or patterns discovered.
  Run after completing any significant implementation task.
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

Also read `CLAUDE.md` to know what is already documented.

### Step 2 — Identify gaps

Cross-reference changed files against CLAUDE.md. Check each category:

| If this changed | Check this in CLAUDE.md |
|---|---|
| New `*_resource.go` or `*_data_source.go` | "Implemented resources and data sources" list |
| New `make` target in `GNUmakefile` | "Commands" section |
| New Go package under `internal/` | "Architecture" section |
| New provider-wide convention | "Provider coding conventions" section |

For agent memories, check if the session revealed a pattern that agents got wrong or a non-obvious project idiom — record it in the relevant agent's memory directory.

Skip anything already documented. Skip anything derivable by reading the code.

### Step 3 — Update

Edit only files where something is genuinely missing or stale. Prefer adding to existing sections.

Do NOT:
- Duplicate content already in CLAUDE.md or agent files
- Make agent MD files longer
- Record ephemeral session details

### Step 4 — Report and suppress hook

List each file updated and what was added (one line per change). If nothing needed updating, say so.

Then write the sentinel file to suppress the stop hook for 5 minutes:

```bash
branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null)
touch "/tmp/claude-project-knowledge-done-$(echo "$branch" | tr '/' '-')"
```
