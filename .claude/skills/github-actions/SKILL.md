---
name: github-actions
description: GitHub Actions best practices for writing and reviewing workflows. Use when creating or modifying .github/workflows/ files.
model: sonnet
---

# Workflow syntax

Refer to the [GitHub Actions workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax) guide to find details about how to implement certain actions in workflows or actions.

# Action pinning

Every `uses:` reference **must** be pinned to a full commit SHA. Tags and branches are mutable and can be silently redirected (supply chain attack vector). Add the version tag as a trailing comment so humans know what version is in use.

```yaml
# Bad — mutable tag
uses: hashicorp/setup-terraform@v3

# Good — immutable SHA + human-readable comment
uses: hashicorp/setup-terraform@b9cd54a3c349d3f38e8881555d616ced269ef032 # v3.1.2
```

This applies to all third-party actions including `actions/` official actions.

# Permissions

Always set a zero-permission block at the **workflow level** to disable all permissions by default:

```yaml
permissions: {}
```

Then set an explicit `permissions:` block on **every job**, granting only what that job needs:

- Read-only jobs (lint, test, build): `contents: read`
- Jobs that push or tag: `contents: write`
- Jobs that authenticate to OIDC providers: `id-token: write`
- Jobs that comment on PRs or issues: `pull-requests: write`, `issues: write`

Never leave a job without a `permissions:` block.

# Command injection prevention

`${{ }}` expressions are expanded **before** the shell executes — no escaping occurs. Any attacker-controlled value in a `run:` step is an arbitrary code execution risk.

Never interpolate these directly in `run:` steps:

| Source | Variable |
|--------|----------|
| PR title | `github.event.pull_request.title` |
| PR body | `github.event.pull_request.body` |
| Issue title | `github.event.issue.title` |
| Comment body | `github.event.comment.body` |
| Branch name | `github.event.pull_request.head.ref` |
| Commit message | `github.event.head_commit.message` |
| Dispatch input | `github.event.inputs.*` |

```yaml
# Bad — injection risk
- run: echo "PR: ${{ github.event.pull_request.title }}"

# Good — pass through env var
- run: echo "PR: $PR_TITLE"
  env:
    PR_TITLE: ${{ github.event.pull_request.title }}
```

# Harden runner

Every job's **first** step must be `step-security/harden-runner`, SHA-pinned, with `egress-policy: audit`. This intercepts all outbound network calls and logs unexpected egress (e.g., a compromised action exfiltrating secrets).

```yaml
steps:
  - name: Harden runner
    uses: step-security/harden-runner@<full-sha> # <version tag>
    with:
      egress-policy: audit
```

# Secret handling

- Secrets must only be passed via `env:` or `with:` — never interpolated directly into `run:` shell commands with `${{ secrets.FOO }}` (log injection risk).
- Never print, echo, or write secrets to files or stdout.

```yaml
# Bad — log injection risk
- run: curl -H "Authorization: ${{ secrets.API_KEY }}" https://example.com

# Good
- run: curl -H "Authorization: $API_KEY" https://example.com
  env:
    API_KEY: ${{ secrets.API_KEY }}
```

# Fork PR security

Jobs that use repository secrets must not be triggerable from fork pull requests. Fork PRs run with no access to secrets under a plain `pull_request` trigger — but this protection is bypassed by `pull_request_target`, which runs in the base branch context and exposes secrets.

- Never use `pull_request_target` for jobs that access secrets unless forked code is never checked out.
- Jobs needing secrets must be triggered by `push` to a protected branch, `workflow_dispatch`, or a GitHub Environment with required reviewers.

```yaml
# Correct pattern for jobs needing secrets (e.g., acceptance tests)
on:
  push:
    branches: [main]
  workflow_dispatch:

jobs:
  acceptance-tests:
    environment: production  # requires manual approval from a maintainer
    env:
      ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
```

# Security tooling

## `security.yml` workflow

The project must have `.github/workflows/security.yml` with four jobs, all SHA-pinned, triggered on `pull_request` and `merge_group`:

| Job | Tool | What it checks |
|-----|------|----------------|
| `actionlint` | `rhysd/actionlint` | Syntax errors and hazardous patterns in workflow YAML |
| `poutine` | Binary from GitHub releases | Supply-chain vulnerabilities (unpinned actions, script injections) |
| `semgrep` | `semgrep/semgrep-action` | Anti-patterns including `pull_request_target` misuse |
| `checkov` | `bridgecrewio/checkov-action` | Bad practices (write-all permissions, unsecure commands) |

## Pre-commit hooks

`.pre-commit-config.yaml` must include hooks for all four tools: `actionlint`, `poutine`, `semgrep`, `checkov`.

## Tool configuration files

- **`tools/semgrep.yml`** — custom semgrep rules scoped to `.github/`. Add new workflow anti-pattern rules here.
- **`tools/.poutine.yml`** — skip list for known-safe unverified-creator actions. Uses PURL format `pkg:githubactions/<owner>/<repo>` (no SHA). When intentionally using an unverified-creator action, add it here with a comment explaining why it is trusted.

# CI coverage

Quality-gate Makefile targets must each have a corresponding CI job. Reference `make <target>` in CI steps rather than reimplementing the logic inline — this avoids drift between local and CI behavior.

Targets that must be covered: `fmt`, `lint`, `test`, `testacc`, `generate`, `terraform-test`.

The `install` target writes to a local path and does not need its own CI job, but is typically a prerequisite step.
