---
name: CI design intent
description: Intentional CI choices that are not gaps, checked against the workflows on 2026-11-03
type: project
---

Checked on 2026-11-03 against `main` at 67273a4. The workflow list changes: re-read `.github/workflows/` rather than trusting a count (`ls .github/workflows/`).

Intentional choices, not to be reported as coverage gaps:
- `make testacc` and `make terraform-test` run in `testacc.yml`, whose only triggers are `workflow_call` and `workflow_dispatch`: `semantic-release.yml` calls it on `push` to `main` and releases only when it passes. Neither file triggers on `pull_request`, so that fork PRs never run with the API keys. Re-check: `grep -nE "^  (push|pull_request|workflow_call|workflow_dispatch):|workflows/testacc.yml" .github/workflows/testacc.yml .github/workflows/semantic-release.yml`.
- `make install` has no CI job of its own: it is a local step, and `make terraform-test` depends on it. Re-check: `grep -n "^terraform-test:" GNUmakefile`.
- semantic-release is an npm project under `.github/semantic-release/` (exact versions, lockfile, `.npmrc` with `ignore-scripts`), installed with `npm ci --ignore-scripts` before the App token is minted; checkout uses `persist-credentials: false` and the job token is `contents: read`. Re-check: `grep -n "npm ci\|persist-credentials" .github/workflows/semantic-release.yml`.

The harden-runner settings, action SHAs and the per-job permissions are not recorded here: read them from the workflows and apply the `github-actions` skill.
