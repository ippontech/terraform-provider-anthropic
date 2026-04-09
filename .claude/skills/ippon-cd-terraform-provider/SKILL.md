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

To add a new dependency `github.com/author/dependency` to your Terraform provider:

```shell
go get github.com/author/dependency
go mod tidy
```

Then commit the changes to `go.mod` and `go.sum`.

# Best practices

Follow the [Terraform plugin framework documentation](https://developer.hashicorp.com/terraform/plugin/framework).
