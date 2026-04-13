---
name: CI workflow conventions
description: Runner, Go version strategy, action versions, and structural patterns used across workflow files
type: project
---

Conventions confirmed in .github/workflows/ci.yml (audited 2026-04-13):

- Runner: ubuntu-latest for all jobs
- Go version: go-version-file: 'go.mod' (pins to whatever go.mod declares, not hardcoded)
- actions/checkout: v4
- actions/setup-go: v5
- hashicorp/setup-terraform: v3 (with terraform_wrapper: false for dev-override jobs)
- Trigger: on push and pull_request to main branch
- ANTHROPIC_API_KEY sourced from secrets.ANTHROPIC_API_KEY
- Jobs that call make commands directly (not reimplementing inline shell logic)
- Job naming: snake-case job IDs, Title Case job names
- release.yml uses semantic-release and goreleaser (separate workflow, not test-related)

**How to apply:** All new CI jobs must follow these conventions. New test jobs should use make <target> rather than reimplementing the underlying commands inline.
