---
name: CI workflow conventions
description: Runner, Go version strategy, action versions, and structural patterns used across workflow files
type: project
---

Conventions confirmed across .github/workflows/ files (last audited 2026-04-13):

- Runner: ubuntu-latest for all jobs
- Go version: go-version-file: 'go.mod' (pins to whatever go.mod declares, not hardcoded)
- actions/checkout: 34e114876b0b11c390a56381ad16ebd13914f8d5 # v4.3.1
- actions/setup-go: 40f1582b2485089dde7abd97c1529aa768e1baff # v5.6.0
- hashicorp/setup-terraform: b9cd54a3c349d3f38e8881555d616ced269862dd # v3.1.2
- terraform_wrapper: false is set on all jobs that use dev overrides (terraform-test job)
- All jobs that touch real API set env: ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
- Jobs that need real API run in environment: acctest
- permissions block is set per-job (not at workflow level) — contents: read for most jobs
- workflow-level permissions: {} (empty, i.e., no extra permissions by default)
- concurrency block used only in semantic-release.yml (cancel-in-progress: false for release safety)
- Jobs call make <target> directly — no inline reimplementation of logic
- Job IDs: lowercase with hyphens (e.g., goreleaser-snapshot, terraform-test)
- ANTHROPIC_API_KEY sourced from secrets.ANTHROPIC_API_KEY (set in the acctest GitHub environment)

Workflow split pattern:
- Quality gate checks (fmt, lint, unit tests, generate drift check) → provider.yml, trigger: pull_request
- Acceptance / integration tests (testacc, terraform-test) → testacc.yml, trigger: push to main + workflow_dispatch
- Release-time snapshot build check → goreleaser-snapshot.yml, trigger: pull_request
- Release publishing → goreleaser-release.yml, trigger: push tag; semantic-release.yml, trigger: push to main

**How to apply:** All new CI jobs must follow these conventions. New test jobs should use make <target> rather than reimplementing the underlying commands inline. Jobs requiring the real API must be placed in a workflow that uses the acctest environment and is NOT triggered on pull_request (to protect secrets from fork PRs).
