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
- Jobs needing secrets must be triggered by `push` to a protected branch, `workflow_dispatch`, a GitHub Environment with required reviewers, or `workflow_call` from a workflow that meets one of these conditions.
- A called workflow receives a secret only if the caller passes it, by name or with `secrets: inherit`, even a secret that exists only in the environment of the called job. That job then gets the environment's value. Keep such secrets `required: false`: `required` only checks that the caller passes the secret, not its value.

```yaml
# Correct pattern for jobs needing secrets: testacc.yml, called by semantic-release.yml on push to main
on:
  workflow_call:
    secrets:
      ANTHROPIC_API_KEY:
        required: false
  workflow_dispatch:

jobs:
  testacc:
    environment: acctest  # holds the secret
    env:
      ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}

# semantic-release.yml, the caller: passes the secret by name
jobs:
  acceptance-tests:
    uses: ./.github/workflows/testacc.yml
    secrets:
      ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
```

# Privileged jobs (release, publication)

A job that signs artifacts or writes to `main` holds credentials that every user of the provider ends up trusting: the GPG key signs the Registry checksums, and the semantic-release App bypasses the `main` ruleset. Write access to the repository must not be enough to reach them. `RELEASE.md` ("Protect the release credentials") has the GitHub settings; when writing or reviewing `goreleaser-release.yml`, `semantic-release.yml` or a new privileged workflow, check these six rules:

1. **Credentials live in an environment, never at repository or organization level.** The job declares `environment:` (`release` for signing, deployable from `v*` tags only, with required reviewers; `semantic-release` for publishing, deployable from `main` only), and the secrets are environment secrets. A repository secret is readable by a workflow pushed on any branch.
2. **Release tags are protected.** A tag ruleset on `v*` restricts creation, update and deletion, with the semantic-release App as the only bypass, because any `v*` tag starts the signed release.
3. **No shared cache in a job that signs or publishes.** `actions/setup-go` gets `cache: false` and the job uses no `actions/cache`: a cache restored there may have been saved by a test job, and Go does not re-hash extracted module sources on restore. Run `go mod download && go mod verify` instead.
4. **Installs come from a committed lockfile, without scripts.** `npm ci --ignore-scripts` from `.github/semantic-release/package-lock.json` (also enforced by its `.npmrc`), never `npm install` with inline versions: every transitive package would otherwise be resolved at release time and could run code next to the credentials.
5. **Credentials enter the job after the install.** `actions/checkout` sets `persist-credentials: false`, the App token is minted after `npm ci` and passed only to the step that publishes, and the job token keeps `contents: read`.
6. **Release tooling updates get a human review.** In `renovate.json`, the `release tooling` group (matched by file: both release workflows and `.github/semantic-release/package.json`) keeps `automerge: false` whatever the update type. Do not add these files to an automerged group.
   The group also matches `testacc.yml`, whose jobs hold the `acctest` keys. Add to it any new workflow whose jobs read a secret other than `GITHUB_TOKEN` or run in a protected environment. Root `go.mod` updates are never automerged either, because the acceptance tests compile them, and a tool that such a job downloads is pinned, never `latest` (`terraform_version` in `testacc.yml`).

```yaml
jobs:
  release:
    environment: semantic-release        # secrets scoped to main
    permissions:
      contents: read                     # writes go through the App token
    steps:
      - uses: actions/checkout@<full-sha> # <version tag>
        with:
          persist-credentials: false
      - name: Install semantic-release
        working-directory: .github/semantic-release
        run: npm ci --ignore-scripts --no-audit --no-fund
      - name: Generate app token         # minted after the install
        id: app-token
        uses: actions/create-github-app-token@<full-sha> # <version tag>
        with:
          app-id: ${{ secrets.SEMREL_APP_ID }}
          private-key: ${{ secrets.SEMREL_APP_PRIVATE_KEY }}
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
