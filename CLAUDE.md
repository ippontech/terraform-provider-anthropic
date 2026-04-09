# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make build       # Compile the provider
make install     # Build and install locally
make fmt         # Format Go code
make lint        # Run golangci-lint
make test        # Run unit tests (120s timeout, 10 parallel workers)
make testacc     # Run acceptance tests (requires TF_ACC=1, 120m timeout)
make generate    # Regenerate docs and format examples
make             # Default: fmt lint install generate
```

Run a single test:
```bash
go test -run TestName -v ./internal/provider/
```

Acceptance tests require `TF_ACC=1` and a real Anthropic API key.

Before committing, run pre-commit hooks:
```bash
pre-commit run -a
```

## Architecture

This is a Terraform provider built with [HashiCorp Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework) v1.13.0.

- `main.go` — entry point; serves the provider at `registry.terraform.io/ippon/anthropic`
- `internal/provider/provider.go` — provider registration; `Resources()` and `DataSources()` methods list all implemented resources and data sources
- `internal/provider/` — all resources, data sources, and their tests live here
- `examples/provider/` — example Terraform configs used by `terraform-plugin-docs` to generate `docs/`
- `tools/tools.go` — build-time tool imports only (not runtime)

### Adding a resource or data source

1. Create `internal/provider/<name>_resource.go` (or `_data_source.go`)
2. Implement the `resource.Resource` (or `datasource.DataSource`) interface
3. Register the factory function in `Resources()` (or `DataSources()`) in `internal/provider/provider.go`
4. Add an example config under `examples/resources/<name>/` (or `examples/data-sources/<name>/`)
5. Run `make generate` to regenerate docs

### Testing pattern

- `internal/provider/provider_test.go` defines `testAccProtoV6ProviderFactories` used by all acceptance tests
- Unit tests: no special env vars needed
- Acceptance tests: use `resource.Test(t, resource.TestCase{...})` with `TF_ACC=1`

## Conventions

Commits and MR titles must follow [conventional commits](https://www.conventionalcommits.org/):
- `feat:` new features
- `fix:` bug fixes
- `docs:` documentation and examples
- `refactor:` code refactoring
- `test:` tests
- `ci:` CI changes
- `chore:` maintenance

PRs are squash-merged; the MR title becomes the commit message.
