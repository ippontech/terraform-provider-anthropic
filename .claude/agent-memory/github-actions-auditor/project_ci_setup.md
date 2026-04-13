---
name: CI/CD setup overview
description: Key facts about this project's five-workflow CI/CD setup that are not derivable from reading files alone
type: project
---

Five workflows exist as of April 2026:
- `provider.yml` — PR + merge_group: lint (fmt + golangci-lint), unit tests (make test), generate (make generate + drift check)
- `testacc.yml` — push to main + workflow_dispatch: acceptance tests (make testacc) and terraform-test (make terraform-test); both jobs use the `acctest` GitHub Environment, which gates secret access
- `goreleaser-snapshot.yml` — merge_group: GoReleaser snapshot build; validates release config before merge
- `goreleaser-release.yml` — push of semver tags: full GoReleaser release with GPG signing
- `semantic-release.yml` — push to main: runs semantic-release to create tags and changelogs; uses a GitHub App token to bypass branch protection

**Why:** Knowing these triggers avoids wasting time re-reading files every audit.

**How to apply:** Use as a quick orientation; still re-read files to check for drift.

Key intentional design decisions:
- Acceptance tests run AFTER merge (on push to main), not on PRs — intentional to protect the API key from fork PRs
- `acctest` GitHub Environment acts as the secret gate for ANTHROPIC_API_KEY
- `make install` is called inside `make build` chain; no standalone install CI job — intentional (local dev only)
- `terraform-test` (make terraform-test) IS covered by testacc.yml
- semantic-release uses pinned npm package versions in the install command (not @latest)
- All actions/checkout and actions/setup-go references are pinned to full SHA
- Workflow-level `permissions: {}` is set on every workflow — good baseline

Harden-runner status (updated April 2026):
- `step-security/harden-runner` was absent from all 7 jobs across all 5 workflows; added in April 2026
- Pinned to v2.17.0 @ SHA f808768d1510423e83855289c910610ca9b43176, with `egress-policy: audit` on every job
- Placement: always the very first step, before actions/checkout
- To update the SHA in future: `git ls-remote https://github.com/step-security/harden-runner.git 'refs/tags/v2.*' | grep -v '\^{}' | awk '{print $2,$1}' | sed 's|refs/tags/v||' | sort -V | tail -1`

Known security finding (as of April 2026 audit):
- `goreleaser-release.yml` job has `contents: write` but no `id-token: write` — acceptable, only needs to publish release artifacts
- `semantic-release.yml` job has `contents: write`, `issues: write`, `pull-requests: write` — intentional, semantic-release needs all three to create releases, update changelogs, and comment on PRs/issues
- `goreleaser-snapshot.yml` passes GITHUB_TOKEN env var to goreleaser snapshot — goreleaser only needs it to read repo metadata for snapshot; low risk but could be tightened
- `semantic-release.yml` uses `actions/setup-node` rather than calling `make` — no Makefile target exists for semantic-release (it's a JS tool), so this is not drift
