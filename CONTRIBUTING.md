# Contributing

When contributing to this repository, please first discuss the change you wish to make via issue,
email, or any other method with the owners of this repository before making a change.

## Local Development Setup

### Build and install the provider

```bash
make install
```

This compiles the provider and installs it via `go install`. The binary location depends on your Go setup — find it with:

```bash
whereis terraform-provider-anthropic
```

### Configure Terraform to use the local binary

Create or edit `~/.terraformrc` with the path found above:

```hcl
provider_installation {
  dev_overrides {
    "registry.terraform.io/ippontech/anthropic" = "/path/to/your/go/bin"
  }
  direct {}
}
```

With `dev_overrides`, `terraform init` is not required — run `terraform plan` directly. Terraform will show a warning about development overrides being in effect; this is expected.

### Set your API key

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
```

### Run acceptance tests
Also
```bash
TF_ACC=1 make testacc
```

## Merge Request Process

1. Create your MR and add reviewers. Owners or contributors of this repository must be added as reviewers.
2. Run pre-commit hooks `pre-commit run -a`.
3. Once all comments and checklist items have been addressed, your contribution will be merged! Merged MRs will be included in the next release. [Semantic release](https://github.com/semantic-release/semantic-release) will be in charge to construct the Release automatically (Tag, CHANGELOG).

## Checklists for contributions

- [ ] Add [semantics prefix](#semantic-pull-requests) to your Commits
- [ ] MR Title and description written in English
- [ ] Run pre-commit hooks `pre-commit run -a`
- [ ] CI is passing (if needed)

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
