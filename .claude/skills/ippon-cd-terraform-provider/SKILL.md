---
name: ippon-cd-terraform-provider
description: Develops Terraform providers in Go. Use when creating a new Terraform provider or updating an existing one.
model: opus
---

# Requirements

Install the following tools:

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.0
- [Go](https://golang.org/doc/install) >= 1.24

With mise:
```shell
mise use terraform@1 go@1
```

# Get started

When starting a new Terraform provider, use [terraform-provider-scaffolding-framework](https://github.com/hashicorp/terraform-provider-scaffolding-framework) GitHub repository.

This repository is a template for a Terraform provider. It is intended as a starting point for creating Terraform providers, containing:

- A resource and a data source (internal/provider/),
- Examples (examples/) and generated documentation (docs/),
- Miscellaneous meta files.

To simplify the bootstrapping of a new provider, clone the scaffolding provider:
```shell
cd /tmp && git clone https://github.com/hashicorp/terraform-provider-scaffolding-framework.git
```

And then, retrieve base resources from the cloned repository to initialize the new provider.

# Adding Dependencies

A Terraform provider uses [Go modules](https://github.com/golang/go/wiki/Modules).
Please see the Go documentation for the most up to date information about using Go modules.

To add a new dependency `github.com/author/dependency` to your Terraform provider, always pin to an explicit version to avoid regressions. Never use `@latest`. First list available versions, then pick the most recent one:

```shell
go list -m -versions github.com/author/dependency
go get github.com/author/dependency@vX.Y.Z
go mod tidy
```

Then commit the changes to `go.mod` and `go.sum`.

# Best practices

Follow the [Terraform plugin framework documentation](https://developer.hashicorp.com/terraform/plugin/framework).

## File naming

Always use snake_case for Terraform file names. For example:
- `data_source.tf` not `data-source.tf`
- `main_resource.tf` not `main-resource.tf`

## Example folder naming

Place examples under `examples/<resource_name>/` without provider prefix. For example:
- `examples/models/` not `examples/anthropic_models/`
- `examples/messages/` not `examples/anthropic_messages/`

## Provider documentation structure

Follow the [HashiCorp registry docs structure](https://developer.hashicorp.com/terraform/registry/providers/docs#directory-structure). The `docs/` folder is exclusively user-facing and published to the Terraform Registry:

```
docs/
├── index.md                      # Required — provider overview and argument reference
├── data-sources/
│   └── <name>.md                 # One file per data source, no provider prefix
├── resources/
│   └── <name>.md                 # One file per resource, no provider prefix
├── functions/
│   └── <name>.md
└── guides/
    └── <guide>.md                # Optional user-facing guides
```

Each `docs/data-sources/<name>.md` and `docs/resources/<name>.md` must include YAML frontmatter:

```markdown
---
page_title: "Anthropic: anthropic_<name>"
subcategory: "<Category>"
description: |-
  One-line description of the resource or data source.
---
```

`docs/index.md` frontmatter:

```markdown
---
page_title: "Provider: Anthropic"
description: |-
  Use the Anthropic Terraform provider to interact with Anthropic APIs.
---
```

**Technical/maintainer documentation** (release process, GPG setup, CI secrets) must NOT go in `docs/` — it would be published to the registry. Place it at the repo root (e.g., `RELEASE.md`, `CONTRIBUTING.md`) instead.

## Version constraints

For providers on major version 0, always use patch-only version constraints to prevent breaking changes from minor upgrades:

```hcl
# Correct — patch only
version = "~> 0.1.0"

# Wrong — allows minor bumps (0.2, 0.3…) which may break
version = "~> 0.1"
```

For providers on major version 1+, `~> 1.0` is acceptable as semver guarantees backwards compatibility within a major version.

## Native Terraform tests

Use [native Terraform tests](https://developer.hashicorp.com/terraform/language/tests) (`terraform test`, requires Terraform >= 1.6) to test example modules end-to-end. Place test files in `tests/` at the project root, one file per example root module.

### File naming and structure

```
tests/
├── provider.tftest.hcl            # Tests examples/provider/
└── <data_source_name>.tftest.hcl  # Tests examples/data-sources/<name>/
```

### Test file anatomy

Each test file may contain (in order):
- Zero or one `test` block — file-level execution settings
- Zero or more `provider` blocks — shared provider configuration
- Zero or one `variables` block — file-level variable defaults
- One or more `run` blocks — each is an independent test case

Always add `test { parallel = true }` at the top of every test file to allow eligible `run` blocks to execute simultaneously:

```hcl
test {
  parallel = true
}

run "models_data_source_returns_results" {
  module {
    source = "../examples/data-sources/models"
  }

  assert {
    condition     = length(output.models) > 0
    error_message = "Expected at least one model to be returned, but got none."
  }

  assert {
    condition     = output.models[0].id != ""
    error_message = "Expected the first model's id to be non-empty."
  }
}
```

Paths in `module { source = "..." }` are relative to the **working directory where `terraform test` runs** (the project root), not the test file's location. Always use `./examples/...` style paths, not `../examples/...`.

### Run block options

| Option | Default | Purpose |
|---|---|---|
| `command` | `apply` | `apply` (integration test) or `plan` (unit test, no real infra) |
| `module` | — | Alternate module to test instead of root module |
| `variables` | — | Variable overrides for this run |
| `assert` | — | Validation conditions (each needs `condition` + `error_message`) |
| `expect_failures` | — | Validate that a custom condition (validation/check block) fails |
| `parallel` | `false` | Mark this run block as eligible for parallel execution |
| `state_key` | — | Share state across run blocks that reference different modules |

Use `command = plan` when you only need to validate logic without creating real infrastructure (no API calls made). Use `command = apply` (the default) for full integration tests.

### Running tests locally

Tests use a locally-built provider via Terraform dev overrides. `terraform init` is required before `terraform test` to install local module references. With `dev_overrides` correctly configured (pointing to the actual binary path), `terraform init` skips the registry entirely for the dev-overridden provider and only installs the local modules. Add these targets to the `GNUmakefile`:

```makefile
.dev.tfrc:
    @printf 'provider_installation {\n  dev_overrides {\n    "registry.terraform.io/ippontech/anthropic" = "%s/bin"\n  }\n  direct {}\n}\n' "$$(go env GOPATH)" > $@

terraform-test: install .dev.tfrc
    TF_CLI_CONFIG_FILE=$(CURDIR)/.dev.tfrc terraform init
    TF_CLI_CONFIG_FILE=$(CURDIR)/.dev.tfrc terraform test
```

Add `.dev.tfrc` to `.gitignore` — it is generated locally and contains a machine-specific path.

Run from the project root:
```shell
ANTHROPIC_API_KEY=<key> make terraform-test
```

`terraform test` automatically discovers `.tftest.hcl` files in the current directory and `tests/` subdirectory.

### Best practices

- **Integration vs. unit:** Default to `command = apply`; use `command = plan` only for logic-only validation
- **Assertion specificity:** Assert exact attribute values when deterministic; use `!= ""` or `length() > 0` for dynamic API data
- **Sensitive credentials:** Pass via environment variables (`ANTHROPIC_API_KEY`), never hardcode in test files
- **Run ordering:** Order `run` blocks so that dependencies are created before they are referenced; destruction runs in reverse order
- **Parallelism:** A `run` block with `parallel = false` (or no `parallel` attribute) acts as a synchronization barrier — subsequent runs wait for it
