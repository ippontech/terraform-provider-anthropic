---
name: ippon-cd-terraform-provider
description: Develops Terraform providers in Go. Use when creating a new Terraform provider or updating an existing one.
model: sonnet
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
