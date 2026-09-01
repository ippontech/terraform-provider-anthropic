# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Architecture

This is a Terraform provider built with [HashiCorp Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework) v1.19.0.

The Anthropic Go SDK is pinned to **v1.67.0** and must not be bumped past it in a plain `chore(deps)` change. v1.68.0 renames skill fields (`display_title` → `display_name`, `latest_version` → `latest_version_id`) and drops `directory` and `version` from the skill-version response — but **the deployed API still sends the v1.67.0 names**. Verified against the live API on 2026-09-01 under `anthropic-beta: skills-2025-10-02` (the only skills beta constant, unchanged in v1.68.0): `GET /v1/skills` returns `display_title` and `latest_version`, and `GET /v1/skills/{id}/versions` returns `directory` and `version`. v1.67.0's struct tags match that payload field for field.

So v1.68.0 is **ahead of the API, not behind it**: bumping now would silently deserialise `""` into `display_name` and `latest_version_id` (the API sends the old keys, which v1.68.0 no longer maps) and would drop two attributes the API still returns. Going past v1.67.0 needs the API rename to ship first, then a documented migration. `renovate.json` enforces this with an `allowedVersions: "<1.68.0"` rule on the SDK, so no bump PR can be opened (patch releases within `1.67.x` still flow) — the note in `.github/renovate.md` explains why guidance alone was not enough. Re-run the check above before revisiting, and remove the rule only as part of [#192](https://github.com/ippontech/terraform-provider-anthropic/issues/192).

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
  admintest/       — shared unit-test helper: admintest.NewClient(t, srv) builds an admin.Client pointed at an httptest server
  acctest/         — shared acceptance test helpers (ProtoV6ProviderFactories, PreCheck)
  errors/          — nil-client guards for Configure methods (import alias: providerrors)
  providerdata/    — ProviderData struct passed to every resource/data source Configure call
  retry/           — multipart upload with 5xx retry (import alias: provretry)
  provider/        — AnthropicProvider implementation only (provider.go)
  services/
    agents/        — anthropic_agent resource + agent/agents data sources
    apikeys/       — anthropic_api_key resource (import/update/delete only; no create) + api_key/api_keys data sources
    environments/  — anthropic_environment resource + environment/environments data sources
    messages/      — anthropic_message resource + count_tokens data source
    models/        — model/models data sources
    organizations/ — organization data source (anthropic_organization; admin API GET /v1/organizations/me, no input) + organization_member/organization_members data sources (admin API GET /v1/organizations/users[/{id}]; members are Users)
    skills/        — skill/skill_version resources + skill/skills/skill_version/skill_versions data sources
    workspaces/    — anthropic_workspace + anthropic_workspace_member resources + workspace/workspaces/workspace_member/workspace_members data sources (shared test helpers in workspacetest.go)
    vaults/        — anthropic_vault + anthropic_vault_credential resources (Beta managed-agents API; vault_credential uses write-only secret attributes)
```

### Implemented resources and data sources

**Resources:**
- `anthropic_message` (`internal/services/messages/message_resource.go`) — calls the Messages API; write-only, immutable (no read/update/delete)
- `anthropic_agent` (`internal/services/agents/agent_resource.go`) — manages Managed Agents (create/read/update/delete)
- `anthropic_environment` (`internal/services/environments/environment_resource.go`) — manages environments; supports `archive_on_destroy` (archives instead of deleting on destroy when true)
- `anthropic_skill` (`internal/services/skills/skill_resource.go`) — manages skills
- `anthropic_skill_version` (`internal/services/skills/skill_version_resource.go`) — manages skill versions
- `anthropic_workspace` (`internal/services/workspaces/workspace_resource.go`) — manages workspaces (admin API)
- `anthropic_workspace_member` (`internal/services/workspaces/workspace_member_resource.go`) — assigns a user to a workspace with a given role (admin API); composite ID `<workspace_id>:<user_id>`; `workspace_billing` role rejected at plan time
- `anthropic_api_key` (`internal/services/apikeys/api_key_resource.go`) — import-only resource; manages lifecycle of existing API keys (rename, deactivate) via Admin API; Create always errors with a message to use `terraform import`; Delete sets `status: inactive`
- `anthropic_vault` (`internal/services/vaults/vault_resource.go`) — manages vaults (Beta managed-agents API; `client.Beta.Vaults`); named container for MCP credentials; supports `archive_on_destroy`
- `anthropic_vault_credential` (`internal/services/vaults/vault_credential_resource.go`) — manages a credential inside a vault (`client.Beta.Vaults.Credentials`); three auth `type`s (`static_bearer`, `mcp_oauth` with nested `refresh`/`token_endpoint_auth`, `environment_variable` with `networking`); secret material is **write-only** (see write-only convention below); structural/immutable fields are RequiresReplace (`vault_id`, `type`, `mcp_server_url`, `secret_name`, and the `refresh` block's `client_id`/`token_endpoint`/`resource`/`token_endpoint_auth.type` — the update API cannot modify these, and its `token_endpoint_auth` union has no `none` variant); supports `archive_on_destroy`

**Data sources:**
- `anthropic_model` (`internal/services/models/model_data_source.go`) — fetches a single model by ID
- `anthropic_models` (`internal/services/models/models_data_source.go`) — lists all available models
- `anthropic_organization` (`internal/services/organizations/organization_data_source.go`) — fetches the organization tied to the admin key (admin API `GET /v1/organizations/me`); takes no input; exposes `id`, `name`, `type`
- `anthropic_organization_member` (`internal/services/organizations/organization_member_data_source.go`) — fetches a single organization member (User) by user ID (admin API `GET /v1/organizations/users/{user_id}`); input `id`; exposes `email`, `name`, `role`, `added_at`, `type`. No managed "user" resource exists to anchor an unknown at plan time, so the example chains off `organization_members` (`members[0].id`) to resolve a real ID — a constant `id` would trigger a live 404 during the native `command = plan` test
- `anthropic_organization_members` (`internal/services/organizations/organization_members_data_source.go`) — lists all organization members (admin API `GET /v1/organizations/users`) with an optional `email` filter and transparent cursor pagination
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
- `anthropic_api_key` (`internal/services/apikeys/api_key_data_source.go`) — fetches a single API key by ID (admin API); reuses `APIKeyResourceModel` and `mapAPIKeyToState` from the resource file
- `anthropic_api_keys` (`internal/services/apikeys/api_keys_data_source.go`) — lists API keys (admin API) with optional `status` and `workspace_id` filters; transparent pagination

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
- **Important:** acceptance tests (those importing `internal/acctest`) MUST live in `package <service>_test` (external) to avoid an import cycle (`acctest` → `provider` → `<service>`). When unit tests need internal types AND a service has acceptance tests, split them into two files: one in `package <service>` (internal, unit) and one in `package <service>_test` (external, acceptance). When the split is for a **single** resource/data source (so the `<name>` prefix can't disambiguate, unlike `workspace` vs `workspaces`), give the internal-package file an `_internal_test.go` suffix and keep the plain `_test.go` for the external acceptance file — e.g. `agent_resource_internal_test.go` (internal) + `agent_resource_test.go` (acceptance), and `organization_data_source_internal_test.go` (internal) + `organization_data_source_test.go` (acceptance). The suffix names the file by what truly distinguishes it — the **internal package** (unexported access, mock `httptest` server, no live API) — not a test type: it may hold mapping, pagination, filter/query-param construction, and 404 coverage. The plain `<name>_test.go` always holds the external acceptance tests; **never** use an `_acc_test.go` suffix, since the `TestAcc` prefix already disambiguates acceptance functions. Exercise the API-response → state mapping directly by extracting a `map<Resource>ToState` helper the internal test calls.
- `internal/acctest/acctest.go` exports `ProtoV6ProviderFactories`, `PreCheck` (standard API key), and `PreCheckAdmin` (admin API key) used by all acceptance tests
- **Test workspace isolation.** All acceptance/native tests operate within the dedicated `terraform-tests` workspace so test resources never touch production. The standard `ANTHROPIC_API_KEY` used for tests is **scoped to `terraform-tests`**, so standard-API resources created during tests land there automatically. Admin API tests (organization-wide) target it by ID via the single shared constant `acctest.TerraformTestsWorkspaceID` — use that constant, never re-hardcode the `wrkspc_...` literal in Go tests.
- For an admin `*admin.Client` pointed at an `httptest` server in any package's unit tests, use `admintest.NewClient(t, srv)` (`internal/admintest`) — do not construct `admin.Client` inline or duplicate the helper per package
- `internal/services/workspaces/workspacetest.go` defines workspaces-specific shared helpers (`workspaceFixture`, `pageData`, `fetchAllPages`, plus a thin `newTestAdminClient` that delegates to `admintest.NewClient`); reuse them in any new workspaces unit test instead of duplicating pagination loops
- **Admin API acceptance tests for read-only data sources:** target the `terraform-tests` workspace via `acctest.TerraformTestsWorkspaceID` and gate on `acctest.PreCheckAdmin`. These are **smoke** tests (the live workspace's data isn't deterministic, so assert attribute presence like `members.#`, not specific values); they **complement, not replace**, the httptest-based unit tests that deterministically cover pagination, query-param/filter construction, mapping edge cases, and 404 handling. Covered so far: `workspace`, `workspaces`, `workspace_members`, `workspace_rate_limits`, `organization`, `organization_member`, `organization_members`, `api_keys`. (`organization_member`'s smoke test chains off `organization_members` to resolve a real user ID, since no constant ID is deterministically valid.) Resource tests (create/update/delete) on the Admin API remain blocked by [#58](https://github.com/ippontech/terraform-provider-anthropic/issues/58) because we do not yet have a dedicated test organization, so do not create new resources via Admin API in tests.
- Admin API **Terraform native tests**: use `command = plan` (not the default `apply`) until a test org is available; this validates schema without making live API calls. Read-only data source native tests may run against the `terraform-tests` workspace ID above.
- **Standard-API resources support full CRUD acceptance tests.** The #58 blocker is Admin-API-only. Vaults and vault credentials use the standard `ANTHROPIC_API_KEY` (gate on `acctest.PreCheck`) and are workspace-scoped to that key's workspace — there is no `workspace_id` create param, so they land wherever the key is scoped (the test key is scoped to `terraform-tests`; see the test-workspace-isolation note above). Vaults are billed only at runtime, so create/destroy is free. **Vault credentials are not validated until session runtime**, so acceptance tests create them with placeholder secrets and unreachable `mcp_server_url`s; the full CRUD lifecycle runs without ever starting a session. Their native test (`tests/vault_credential.tftest.hcl`) therefore applies for real (the default command), unlike the Admin-API native tests that are pinned to `command = plan`. `archive_on_destroy` tests for a credential must set `archive_on_destroy = true` on the parent vault too (a hard-deleted vault cascade-deletes the credential, leaving nothing to assert on).
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

After upgrading Go via mise, run `go clean -cache` before `make` to clear stale build artifacts. Without this, golangci-lint's typecheck step fails with a "version does not match go tool version" error because cached objects carry the old Go version tag.

## Commands

```bash
make build          # Compile the provider
make install        # Build and install locally
make fmt            # Format Go code
make tidy-check     # Fail if go.mod/go.sum are not tidy (go mod tidy -diff)
make lint           # Run golangci-lint
make test           # Run unit tests (120s timeout, 10 parallel workers)
make testacc        # Run Go acceptance tests (requires TF_ACC=1, 120m timeout)
make terraform-test # Run Terraform native tests (builds provider, uses .dev.tfrc)
make generate       # Regenerate docs and format examples
make                # Default: fmt tidy-check lint test install generate
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

- `internal/admin/` — HTTP client for Admin API; import as `"github.com/ippontech/terraform-provider-anthropic/internal/admin"`. `DoRequest` retries with exponential backoff plus jitter, honouring the `x-should-retry` override and the `retry-after-ms` / `retry-after` headers. **What gets retried depends on the method.** Idempotent requests (`GET`, `DELETE`, and the rest of the RFC 9110 set) replay on connection errors, on a response body that stops arriving mid-read, and on the transient statuses the Anthropic SDK retries (408, 409, 429, 5xx). A `POST` replays **only on 429** — the one answer that states the call was not processed; on a 5xx, a 409 or a dropped connection the write may already have landed, and a second `POST /v1/organizations/workspaces` would create a duplicate workspace (names are not unique) while a second member-add would burn the budget on a 409 and leave the membership untracked. An explicit `x-should-retry` header from the server still wins in both directions. Retries are **opt-in**: `admin.NewClient` sets `MaxRetries` to `DefaultMaxRetries` (2), while the zero-value `Client` built by `admintest.NewClient` performs a single attempt, so unit tests stay fast and deterministic. Tests that do exercise retrying should set `MaxRetries` along with `BaseRetryDelay`/`MaxRetryDelay` (both default when zero) to keep delays in the millisecond range, and their stub must **not** send `retry-after` / `retry-after-ms`: a server-supplied delay deliberately bypasses both knobs (shortening it would only earn another 429) and is bounded only by the 60s `maxRetryAfter` ceiling
- `internal/errors/` (import alias `providerrors`) — nil-client guards for `Configure` methods
- `internal/providerdata/` (import alias `providerdata`) — `ProviderData` struct
- `internal/retry/` (import alias `provretry`) — multipart file upload with automatic 5xx retry; use `provretry.MultipartUpload(ctx, filePaths, bundleRoot, dirName, fn)` for any resource that uploads files to the API (the Anthropic SDK cannot retry streaming multipart bodies on its own). Each file's multipart name is `dirName + "/" + <path relative to bundleRoot>` (forward-slash normalised), so nested subdirectories inside a bundle are preserved on upload. Derive `bundleRoot` and `dirName` with `provretry.DeriveBundleRoot(filePaths)` — it returns the longest shared parent, which is order-independent (necessary because `fileset()` returns lexically sorted paths and a nested file like `Assets/icon.png` may sort before `SKILL.md`). Files outside `bundleRoot`, or a path equal to `bundleRoot`, are rejected explicitly

### archive_on_destroy pattern

Resources where the API supports archiving (non-destructive) as an alternative to hard-delete should expose an `archive_on_destroy` bool rather than a separate archive resource. Follow this shape exactly:

```go
// Schema attribute
"archive_on_destroy": schema.BoolAttribute{
    Optional:            true,
    Computed:            true,
    Default:             booldefault.StaticBool(false),
    MarkdownDescription: "If `true`, destroying this resource archives it instead of permanently deleting it. Default: `false`.",
    PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
},

// Delete method
if data.ArchiveOnDestroy.ValueBool() {
    _, err := r.client.Beta.<Resource>.Archive(ctx, data.ID.ValueString(), ...)
    // handle err
    return
}
_, err := r.client.Beta.<Resource>.Delete(ctx, data.ID.ValueString(), ...)

// Read method — default to false on import (field is not in API response)
if data.ArchiveOnDestroy.IsNull() || data.ArchiveOnDestroy.IsUnknown() {
    data.ArchiveOnDestroy = types.BoolValue(false)
}
```

Acceptance tests whose `CheckDestroy` verifies archive behaviour must also hard-delete the environment afterwards to avoid dangling archived resources in the test org:

```go
func testAccCheck<Resource>ArchivedAndCleanup(s *terraform.State) error {
    // 1. Get — assert ArchivedAt != ""
    // 2. Delete — permanently remove to avoid accumulation
}
```

### jsontypes.Normalized for JSON string attributes

Any schema attribute that stores a JSON string and is `Optional` (not `Computed`) must use `jsontypes.NormalizedType{}` as its `CustomType` and `jsontypes.Normalized` as the Go model field type. Plain `types.String` causes "Provider produced inconsistent result after apply" whenever the API response JSON differs from the user's `jsonencode()` output in key order or whitespace.

```go
// Schema attribute
"input_schema": schema.StringAttribute{
    Optional:   true,
    CustomType: jsontypes.NormalizedType{},
    ...
},

// Model field
InputSchema jsontypes.Normalized `tfsdk:"input_schema"`

// Attr-type map
"input_schema": jsontypes.NormalizedType{}

// State mapping — use RawJSON() to avoid double-marshal artifacts
inputSchema := jsontypes.NewNormalizedNull()
if raw := t.SomeField.RawJSON(); raw != "" && raw != "null" {
    inputSchema = jsontypes.NewNormalizedValue(raw)
}
```

Import: `"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"` (dependency: `terraform-plugin-framework-jsontypes v0.2.0`).

Do **not** use `json.Marshal(sdkStruct)` to populate a string attribute in state — SDK upgrades can change struct field order or add new fields, silently breaking plan/apply consistency.

### Write-only secret attributes

For sensitive input that must never be persisted to state (tokens, secrets), use **write-only attributes** (plugin-framework v1.11+, requires Terraform ≥1.11). Reference implementation: `internal/services/vaults/vault_credential_resource.go`.

```go
// Schema: WriteOnly cannot be Computed; must be Optional (or Required).
"token": schema.StringAttribute{Optional: true, WriteOnly: true, Sensitive: true, ...},
```

Rules:
- Read write-only values from `req.Config` (NOT `req.Plan`/`req.State`) in Create/Update — e.g. `req.Config.GetAttribute(ctx, path.Root("token"), &v)`; nested via `path.Root("refresh").AtName("refresh_token")`.
- Never write them into state and never populate them from API responses in the Read/map step.
- Terraform can't diff a write-only value, so pair it with a normal `_wo_version` Int64 attribute (stored in state, e.g. `token_wo_version`). Bumping the version is what triggers an Update that re-reads and re-pushes the secret from config (rotation).
- Require the `_wo_version` attribute (via a `ConfigValidator`) whenever a write-only secret is configured. Otherwise, if it is left null, the Update never fires and changing the secret in config is a silent no-op. See `vaultCredentialConfigValidator`.
- In Update, re-push the auth/secret payload on **any** mutable-field diff, not just a `_wo_version` bump — otherwise a change to a non-secret mutable field (e.g. `expires_at`) is dropped because the API response then overwrites the planned value, yielding "Provider produced inconsistent result after apply". Mark truly-immutable fields `RequiresReplace` so they never reach Update.

### ConfigValidator Unknown handling

In a `ConfigValidator`, treat Unknown values (unresolved `var`/output refs at plan time) as neither set nor missing, or otherwise-valid configs fail spuriously. Use helpers: `isMissing(v) = v.IsNull() && !v.IsUnknown()` for required checks, `isSet(v) = !v.IsNull() && !v.IsUnknown()` for conflicting-attribute checks. Reference: `vaultCredentialConfigValidator`.

### Map attributes with PATCH semantics (metadata)

The Anthropic API's `metadata` field uses PATCH semantics: omitted keys are preserved, a key set to `null` is deleted. A plain `if !plan.Metadata.IsNull() { params.Metadata = ... }` therefore **cannot clear** keys removed in config — the API keeps them and the response overwrites the planned value → "Provider produced inconsistent result after apply".

Use `buildMetadataPatch(ctx, plan, state)` (in `internal/services/vaults/vault_resource.go`): it upserts planned keys and sets keys removed since prior state to `nil`, returning a `map[string]any` sent via `params.SetExtraFields(map[string]any{"metadata": patch})` (the typed `map[string]string` field can't carry per-key nulls). The same drift applies to nullable scalars like `display_name`: send `param.Null[string]()` to clear, not an omitted field.

### Read-after-write consistency on the vaults API

`POST /v1/vaults/{id}` returns the updated object, but a `GET` issued immediately after can still return the **pre-update** object. Measured against the live API on 2026-09-01: stale at t+493ms, converged between 520ms and 1.04s across three trials, with every field back at its prior value (`display_name` included, so it is a stale read rather than a `buildMetadataPatch` artefact). A `GET` straight after a **create** is consistent, so only the update path is affected.

Left alone, Terraform's post-apply refresh lands inside that window and the next plan shows a phantom in-place update — this is what made `TestAccVaultResource_update` flaky. `vault_resource.go` therefore calls `awaitVaultUpdateVisible` after a successful update: it polls `Get` until `updated_at` is no older than the timestamp the write returned. The wait is **best-effort** — a read error, a timeout or a cancelled context returns without a diagnostic, because the write already succeeded and failing the apply would turn a cosmetic staleness window into a hard error.

Two details of that loop are load-bearing. The ceiling (5s) and interval (200ms) are **consts passed as arguments**, not package-level `var`s: the unit tests need to shrink them to milliseconds, and the vaults acceptance tests exercise the real `Update` in the same test binary, so a mutable global would race the moment any vault test opted into `t.Parallel()` (`make test` runs with `-parallel=10`). And a `401`, `403` or `404` from the poll (`isTerminalVaultReadError`) bails out immediately instead of retrying to the deadline — a vault deleted out-of-band, or a key that lost access to it, will never converge, and retrying would stall the apply for seconds. Every other failure keeps polling, since it says nothing about visibility.

Compare the two timestamps with a **non-strict** comparison — `!vault.UpdatedAt.Before(writtenAt)`, not `vault.UpdatedAt.After(writtenAt)`: an `updated_at` **equal** to the write timestamp must count as visible, or the loop always times out. `anthropic_vault_credential` has the same structural exposure but no observed flakiness, and its endpoint was not probed — check before assuming it needs the same wait.

### PII attributes and example outputs

Do **not** mark user PII attributes (`email`, `name`, etc.) as `Sensitive: true` in a data source/resource schema. They are the legitimate product of a lookup, not credentials, and schema-level sensitivity forces every consumer into `sensitive = true` outputs or `nonsensitive()` wrappers. This matches the GitLab provider, whose `gitlab_user` resource and `gitlab_user`/`gitlab_users` data sources mark only `password` sensitive, never `email`/`name`. Reserve `Sensitive: true` for true secrets (see write-only attributes above).

Instead, prevent PII from leaking into CI logs at the **example-output** layer: any example `output` that surfaces an email/name must set `sensitive = true` (e.g. `examples/data-sources/organization_member[s]/data_source.tf`). The native tests run those example modules via `terraform test`, and the live-API jobs (`testacc.yml`: `push` to `main` + `workflow_dispatch`, never `pull_request`) run under the public repo, so a non-sensitive output would print real addresses in world-readable Actions logs. Marking the output sensitive renders `(sensitive value)` instead. An output can always upgrade a non-sensitive source attribute to sensitive, so no schema change is needed.

### Agent built-in tool configs are an SDK union

Since SDK v1.66.0 `agent_toolset.configs` maps to `BetaManagedAgentsAgentToolConfigParamsUnion` — one struct per built-in tool, so the Terraform `name` attribute selects a union branch instead of filling an enum field. Two places must stay in sync when the API gains a built-in tool: the `stringvalidator.OneOf(...)` list on the `name` attribute and the `switch` in `buildAgentToolConfigParams` (`internal/services/agents/agent_resource.go`). The helper returns an error diagnostic on an unknown name rather than silently sending an empty config, so a missed branch surfaces as a plan-time error — cover it in `agent_resource_internal_test.go`. Permission policies are still just the two `alwaysAllowPolicyParam` / `alwaysAskPolicyParam` shapes, wrapped in each tool's own `PermissionPolicyUnion`.

Response-side types stay flat (`...AgentToolConfigUnion`), so state mapping needs no per-tool switch.

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
