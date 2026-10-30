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

Three Terraform checks need no credentials and no network beyond downloading Go modules. They run on every pull request (`.github/workflows/terraform.yml`):

```bash
make terraform-fmt-check   # terraform fmt -check -diff -recursive
make terraform-validate    # terraform validate in every example directory, against the provider just built
make terraform-test-mock   # the native test files that use mock_provider
```

`make terraform-test-mock` selects every test file containing a `mock_provider` block, so such a file must not need credentials.

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

- [ ] Add [semantics prefix](#semantic-pull-requests) to your commits
- [ ] PR title and description written in English
- [ ] Run `make` (fmt + lint + test + install + generate)
- [ ] Run pre-commit hooks `pre-commit run -a`
- [ ] CI is passing

## Semantic Pull Requests

To generate changelog, Pull Requests and Commit messages must follow [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/) specs below:

- `feat:` for new features
- `fix:` for bug fixes
- `docs:` for documentation and examples
- `refactor:` for code refactoring
- `test:` for tests
- `ci:` for CI purpose
- `chore:` for chores stuff

We use the `chore` prefix to generate a new release and for changelog generation (the label '[skip ci]' allows us to skip CI). It can be used for `chore: update changelog` commit message by example.

We squash-merge PRs. The title of the PR is the commit title (commit type + scope + short description) and the description of the PR is the commit body.

## Dependency Updates

Dependencies are updated automatically by [Renovate](https://docs.renovatebot.com/), run as the Mend-hosted app at the `ippontech` organization level (nothing runs in this repository's CI). It opens grouped pull requests (one per manager: Go modules, GitHub Actions, mise tools, pre-commit hooks, and tool versions pinned in workflow inputs), and auto-merges `minor` / `patch` / `pin` / `digest` updates once required checks pass, three days after a release is published. Updates to the release tooling (anything declared in `goreleaser-release.yml` or `semantic-release.yml`) are the exception: they are grouped in a `release tooling` PR that a maintainer must review and merge.

Its behaviour is driven by `renovate.json` at the repository root. See [`.github/renovate.md`](.github/renovate.md) for the full configuration reference and how to validate a change to it. Maintainers can inspect recent runs, their logs, and trigger an on-demand scan from the [Mend dashboard for this repository](https://developer.mend.io/github/ippontech/terraform-provider-anthropic) (requires access to the `ippontech` organization on Mend).

## Claude Code

This project includes a [Claude Code](https://claude.ai/code) configuration under `.claude/` to help contributors follow Terraform provider best practices.

The [`terraform-skill@antonbabenko`](https://github.com/antonbabenko/terraform-skill) plugin is enabled for this project. It provides guidance and code generation assistance aligned with HashiCorp's Terraform plugin framework conventions — schema design, resource/data source patterns, testing, and documentation generation.

If you use Claude Code, this skill will be automatically active when working in this repository. No additional setup is required.
