# Contributing

When contributing to this repository, please first discuss the change you wish to make via issue,
email, or any other method with the owners of this repository before making a change.

## Local Development Setup

### Prerequisites

This project uses [mise](https://mise.jdx.dev/) to manage tool versions (Go, Terraform, golangci-lint, etc.). Install it and run:

```bash
mise install
```

This installs the exact versions declared in `mise.toml`.

### Go version

CI and releases build with the Go release named by the `toolchain` line of `go.mod` (`actions/setup-go` reads it before the `go` line). Locally, a Go older than that downloads it on first use (the default `GOTOOLCHAIN=auto`), and a newer one, such as the Go from `mise.toml`, is used as is. Keep the `toolchain` line: it is what keeps fixed standard-library vulnerabilities out of the released binaries, and Renovate bumps it (see [`.github/renovate.md`](.github/renovate.md#go-version)).

The `govulncheck` CI job fails a pull request that brings in a known vulnerability the code reaches; findings the base branch already has are listed in the job output without failing it. Its weekly run on `main` fails on any reachable vulnerability. To list them all locally, like the weekly run (standard-library findings refer to the Go that `go version` reports in the repository, which may be newer than the `toolchain` line):

```bash
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

### Build and install the provider

```bash
make install
```

This compiles the provider and installs it via `go install`. The binary location depends on your Go setup — find it with:

```bash
whereis terraform-provider-anthropic
```

### Configure Terraform to use the local binary

For ad-hoc local development, create or edit `~/.terraformrc` with the path found above:

```hcl
provider_installation {
  dev_overrides {
    "registry.terraform.io/ippontech/anthropic" = "/path/to/your/go/bin"
  }
  direct {}
}
```

With `dev_overrides`, `terraform init` is not required — run `terraform plan` directly. Terraform will show a warning about development overrides being in effect; this is expected.

Alternatively, `make terraform-test` auto-generates a project-local `.dev.tfrc` and uses it automatically (see [Run Terraform native tests](#run-terraform-native-tests) below).

### Set your credentials

The provider accepts three credentials:

```bash
# Standard API key: most resources and data sources (agents, environments, skills, vaults, files, ...)
export ANTHROPIC_API_KEY="sk-ant-..."

# Admin API key: organization endpoints (workspaces and their members, API keys, invites, organization members)
export ANTHROPIC_ADMIN_API_KEY="sk-ant-admin-..."

# OAuth bearer token with the org:admin scope: Workload Identity Federation endpoints
# (service accounts, federation issuers and rules), which reject API keys
export ANTHROPIC_AUTH_TOKEN="..."
```

At least one must be set, and they can be combined. The provider can also obtain the OAuth token itself through its `federation` block, which conflicts with `auth_token` (see `docs/index.md`).

Leave `ANTHROPIC_AUTH_TOKEN` unset when you run acceptance tests: the OAuth-gated tests create federation objects and service accounts that can only be archived, never deleted, and there is no dedicated test organization for them yet (#58, #137).

> **Test isolation.** Acceptance and Terraform native tests create real resources. Use an `ANTHROPIC_API_KEY` scoped to a **dedicated, non-production workspace** so test resources never land in a production workspace. This project uses a workspace named `terraform-tests`: standard-API resources (vaults, agents, environments, skills, ...) are created there via the key's scope, and the Admin API read-only data source tests target the same workspace by its ID (exposed as `acctest.TerraformTestsWorkspaceID`).

A `.env` file at the project root (gitignored) can hold these keys and other machine-specific values. `mise.toml` declares it under `[env] _.file = ".env"`, so [mise](https://mise.jdx.dev/environments/) exports its variables automatically when you enter the directory; nothing to source by hand.

### Run unit tests

```bash
make test
```

`make cover` runs the same tests, then the coverage gate of the CI `coverage` job (`tools/coversummary`, thresholds and exclusions in `.coverage.json`), followed by the coverage of every resource and data source file, least covered first. The gate is a required check with three blocking rules and one monitored one, measured on the Go files of the module outside the test-helper packages, `main.go` and `tools/`:

- **Diff**: at least 75% of the executable statements a pull request adds or changes (non-test Go files) are covered by unit tests. Write the tests with the code; `httptest` fakes and `resource.UnitTest` need no credential.
- **Ratchet**: the total stays at or above `.coverage-baseline` minus 0.5 point. When a pull request raises the total, the job summary gives the new value to commit in `.coverage-baseline`. Lowering it is possible in the pull request that needs it, with a reason reviewers accept.
- **Floor**: once the baseline reaches 80%, the total never drops under 80% and the baseline cannot go back under it.
- **Packages** (monitored, not yet enforced): the job summary lists every package with its coverage and its distance to 80%, least covered first. `package_min` in `.coverage.json` is `null` for now; the last pull request of the coverage effort sets it to 80, and from then on every package must stay at 80% or more, not only the total.

Locally, `make cover` compares the working tree, as the tests profile it, with its merge base with `origin/main` (`COVER_BASE=<ref>` to change it): committed and uncommitted changes count, in tracked files only (`git add -N` a new file for the diff rule to see it). `go test -cover` needs Go's `covdata` tool, missing from the toolchains `GOTOOLCHAIN` downloads (`go: no such tool "covdata"`); see the `covdata` note in CLAUDE.md.

### Run acceptance tests

Go acceptance tests run against the live Anthropic API:

```bash
TF_ACC=1 make testacc
```

Requires `ANTHROPIC_API_KEY` to be set.

### Run Terraform native tests

Terraform native tests (`.tftest.hcl` files under `tests/`) build the provider, generate a project-local `.dev.tfrc`, and run `terraform test`:

```bash
make terraform-test
```

Requires both `ANTHROPIC_API_KEY` and `ANTHROPIC_ADMIN_API_KEY` to be set (some tests exercise Admin API resources).

## Pull Request Process

1. Create your PR and add reviewers. Owners or contributors of this repository must be added as reviewers.
2. Run `make` (formats, lints, tests, installs, and regenerates docs in one step).
3. Run pre-commit hooks `pre-commit run -a`.
4. Once all comments and checklist items have been addressed, your contribution will be merged! Merged PRs will be included in the next release. [Semantic release](https://github.com/semantic-release/semantic-release) will be in charge to construct the Release automatically (Tag, CHANGELOG).

## Commit Signing

**Signed commits are not required to contribute.** You can open and get a Pull Request merged without configuring any GPG/SSH signing key. We deliberately keep the contribution bar low: trust in this project comes from code review and CI, not from commit-level signatures.

What *is* signed is what ships:

- **Release artifacts are GPG-signed.** Every release publishes a `*_SHA256SUMS` checksum file signed with the maintainers' GPG key (see `.goreleaser.yml` and `.github/workflows/goreleaser-release.yml`). This is the signature the Terraform Registry and `terraform init` verify when users install the provider — it is the signature that matters for supply-chain integrity.
- **Merges land as GitHub-signed commits.** Because we squash-merge, the commit that lands on `main` is created and signed by GitHub's web-flow key.

### Optional: sign your own commits

Maintainers and contributors are welcome (but not obliged) to sign their commits. SSH signing is the simplest setup:

```bash
git config gpg.format ssh
git config user.signingkey ~/.ssh/id_ed25519.pub
git config commit.gpgsign true
```

For GitHub to show **Verified**, add the key's public half under your account *Settings → SSH and GPG keys* (key type **Signing Key**), and make sure your commit author email is a verified email on that account.

## Checklists for contributions

- [ ] PR title follows [Conventional Commits](#conventional-commits) (checked by CI)
- [ ] PR title and description written in English
- [ ] Run `make` (fmt + lint + test + install + generate)
- [ ] Run pre-commit hooks `pre-commit run -a`
- [ ] CI is passing

## Conventional Commits

Pull requests are squash-merged: the PR title becomes the subject of the commit on `main`, and the PR description its body. [semantic-release](https://semantic-release.gitbook.io/semantic-release/) reads that commit to choose the next version and write the release notes, so the PR title must follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/): `<type>[(scope)]: <subject>`, for example `feat(agents): add execution_identity`. The `pr-title` check enforces it on every pull request. The commits inside a pull request do not reach `main`; the optional `commit-msg` hook below keeps them in the same format anyway.

| Type | Use it for | Release |
|---|---|---|
| `feat` | a new resource, data source, attribute or behaviour | minor |
| `fix` | a bug fix; Renovate also uses `fix(deps)` for Go module and Go toolchain updates, which change the shipped binary | patch |
| `perf`, `refactor`, `build` | performance, refactoring, build changes | patch |
| `docs` | documentation and examples: the Terraform Registry only shows the docs of a released version | patch |
| `test`, `ci`, `style`, `chore` | tests, CI, formatting, tooling; Renovate uses `chore(deps)` for CI actions, pre-commit hooks and mise tools | none |
| `Revert "<title>"` (GitHub's revert button) | undoing a merged pull request | patch |

`build`, `refactor` and `docs` release by choice of this repository (`releaseRules` in `.releaserc`); the others follow the defaults of `@semantic-release/commit-analyzer`. The `chore(release): <version>` commits on `main` are written by semantic-release itself after each release, to update `CHANGELOG.md`: a `chore` commit never triggers a release.

A breaking change, marked by `!` after the type or a `BREAKING CHANGE:` footer, publishes a major version. semantic-release reads such a footer on any line of the description that starts with `BREAKING CHANGE` or `BREAKING-CHANGE`, in any case and after spaces or `*`, so `Breaking change: none` counts too. The `pr-title` check refuses both on pull requests to `main`, except the `major/<version>` release PR, which is merged with a merge commit: breaking changes go through the staging branch described in [RELEASE.md](RELEASE.md#major-versions).

To check your own commit messages locally, `pre-commit install` also installs a `commit-msg` hook ([conventional-pre-commit](https://github.com/compilerla/conventional-pre-commit)) that rejects a message outside these types.

## Dependency Updates

Dependencies are updated automatically by [Renovate](https://docs.renovatebot.com/), run as the Mend-hosted app at the `ippontech` organization level (nothing runs in this repository's CI). It opens grouped pull requests (one per manager: Go modules, GitHub Actions, mise tools, pre-commit hooks, and tool versions pinned in workflow inputs), and auto-merges `minor` / `patch` / `pin` / `digest` updates once required checks pass, three days after a release is published. Updates to the release tooling (anything declared in `goreleaser-release.yml` or `semantic-release.yml`) are the exception: they are grouped in a `release tooling` PR that a maintainer must review and merge.

Its behaviour is driven by `renovate.json` at the repository root. See [`.github/renovate.md`](.github/renovate.md) for the full configuration reference and how to validate a change to it. Maintainers can inspect recent runs, their logs, and trigger an on-demand scan from the [Mend dashboard for this repository](https://developer.mend.io/github/ippontech/terraform-provider-anthropic) (requires access to the `ippontech` organization on Mend).

## Claude Code

This project includes a [Claude Code](https://claude.ai/code) configuration under `.claude/` to help contributors follow Terraform provider best practices.

The [`terraform-skill@antonbabenko`](https://github.com/antonbabenko/terraform-skill) plugin is enabled for this project. It provides guidance and code generation assistance aligned with HashiCorp's Terraform plugin framework conventions — schema design, resource/data source patterns, testing, and documentation generation.

If you use Claude Code, this skill will be automatically active when working in this repository. No additional setup is required.
