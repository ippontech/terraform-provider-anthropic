# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Architecture

This is a Terraform provider built with [HashiCorp Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework) v1.13.0.

- `main.go` — entry point; serves the provider at `registry.terraform.io/ippontech/anthropic`
- `internal/provider/provider.go` — provider registration; `Resources()` and `DataSources()` methods list all implemented resources and data sources
- `internal/provider/` — all resources, data sources, and their tests live here
- `examples/provider/` — example Terraform configs used by `terraform-plugin-docs` to generate `docs/`
- `tests/` — Terraform native tests (`.tftest.hcl` files), one per resource/data source
- `tools/tools.go` — build-time tool imports only (not runtime)

### Implemented resources and data sources

**Resources:**
- `anthropic_message` (`internal/provider/message_resource.go`) — calls the Messages API; write-only, immutable (no read/update/delete)
- `anthropic_agent` (`internal/provider/agent_resource.go`) — manages Managed Agents (create/read/update/delete)

**Data sources:**
- `anthropic_model` (`internal/provider/model_data_source.go`) — fetches a single model by ID
- `anthropic_models` (`internal/provider/models_data_source.go`) — lists all available models
- `anthropic_count_tokens` (`internal/provider/count_tokens_data_source.go`) — counts tokens for a given prompt

### Adding a resource or data source

1. Create `internal/provider/<name>_resource.go` (or `_data_source.go`)
2. Implement the `resource.Resource` (or `datasource.DataSource`) interface
3. Register the factory function in `Resources()` (or `DataSources()`) in `internal/provider/provider.go`
4. Add an example config under `examples/resources/<name>/` (or `examples/data-sources/<name>/`)
5. Add a template under `templates/resources/<name>.md.tmpl` (or `templates/data-sources/<name>.md.tmpl`) — **required** to set a non-empty `subcategory` (e.g. `"Agents"`, `"Messages"`, `"Models"`); without it `make generate` produces `subcategory: ""` and the resource appears ungrouped on the Terraform Registry
6. Add a Terraform native test under `tests/<name>.tftest.hcl`
7. Run `make generate` to regenerate docs

### Testing pattern

**Go acceptance tests** (`internal/provider/`):
- `internal/provider/provider_test.go` defines `testAccProtoV6ProviderFactories` used by all acceptance tests
- Unit tests: no special env vars needed
- Acceptance tests: use `resource.Test(t, resource.TestCase{...})` with `TF_ACC=1`

**Terraform native tests** (`tests/`):
- One `.tftest.hcl` file per resource/data source (e.g. `tests/message.tftest.hcl`)
- Each test references the corresponding example config as its module source (e.g. `source = "./examples/resources/message"`)
- Tests use `assert` blocks to verify computed attribute values
- All test blocks set `parallel = true`
- Run with `make terraform-test` (builds and installs the provider first via `.dev.tfrc`)

## Environment

A `.env` file at the project root sets machine-specific variables (e.g., `OTEL_TRACES_EXPORTER=`). **Always source it before running any command** to avoid env-related failures:

```bash
set -a && source .env && set +a
```

## Commands

```bash
make build          # Compile the provider
make install        # Build and install locally
make fmt            # Format Go code
make lint           # Run golangci-lint
make test           # Run unit tests (120s timeout, 10 parallel workers)
make testacc        # Run Go acceptance tests (requires TF_ACC=1, 120m timeout)
make terraform-test # Run Terraform native tests (builds provider, uses .dev.tfrc)
make generate       # Regenerate docs and format examples
make                # Default: fmt lint test install generate
```

**After implementing any feature or bug fix, always run `make` (alias for `make default`) before committing.** It formats code, runs the linter, reinstalls the provider, and regenerates docs in one step.

Run a single Go test:
```bash
go test -run TestName -v ./internal/provider/
```

Go acceptance tests require `TF_ACC=1` and a real Anthropic API key. Terraform native tests also require a real API key and a locally installed provider.

Before committing, run pre-commit hooks:
```bash
pre-commit run -a
```

## Provider coding conventions

### API key model

The provider has two optional API keys — at least one must be configured:

| Key | Provider arg | Env var | Client field | Used by |
|---|---|---|---|---|
| Standard | `api_key` | `ANTHROPIC_API_KEY` | `pd.Client` | All standard resources and data sources |
| Admin | `admin_api_key` | `ANTHROPIC_ADMIN_API_KEY` | `pd.AdminClient` | Organization endpoints (`/v1/organizations/*`, e.g. workspaces) |

### Configure method pattern

Every resource and data source `Configure` method must guard against a nil client using helpers from `internal/errors/` (import alias `providerrors`). Never use an inline `if pd.Client == nil` check.

```go
import providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"

// Standard resource
if !providerrors.RequireResourceAPIClient(pd.Client, &resp.Diagnostics) {
    return
}
r.client = pd.Client

// Standard data source  →  providerrors.RequireDataSourceAPIClient
// Admin resource        →  providerrors.RequireAdminResourceClient(pd.AdminClient, ...)
// Admin data source     →  providerrors.RequireAdminDataSourceClient(pd.AdminClient, ...)
```

### Shared helpers

Helpers shared across resources/data sources live in `internal/`, not `internal/provider/`:

- `internal/errors/` (import alias `providerrors`) — nil-client guards for `Configure` methods
- `internal/retry/` (import alias `provretry`) — multipart file upload with automatic 5xx retry; use `provretry.MultipartUpload` for any resource that uploads files to the API (the Anthropic SDK cannot retry streaming multipart bodies on its own)

### Version constraints

Provider is on major version 1.x. Always use `~> 1.0` in example configs — never `~> 0.1.0`.

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

### Go unit tests

After any bug fix, refactoring, or new helper added under `internal/`, write or update Go unit tests in the same package before considering the task done. Unit tests live next to the code they test (e.g. `internal/retry/multipart_test.go` for `internal/retry/multipart.go`). Run `make test` to verify they pass.
