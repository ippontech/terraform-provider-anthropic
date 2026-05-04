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

### Set your API keys

The provider uses two API keys:

```bash
# Required for all resources and data sources
export ANTHROPIC_API_KEY="sk-ant-..."

# Required for organization management resources (workspaces, environments)
export ANTHROPIC_ADMIN_API_KEY="sk-ant-admin-..."
```

At least one key must be set. `ANTHROPIC_API_KEY` is needed for most resources and data sources. `ANTHROPIC_ADMIN_API_KEY` is needed for organization management resources (`anthropic_workspace`). Both can be set at the same time.

A `.env` file at the project root can hold machine-specific values. **Always source it before running any command:**

```bash
set -a && source .env && set +a
```

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

## Merge Request Process

1. Create your MR and add reviewers. Owners or contributors of this repository must be added as reviewers.
2. Run `make` (formats, lints, tests, installs, and regenerates docs in one step).
3. Run pre-commit hooks `pre-commit run -a`.
4. Once all comments and checklist items have been addressed, your contribution will be merged! Merged MRs will be included in the next release. [Semantic release](https://github.com/semantic-release/semantic-release) will be in charge to construct the Release automatically (Tag, CHANGELOG).

## Checklists for contributions

- [ ] Add [semantics prefix](#semantic-pull-requests) to your commits
- [ ] MR title and description written in English
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

We do Squash Merge during the MRs merge. The title of the MR is the commit title (commit type + scope + short description) and the description of the MR is the commit body.

## Claude Code

This project includes a [Claude Code](https://claude.ai/code) configuration under `.claude/` to help contributors follow Terraform provider best practices.

The [`terraform-skill@antonbabenko`](https://github.com/antonbabenko/terraform-skill) plugin is enabled for this project. It provides guidance and code generation assistance aligned with HashiCorp's Terraform plugin framework conventions — schema design, resource/data source patterns, testing, and documentation generation.

If you use Claude Code, this skill will be automatically active when working in this repository. No additional setup is required.
