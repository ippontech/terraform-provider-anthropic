# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Architecture

This is a Terraform provider built with [HashiCorp Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework) v1.13.0.

- `main.go` — entry point; serves the provider at `registry.terraform.io/ippontech/anthropic`
- `internal/provider/provider.go` — provider registration; `Resources()` and `DataSources()` methods list all implemented resources and data sources
- `internal/services/` — all resources and data sources, organized by Anthropic service (one subdirectory per service)
- `examples/provider/` — example Terraform configs used by `terraform-plugin-docs` to generate `docs/`
- `tests/` — Terraform native tests (`.tftest.hcl` files), one per resource/data source
- `tools/tools.go` — build-time tool imports only (not runtime)

### Internal package layout

```
internal/
  admin/           — HTTP client for Admin API (/v1/organizations/*)
  acctest/         — shared acceptance test helpers (ProtoV6ProviderFactories, PreCheck)
  errors/          — nil-client guards for Configure methods (import alias: providerrors)
  providerdata/    — ProviderData struct passed to every resource/data source Configure call
  retry/           — multipart upload with 5xx retry (import alias: provretry)
  provider/        — AnthropicProvider implementation only (provider.go)
  services/
    agents/        — anthropic_agent resource + agent/agents data sources
    environments/  — anthropic_environment resource + environment/environments data sources
    messages/      — anthropic_message resource + count_tokens data source
    models/        — model/models data sources
    skills/        — skill/skill_version resources + skill/skills/skill_version/skill_versions data sources
    workspaces/    — anthropic_workspace + anthropic_workspace_member resources + workspace/workspaces/workspace_member/workspace_members data sources (shared test helpers in workspacetest.go)
```

### Implemented resources and data sources

**Resources:**
- `anthropic_message` (`internal/services/messages/message_resource.go`) — calls the Messages API; write-only, immutable (no read/update/delete)
- `anthropic_agent` (`internal/services/agents/agent_resource.go`) — manages Managed Agents (create/read/update/delete)
- `anthropic_environment` (`internal/services/environments/environment_resource.go`) — manages environments
- `anthropic_skill` (`internal/services/skills/skill_resource.go`) — manages skills
- `anthropic_skill_version` (`internal/services/skills/skill_version_resource.go`) — manages skill versions
- `anthropic_workspace` (`internal/services/workspaces/workspace_resource.go`) — manages workspaces (admin API)
- `anthropic_workspace_member` (`internal/services/workspaces/workspace_member_resource.go`) — assigns a user to a workspace with a given role (admin API); composite ID `<workspace_id>:<user_id>`; `workspace_billing` role rejected at plan time

**Data sources:**
- `anthropic_model` (`internal/services/models/model_data_source.go`) — fetches a single model by ID
- `anthropic_models` (`internal/services/models/models_data_source.go`) — lists all available models
- `anthropic_count_tokens` (`internal/services/messages/count_tokens_data_source.go`) — counts tokens for a given prompt
- `anthropic_agent` (`internal/services/agents/agent_data_source.go`) — fetches a single agent
- `anthropic_agents` (`internal/services/agents/agents_data_source.go`) — lists all agents
- `anthropic_environment` (`internal/services/environments/environment_data_source.go`) — fetches a single environment
- `anthropic_environments` (`internal/services/environments/environments_data_source.go`) — lists all environments
- `anthropic_skill` (`internal/services/skills/skill_data_source.go`) — fetches a single skill
- `anthropic_skills` (`internal/services/skills/skills_data_source.go`) — lists all skills
- `anthropic_skill_version` (`internal/services/skills/skill_version_data_source.go`) — fetches a single skill version
- `anthropic_skill_versions` (`internal/services/skills/skill_versions_data_source.go`) — lists all skill versions
- `anthropic_workspace` (`internal/services/workspaces/workspace_data_source.go`) — fetches a single workspace by ID (admin API)
- `anthropic_workspaces` (`internal/services/workspaces/workspaces_data_source.go`) — lists all workspaces (admin API, transparent pagination)
- `anthropic_workspace_member` (`internal/services/workspaces/workspace_member_data_source.go`) — fetches a single workspace member by workspace ID and user ID (admin API)
- `anthropic_workspace_members` (`internal/services/workspaces/workspace_members_data_source.go`) — lists all members of a workspace (admin API), with transparent pagination
- `anthropic_workspace_rate_limits` (`internal/services/workspaces/workspace_rate_limits_data_source.go`) — lists workspace-level rate-limit overrides for a workspace (admin API), with transparent pagination and an optional `group_type` filter; only entries with at least one override are returned

### Adding a resource or data source

1. Create the file under `internal/services/<service>/<name>_resource.go` (or `_data_source.go`), using `package <service>`
2. Implement the `resource.Resource` (or `datasource.DataSource`) interface
3. Register the factory function in `Resources()` (or `DataSources()`) in `internal/provider/provider.go`
4. Add an example config under `examples/resources/<name>/` (or `examples/data-sources/<name>/`)
5. Add a template under `templates/resources/<name>.md.tmpl` (or `templates/data-sources/<name>.md.tmpl`) — **required** to set a non-empty `subcategory` (e.g. `"Agents"`, `"Messages"`, `"Models"`); without it `make generate` produces `subcategory: ""` and the resource appears ungrouped on the Terraform Registry
6. Add a Terraform native test under `tests/<name>.tftest.hcl`
7. Run `make generate` to regenerate docs

### Testing pattern

**Go acceptance tests** (`internal/services/<service>/`):
- Test files use `package <service>_test` (external test package), except tests that access unexported symbols which use `package <service>`
- **File naming:** use `<name>_test.go` (e.g. `workspace_rate_limits_data_source_test.go`). Do **not** suffix unit-test files with `_unit_test.go` — acceptance test functions are already disambiguated by the `TestAccXxx` prefix, and unit tests use `TestXxx`.
- **Important:** acceptance tests (those importing `internal/acctest`) MUST live in `package <service>_test` (external) to avoid an import cycle (`acctest` → `provider` → `<service>`). When unit tests need internal types AND a service has acceptance tests, split them into two files: one in `package <service>` (internal, unit) and one in `package <service>_test` (external, acceptance).
- `internal/acctest/acctest.go` exports `ProtoV6ProviderFactories`, `PreCheck` (standard API key), and `PreCheckAdmin` (admin API key) used by all acceptance tests
- `internal/services/workspaces/workspacetest.go` defines shared unit-test helpers (`newTestAdminClient`, `workspaceFixture`, `pageData`, `fetchAllPages`); reuse them in any new workspaces unit test instead of constructing `admin.Client` inline or duplicating pagination loops
- **Admin API acceptance tests for read-only data sources:** target the existing `terraform-tests` workspace (`wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm`) and gate on `acctest.PreCheckAdmin`. Resource tests (create/update/delete) on the Admin API remain blocked by [#58](https://github.com/ippontech/terraform-provider-anthropic/issues/58) because we do not yet have a dedicated test organization, so do not create new resources via Admin API in tests.
- Admin API **Terraform native tests**: use `command = plan` (not the default `apply`) until a test org is available; this validates schema without making live API calls. Read-only data source native tests may run against the `terraform-tests` workspace ID above.
- Unit tests: no special env vars needed
- Acceptance tests: use `resource.Test(t, resource.TestCase{...})` with `TF_ACC=1`

**Terraform native tests** (`tests/`):
- One `.tftest.hcl` file per resource/data source (e.g. `tests/message.tftest.hcl`)
- Each test references the corresponding example config as its module source (e.g. `source = "./examples/resources/message"`)
- Tests use `assert` blocks to verify computed attribute values
- The top-level `test { parallel = true }` block opts every `run` block in the file into parallel execution; do not repeat `parallel = true` inside individual `run` blocks (it's redundant)
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

Run tests for a single service:
```bash
go test -run TestName -v ./internal/services/agents/
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

Every resource and data source `Configure` method must:
1. Import `providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"` and cast `req.ProviderData` to `*providerdata.ProviderData`
2. Guard against a nil client using helpers from `internal/errors/` (import alias `providerrors`). Never use an inline `if pd.Client == nil` check.

```go
import (
    providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
    providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
)

// Standard resource
pd, ok := req.ProviderData.(*providerdata.ProviderData)
// ... (ok check) ...
if !providerrors.RequireResourceAPIClient(pd.Client, &resp.Diagnostics) {
    return
}
r.client = pd.Client

// Standard data source  →  providerrors.RequireDataSourceAPIClient
// Admin resource        →  providerrors.RequireAdminResourceClient(pd.AdminClient, ...)
// Admin data source     →  providerrors.RequireAdminDataSourceClient(pd.AdminClient, ...)
```

### Shared helpers

- `internal/admin/` — HTTP client for Admin API; import as `"github.com/ippontech/terraform-provider-anthropic/internal/admin"`
- `internal/errors/` (import alias `providerrors`) — nil-client guards for `Configure` methods
- `internal/providerdata/` (import alias `providerdata`) — `ProviderData` struct
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
