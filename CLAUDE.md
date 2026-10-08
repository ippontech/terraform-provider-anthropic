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
  wifprobetest/    — shared harness behind the TestAccWIFStalenessProbe tests (federation, serviceaccounts): PreCheck gate, Write/Read polling, LogTable, NewClient
  oauthtest/       — shared unit-test helper: oauthtest.NewClient(t, srv) builds a *providerdata.OAuthClient pointed at an httptest server; oauthtest.NewSDKClient(t, srv) the bare *anthropic.Client it wraps; both take extra option.RequestOption (e.g. option.WithMaxRetries(0) for request-counting tests)
  schematest/      — shared unit-test helper: schematest.ResourceObjectType / NullValues / NullState derive a null tfsdk.State from any resource.Resource, for ImportState tests
  acctest/         — shared acceptance test helpers (ProtoV6ProviderFactories, PreCheck)
  errors/          — nil-client guards for Configure methods (import alias: providerrors)
  providerdata/    — ProviderData struct passed to every resource/data source Configure call
  tfvalue/         — StringOrNull / TimeOrNull: map the SDK zero values ("" and the zero time) of absent API fields to null; use these in every state mapping instead of a per-package helper
  retry/           — multipart upload with 5xx retry (import alias: provretry)
  provider/        — AnthropicProvider implementation only (provider.go)
  services/
    agents/        — anthropic_agent resource + agent/agents data sources
    apikeys/       — anthropic_api_key resource (import/update/delete only; no create) + api_key/api_keys data sources
    deployments/   — deployment_runs data source (Beta managed-agents API; `client.Beta.DeploymentRuns`, `GET /v1/deployment_runs`); the anthropic_deployment resource is #140
    environments/  — anthropic_environment resource + environment/environments data sources
    memorystores/  — anthropic_memory_store + anthropic_memory resources + memory_store/memory_stores/memory/memories data sources (Beta managed-agents API; `client.Beta.MemoryStores`/`client.Beta.MemoryStores.Memories`; workspace-scoped, standard API key)
    messages/      — anthropic_message resource + count_tokens data source
    models/        — model/models data sources
    organizations/ — organization data source (anthropic_organization; admin API GET /v1/organizations/me, no input) + organization_member/organization_members data sources (admin API GET /v1/organizations/users[/{id}]; members are Users)
    skills/        — skill/skill_version resources + skill/skills/skill_version/skill_versions data sources
    userprofiles/  — anthropic_user_profile resource + user_profile/user_profiles data sources (Beta; `client.Beta.UserProfiles`; beta header user-profiles-2026-08-18 sent by the SDK; standard API key; no delete endpoint; endpoint returned 404 for the test org on 2026-09-30, so tests are httptest + plan/mock only)
    workspaces/    — anthropic_workspace + anthropic_workspace_member resources + workspace/workspaces/workspace_member/workspace_members data sources (shared test helpers in workspacetest.go)
    vaults/        — anthropic_vault + anthropic_vault_credential resources (Beta managed-agents API; vault_credential uses write-only secret attributes)
    serviceaccounts/ — anthropic_service_account + anthropic_service_account_workspace resources (Beta managed-agents API; WIF non-human identity + its explicit workspace memberships, org:admin OAuth bearer only)
```

### Implemented resources and data sources

**Resources:**
- `anthropic_message` (`internal/services/messages/message_resource.go`) — calls the Messages API; write-only, immutable (no read/update/delete)
- `anthropic_agent` (`internal/services/agents/agent_resource.go`) — manages Managed Agents (create/read/update/delete); `model_effort` (Optional+Computed, API fills a per-model default) and `model_inference_geo` (Optional, free-form) sit next to `model_speed`: the API replaces `model` as a whole on update (omitting `inference_geo` clears it; omitting `effort` keeps the stored value only if `id` is unchanged, else resolves the new model's default), and the provider follows that: `buildModelConfigParams` always sends the full object, sends `effort` only when the plan knows it, and omits a null `model_inference_geo` (which clears the pin); `modelEffortFollowsModel` (plan modifier, replaces `UseStateForUnknown`) reuses the state effort only while `model` is unchanged and leaves it unknown otherwise, so changing `model` without `model_effort` resolves the new model's default; `multiagent` (coordinator roster, `agent_multiagent.go`) is an Optional single-nested attribute whose `agents` entries are `agent`/`self`/`advisor`. The API resolves `self` into an `agent` entry carrying the coordinator's own ID and echoes `advisor` last, so `mapMultiagentToState` correlates the API roster with the planned/prior roster **by key, never by index** (agent by `id`, advisor by type, self last as the owner-ID entry no explicit `agent` entry claimed), keeps config order and `self` as configured, and appends unmatched API entries as drift; with no prior roster (import) it maps verbatim, so `self` reads back as `agent` with the owner's ID. Only `version` is Computed (never the list or object). Clearing the block sends `multiagent: null` through `SetExtraFields` (only when state had one); the data sources map verbatim
- `anthropic_environment` (`internal/services/environments/environment_resource.go`) — manages environments; supports `archive_on_destroy` (archives instead of deleting on destroy when true)
- `anthropic_skill` (`internal/services/skills/skill_resource.go`) — manages skills
- `anthropic_skill_version` (`internal/services/skills/skill_version_resource.go`) — manages skill versions
- `anthropic_workspace` (`internal/services/workspaces/workspace_resource.go`) — manages workspaces (admin API)
- `anthropic_workspace_member` (`internal/services/workspaces/workspace_member_resource.go`) — assigns a user to a workspace with a given role (admin API); composite ID `<workspace_id>:<user_id>`; `workspace_billing` role rejected at plan time
- `anthropic_api_key` (`internal/services/apikeys/api_key_resource.go`) — import-only resource; manages lifecycle of existing API keys (rename, deactivate) via Admin API; Create always errors with a message to use `terraform import`; Delete sets `status: inactive`
- `anthropic_invite` (`internal/services/organizations/invite_resource.go`) — manages an organization invite (Admin API, raw `admin.Client`); immutable — Create/Read/Delete only, `email`/`role` are `RequiresReplace`, no Update endpoint exists; the invite endpoints (`POST`/`GET`/`DELETE /v1/organizations/invites[/{id}]`) require a literal `?beta=true` query parameter not mentioned in the original issue spec; Read treats any non-`pending` `status` (`accepted`/`expired`/`deleted`) as gone and calls `RemoveResource` so Terraform re-creates the invite on next apply; Delete tolerates a 404 as already-gone; no live acceptance test (inviting sends a real email, no dedicated test org per #58) — `command = plan` native test only
- `anthropic_vault` (`internal/services/vaults/vault_resource.go`) — manages vaults (Beta managed-agents API; `client.Beta.Vaults`); named container for MCP credentials; supports `archive_on_destroy`
- `anthropic_vault_credential` (`internal/services/vaults/vault_credential_resource.go`) — manages a credential inside a vault (`client.Beta.Vaults.Credentials`); three auth `type`s (`static_bearer`, `mcp_oauth` with nested `refresh`/`token_endpoint_auth`, `environment_variable` with `networking`); secret material is **write-only** (see write-only convention below); structural/immutable fields are RequiresReplace (`vault_id`, `type`, `mcp_server_url`, `secret_name`, and the `refresh` block's `client_id`/`token_endpoint`/`resource`/`token_endpoint_auth.type` — the update API cannot modify these, and its `token_endpoint_auth` union has no `none` variant); supports `archive_on_destroy`
- `anthropic_service_account` (`internal/services/serviceaccounts/service_account_resource.go`) — manages a WIF service account: a pure identity (`svac_...`) that federation rules target, carrying no authorization of its own (Beta managed-agents API; `client.Beta.Organization.ServiceAccounts`); requires the OAuth `org:admin` bearer client (`pd.OAuthClient`), not Admin API keys; `name` is `RequiresReplace` (no `name` field on update); `organization_role` (`developer`/`admin`, Optional+Computed) is only resent on `Update` when it actually changed from state, since resending an unchanged value is rejected by the API for a non-interactive credential; no hard-delete endpoint — destroy always archives; `Update` applies the same defensive read-after-write consistency wait as vaults (see below)
- `anthropic_service_account_workspace` (`internal/services/serviceaccounts/service_account_workspace_resource.go`) — assigns a WIF service account to a workspace with a non-billing role (`client.Beta.Organization.ServiceAccounts.Workspaces`; OAuth `org:admin` client); composite ID `<service_account_id>:<workspace_id>`; every user-set attribute is `RequiresReplace` (no update endpoint) and Delete is a hard remove; Read pages the membership list and **skips implicit entries** (every service account implicitly belongs to the org default workspace), so an explicit membership removed out-of-band surfaces as drift; ⚠️ the SDK's `Workspaces.Remove(ctx, workspaceID, params{ServiceAccountID})` takes the workspace ID positionally — the wiring is pinned by a URL-asserting unit test
- `anthropic_deployment` (`internal/services/deployments/deployment_resource.go`) — manages a scheduled deployment: binds an agent, an environment, initial events and an optional cron schedule to run Managed Agent sessions autonomously (Beta managed-agents API; `client.Beta.Deployment`); standard API key; `initial_events`, `resources`, `budget` are `jsontypes.Normalized` raw-JSON attributes (unmarshalled into the SDK's typed union params rather than modeled as native nested blocks — the union depth wasn't worth it); `agent_id` only supports the plain agent-ID string SDK variant (re-pins to latest on every send), never the `{id, version}` pinned variant; `metadata` Update goes through `buildMetadataPatch` + `SetExtraFields` like vaults; `paused` is reconciled via the separate Pause/Unpause endpoints, not the main Update body; no hard-delete endpoint — destroy always archives (same as `anthropic_service_account`), so every acceptance run leaves one archived deployment in the test workspace, but unlike that resource this one **does** run in CI (standard API key, no OAuth gate); `Update` applies the same defensive read-after-write consistency wait as vaults
- `anthropic_memory_store` (`internal/services/memorystores/memory_store_resource.go`) — manages a workspace-scoped memory store: a named container for agent memories (Beta managed-agents API; `client.Beta.MemoryStores` — note the plural SDK field name despite singular type names; beta header `agent-memory-2026-07-22`, distinct from the `managed-agents-2026-04-01` header used by vaults/service accounts/deployments); standard API key, workspace scoping is implicit (no `workspace_id` param, same as vaults); supports `archive_on_destroy` following the standard shape (`false` → real hard `Delete`, `true` → `Archive`, idempotent no-op if already archived — unlike `anthropic_deployment`/`anthropic_service_account`, this endpoint genuinely has both); `metadata` Update PATCH-with-null semantics can't be expressed through the typed `map[string]string` field, so it goes through `buildMemoryStoreMetadataPatch` + `params.SetExtraFields` like vaults' `buildMetadataPatch` — if the SDK ever retypes `Metadata` to `map[string]any`, revisit whether the escape hatch is still needed; Update on an already-archived store with changed `name`/`description`/`metadata` fails fast with a diagnostic (checked against state, not a stale in-memory value) rather than surfacing a confusing API 4xx; no read-after-write consistency wait — CRUD acceptance tests never observed staleness, but the endpoint has not been probed like vaults/WIF were, so treat this as unverified rather than confirmed-safe if flakiness ever shows up here
- `anthropic_memory` (`internal/services/memorystores/memory_resource.go`) — manages a memory: a single text document at a hierarchical path inside an `anthropic_memory_store` (Beta managed-agents API; `client.Beta.MemoryStores.Memories`; same beta header as memory stores); standard API key; `memory_store_id` is `RequiresReplace` (no cross-store move); `path` is a mutable rename (the API preserves `id` across renames) and content is mutable in place, both enforced by validators mirroring the API's own constraints (path shape, 100 kB content cap); every `Update` always sends a `content_sha256` `Precondition` built from prior state (optimistic concurrency) and only resends `content`/`path` when they actually changed from state; a 409 is only treated as an out-of-band modification when the API's own error `Type()` is `memory_precondition_failed_error`, distinguishing it from any other 409 (e.g. a rename colliding with an existing path), which falls through to a generic diagnostic; the precondition-failed case is surfaced as an error diagnostic telling the operator to `plan`/`refresh` rather than silently clobbering remote content; `Read` always requests `View: full` so `content` is populated, and falls back to the prior state's content only if the API ever returns an explicit JSON `null` (basic view) rather than overwriting it with an empty string; `Update` applies a defensive read-after-write consistency wait (`awaitMemoryUpdateVisible`, same shape as vaults' `awaitVaultUpdateVisible`: 5s/200ms consts passed as arguments, non-strict `updated_at` comparison plus a `content_sha256` equality check against the write response, terminal on 401/403/404, best-effort/never a diagnostic) — added preemptively because a stale post-apply refresh here would build the next `Update`'s precondition from stale state and surface as a spurious 409, not just a phantom diff; the endpoint itself has not been probed for staleness like vaults/WIF were, so treat this as unverified rather than confirmed-necessary; `path` is additionally validated against control/format characters (including U+2028/U+2029) and NFC normalization via `golang.org/x/text/unicode/norm`, promoted from an existing indirect dependency to direct in `go.mod`; no `archive_on_destroy` — memories only support hard delete, archiving exists at the store level; import uses a composite `<memory_store_id>:<memory_id>` ID (same shape as `anthropic_workspace_member`)
- `anthropic_file` (`internal/services/files/file_resource.go`) — uploads and manages a file (Files API, **GA** — uses `client.Files`, not `client.Beta.Files`; the pinned SDK v1.67.0 ships both, and the GA service is required to get `expires_in_seconds`/`expires_at` and GA pagination/filters, see the files-API note below); standard API key; no Update endpoint (files can't be modified or renamed after upload) — `source_path`, `source_hash`, `filename`, `mime_type`, `expires_in_seconds` are all `RequiresReplace`; file content is never stored in state, only `source_path`, a `source_hash` SHA256 recomputed on every plan via a custom plan modifier (so an out-of-band edit to the file at the same path is detected and forces replace), and API metadata; local size (500 MB) and filename-charset validation happen before the API call; `POST /v1/files` is not idempotent, so its local retry helper (`uploadFileWithRetry`, used because multipart bodies aren't SDK-retryable) follows the `internal/admin` POST convention and retries **only on 429**, never on 5xx
- `anthropic_user_profile` (`internal/services/userprofiles/user_profile_resource.go`) — manages a user profile: an identity representing an end-user or resold-to company a platform acts on behalf of (Beta; `client.Beta.UserProfiles`); standard API key; ⚠️ **not generally available** — `GET`/`POST /v1/user_profiles` returned a plain `404` for Ippon's test organization when probed live on 2026-09-30, under all three beta-header variants and with/without `?beta=true`, so there is **no live acceptance test**: `TestAccUserProfileResource_basic`/`_update` are gated behind an explicit `ANTHROPIC_USER_PROFILES_ACC=1` opt-in on top of `acctest.PreCheck`, and CRUD wiring/mapping is instead covered deterministically by `user_profile_resource_internal_test.go` against an `httptest` server built with `oauthtest.NewSDKClient` (a bare `*anthropic.Client`, not the `providerdata.OAuthClient` wrapper — this resource takes the standard `pd.Client`, `oauthtest` is reused here purely as a generic httptest-client helper); there is **no delete endpoint**, so `Delete` only emits a warning and removes the resource from state, it never calls the API; `metadata` Update uses **merge, not PATCH-with-null** semantics unlike vaults/memory stores — a key is provided to overwrite, omitted to leave unchanged, or set to an **empty string** to remove it — so `buildUserProfileMetadataUpdate` differs from `buildMemoryStoreMetadataPatch`/`buildMetadataPatch` even though the declarative config-diffing goal is the same; `trust_grants` is a read-only `map(object({status = string}))` with no Terraform-side plan modifier needed since it is fully server-derived

**Data sources:**
- `anthropic_model` (`internal/services/models/model_data_source.go`) — fetches a single model by ID
- `anthropic_models` (`internal/services/models/models_data_source.go`) — lists all available models
- `anthropic_organization` (`internal/services/organizations/organization_data_source.go`) — fetches the organization tied to the admin key (admin API `GET /v1/organizations/me`); takes no input; exposes `id`, `name`, `type`
- `anthropic_organization_member` (`internal/services/organizations/organization_member_data_source.go`) — fetches a single organization member (User) by user ID (admin API `GET /v1/organizations/users/{user_id}`); input `id`; exposes `email`, `name`, `role`, `added_at`, `type`. No managed "user" resource exists to anchor an unknown at plan time, so the example chains off `organization_members` (`members[0].id`) to resolve a real ID — a constant `id` would trigger a live 404 during the native `command = plan` test
- `anthropic_organization_members` (`internal/services/organizations/organization_members_data_source.go`) — lists all organization members (admin API `GET /v1/organizations/users`) with an optional `email` filter and transparent cursor pagination
- `anthropic_count_tokens` (`internal/services/messages/count_tokens_data_source.go`) — counts tokens for a given prompt
- `anthropic_agent` (`internal/services/agents/agent_data_source.go`) — fetches a single agent
- `anthropic_agents` (`internal/services/agents/agents_data_source.go`) — lists all agents
- `anthropic_deployment_runs` (`internal/services/deployments/deployment_runs_data_source.go`) — lists the run history of scheduled deployments (`GET /v1/deployment_runs`, **not** `/v1/deployments/{id}/runs`) with optional `deployment_id`, `has_error` and `trigger_type` filters and transparent pagination; `session_id` and `error` are mutually exclusive (one is always null). A well-formed but unknown `deployment_id` (`depl_` + 24 base62 characters, e.g. `depl_01AAAAAAAAAAAAAAAAAAAAAA`) returns 200 with an empty list, but a malformed one (`depl_01xyz` from the public docs, or all zeros) is rejected with `400 Invalid deployment ID` — verified live 2026-09-21; the example and native test rely on the former so they apply for real without any deployment in the test workspace
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
- `anthropic_memory_store` (`internal/services/memorystores/memory_store_data_source.go`) — fetches a single memory store by ID regardless of archived state; shares `mapMemoryStoreCommon` with the resource and the plural data source
- `anthropic_memory_stores` (`internal/services/memorystores/memory_stores_data_source.go`) — lists memory stores with transparent cursor pagination; optional `include_archived` filter (default `false`, matches the API's own default of excluding archived stores)
- `anthropic_user_profile` (`internal/services/userprofiles/user_profile_data_source.go`) — fetches a single user profile by ID (`client.Beta.UserProfiles`, beta header `user-profiles-2026-08-18`); this beta is not enabled for the `terraform-tests` organization (verified 2026-09-30: `GET /v1/user_profiles` returns 404 for the standard key, 401 for the admin key), so acceptance tests are gated on an explicit `ANTHROPIC_USER_PROFILES_ACC=1` opt-in on top of `acctest.PreCheck` and native tests use `mock_provider`
- `anthropic_user_profiles` (`internal/services/userprofiles/user_profiles_data_source.go`) — lists user profiles with transparent cursor pagination and an optional `order` (`asc`/`desc`) filter; same beta-not-enabled caveat as `anthropic_user_profile`
- `anthropic_memory` (`internal/services/memorystores/memory_data_source.go`) — fetches a single memory by ID from a memory store, always with `view=full` so `content` is populated
- `anthropic_memories` (`internal/services/memorystores/memories_data_source.go`) — lists memories in a memory store with transparent cursor pagination; optional `path_prefix` (segment-aligned, must start and end with `/`) and `depth` (`0` recursive default, `1` immediate children with deeper entries rolled up into `prefixes`) filters; `include_content` (default `false`) switches the request to `view=full`, which caps the page size fetched per request at 20

### Adding a resource or data source

1. Create the file under `internal/services/<service>/<name>_resource.go` (or `_data_source.go`), using `package <service>`
2. Implement the `resource.Resource` (or `datasource.DataSource`) interface
3. Register the factory function in `Resources()` (or `DataSources()`) in `internal/provider/provider.go`
4. Add an example config under `examples/resources/<name>/` (or `examples/data-sources/<name>/`): the main file (`resource.tf` / `data_source.tf`) holds only the resource/data blocks and outputs — it is embedded verbatim in the Registry docs — while the `terraform {}` block (required_version + required_providers) lives in a sibling `versions.tf`, which Terraform still loads as part of the module (it maps `anthropic` to `ippontech/anthropic` for the native tests) but the docs never show. Never put a `terraform {}` block back in the main file ([#231](https://github.com/ippontech/terraform-provider-anthropic/issues/231))
5. Add a template under `templates/resources/<name>.md.tmpl` (or `templates/data-sources/<name>.md.tmpl`) — **required** to set a non-empty `subcategory` (e.g. `"Agents"`, `"Messages"`, `"Models"`); without it `make generate` produces `subcategory: ""` and the resource appears ungrouped on the Terraform Registry
6. Add a Terraform native test under `tests/<name>.tftest.hcl`
7. Run `make generate` to regenerate docs

Guides live under `templates/guides/<name>.md.tmpl` (rendered to `docs/guides/`, subcategory required as for resources). Every `.md.tmpl` is parsed as a Go text/template, so a literal `{{ ... }}` (a GitHub Actions `${{ vars.X }}` expression in a YAML snippet, say) fails `make generate` with `function "vars" not defined`; and because tfplugindocs wipes `docs/` before rendering, that failure leaves every `docs/**/*.md` deleted in the working tree until a successful re-run restores them. Use literal placeholder values instead, or escape as `{{"{{"}}`. A guide's HCL example is not covered by the native tests, so validate it by hand: extract it to a scratch directory and run `terraform validate` against a freshly built provider ([#240](https://github.com/ippontech/terraform-provider-anthropic/issues/240), `docs/guides/workload_identity_federation.md`).

### Testing pattern

**Go acceptance tests** (`internal/services/<service>/`):
- Test files use `package <service>_test` (external test package), except tests that access unexported symbols which use `package <service>`
- **File naming:** use `<name>_test.go` (e.g. `workspace_rate_limits_data_source_test.go`). Do **not** suffix unit-test files with `_unit_test.go` — acceptance test functions are already disambiguated by the `TestAccXxx` prefix, and unit tests use `TestXxx`.
- **Important:** acceptance tests (those importing `internal/acctest`) MUST live in `package <service>_test` (external) to avoid an import cycle (`acctest` → `provider` → `<service>`). When unit tests need internal types AND a service has acceptance tests, split them into two files: one in `package <service>` (internal, unit) and one in `package <service>_test` (external, acceptance). When the split is for a **single** resource/data source (so the `<name>` prefix can't disambiguate, unlike `workspace` vs `workspaces`), give the internal-package file an `_internal_test.go` suffix and keep the plain `_test.go` for the external acceptance file — e.g. `agent_resource_internal_test.go` (internal) + `agent_resource_test.go` (acceptance), and `organization_data_source_internal_test.go` (internal) + `organization_data_source_test.go` (acceptance). The suffix names the file by what truly distinguishes it — the **internal package** (unexported access, mock `httptest` server, no live API) — not a test type: it may hold mapping, pagination, filter/query-param construction, and 404 coverage. The plain `<name>_test.go` always holds the external acceptance tests; **never** use an `_acc_test.go` suffix, since the `TestAcc` prefix already disambiguates acceptance functions. Exercise the API-response → state mapping directly by extracting a `map<Resource>ToState` helper the internal test calls.
- `internal/acctest/acctest.go` exports `ProtoV6ProviderFactories`, `PreCheck` (standard API key), `PreCheckAdmin` (admin API key), and `PreCheckOAuth` (`ANTHROPIC_AUTH_TOKEN`, `t.Skip`s when unset) used by all acceptance tests. All three call `SkipUnlessAcc` first (`t.Skip` unless `TF_ACC` is set): fixture setup that seeds objects through the SDK runs **before** `resource.Test` and its own `TF_ACC` gate, so without this a plain `make test` with live credentials in `.env` would create real objects (archive-only ones in the production org for WIF, [#296](https://github.com/ippontech/terraform-provider-anthropic/issues/296)). Call the matching `PreCheck*` at the top of any test that seeds fixtures, never a hand-written `TF_ACC` check. Live SDK clients come from `acctest.NewAPIKeyClient()` / `acctest.NewOAuthClient()` (both `WithoutEnvironmentDefaults`, so the other exported credential is not sent along), never from an inline `anthropic.NewClient`. `PreCheckOAuth`-gated tests (currently `anthropic_service_account` and `anthropic_service_account_workspace`) are **not run at all for now**: CI's `testacc` job carries the standard and admin API keys but no durable org:admin OAuth token (WIF bootstrap pending, [#137](https://github.com/ippontech/terraform-provider-anthropic/issues/137)), and the only org:admin token available locally belongs to Ippon's **production** organization, where WIF objects are archive-only, so every run leaves archived issuers, service accounts and rules behind. Do not run them (or any live WIF probe) locally, and do not have an agent do it: they wait for a dedicated test organization, exactly like the Admin API resource tests blocked by [#58](https://github.com/ippontech/terraform-provider-anthropic/issues/58).
- **Test workspace isolation.** All acceptance/native tests operate within the dedicated `terraform-tests` workspace so test resources never touch production. The standard `ANTHROPIC_API_KEY` used for tests is **scoped to `terraform-tests`**, so standard-API resources created during tests land there automatically. Admin API tests (organization-wide) target it by ID via the single shared constant `acctest.TerraformTestsWorkspaceID` — use that constant, never re-hardcode the `wrkspc_...` literal in Go tests.
- For an admin `*admin.Client` pointed at an `httptest` server in any package's unit tests, use `admintest.NewClient(t, srv)` (`internal/admintest`) — do not construct `admin.Client` inline or duplicate the helper per package
- For an OAuth `*providerdata.OAuthClient` pointed at an `httptest` server, every package must use `oauthtest.NewClient(t, srv)` (`internal/oauthtest`; `oauthtest.NewSDKClient(t, srv)` for a bare `*anthropic.Client`) — do not construct the client inline or keep a per-file copy. ImportState unit tests build their null state with `schematest` (`internal/schematest`), and acceptance tests build the live OAuth client with `acctest.NewOAuthClient()`, never inline.
- `internal/services/workspaces/workspacetest.go` defines workspaces-specific shared helpers (`workspaceFixture`, `pageData`, `fetchAllPages`, plus a thin `newTestAdminClient` that delegates to `admintest.NewClient`); reuse them in any new workspaces unit test instead of duplicating pagination loops
- **Admin API acceptance tests for read-only data sources:** target the `terraform-tests` workspace via `acctest.TerraformTestsWorkspaceID` and gate on `acctest.PreCheckAdmin`. These are **smoke** tests (the live workspace's data isn't deterministic, so assert attribute presence like `members.#`, not specific values); they **complement, not replace**, the httptest-based unit tests that deterministically cover pagination, query-param/filter construction, mapping edge cases, and 404 handling. Covered so far: `workspace`, `workspaces`, `workspace_members`, `workspace_rate_limits`, `organization`, `organization_member`, `organization_members`, `api_keys`. (`organization_member`'s smoke test chains off `organization_members` to resolve a real user ID, since no constant ID is deterministically valid.) Resource tests (create/update/delete) on the Admin API remain blocked by [#58](https://github.com/ippontech/terraform-provider-anthropic/issues/58) because we do not yet have a dedicated test organization, so do not create new resources via Admin API in tests.
- Admin API **Terraform native tests**: use `command = plan` (not the default `apply`) until a test org is available; this validates schema without making live API calls. Read-only data source native tests may run against the `terraform-tests` workspace ID above.
- OAuth-gated (`PreCheckOAuth`) resources' native tests are pinned to `command = plan` too, but for a different reason than the Admin API ones above: CI has no durable org:admin token to apply with (not the #58 test-org blocker). See `tests/service_account.tftest.hcl`.
- **Standard-API resources support full CRUD acceptance tests.** The #58 blocker is Admin-API-only. Vaults and vault credentials use the standard `ANTHROPIC_API_KEY` (gate on `acctest.PreCheck`) and are workspace-scoped to that key's workspace — there is no `workspace_id` create param, so they land wherever the key is scoped (the test key is scoped to `terraform-tests`; see the test-workspace-isolation note above). Vaults are billed only at runtime, so create/destroy is free. **Vault credentials are not validated until session runtime**, so acceptance tests create them with placeholder secrets and unreachable `mcp_server_url`s; the full CRUD lifecycle runs without ever starting a session. Their native test (`tests/vault_credential.tftest.hcl`) therefore applies for real (the default command), unlike the Admin-API native tests that are pinned to `command = plan`. `archive_on_destroy` tests for a credential must set `archive_on_destroy = true` on the parent vault too (a hard-deleted vault cascade-deletes the credential, leaving nothing to assert on).
- Unit tests: no special env vars needed
- Acceptance tests: use `resource.Test(t, resource.TestCase{...})` with `TF_ACC=1`

**Terraform native tests** (`tests/`):
- One `.tftest.hcl` file per resource/data source (e.g. `tests/message.tftest.hcl`)
- Each test references the corresponding example config as its module source (e.g. `source = "../examples/resources/message"`, relative to `tests/`) — **always the public example, never a copy**: a fixture is never what users read, and the two drift ([#213](https://github.com/ippontech/terraform-provider-anthropic/pull/213) broke `generate` exactly that way). Plan-only tests (Admin API and WIF resources, no test org / no CI OAuth token) get their credential from a `provider "anthropic" { admin_api_key = "dummy-admin-api-key" }` (or `auth_token = "dummy-auth-token"`) block **in the `.tftest.hcl` itself**: under `command = plan` a resource never calls the API, Configure only needs a non-empty string, and assertions reference the module's resources directly (`anthropic_workspace.example.name == ...`) so the example needs no extra outputs. `make terraform-test` runs `terraform -chdir=tests test`, so **`tests/` is the root module**: test-file `provider`/`mock_provider` blocks resolve against it, which is why `tests/versions.tf` maps `anthropic` to `ippontech/anthropic` (the repo root stays free of Terraform config) — do not remove it, and write module sources as `../examples/...` ([#233](https://github.com/ippontech/terraform-provider-anthropic/issues/233)). Self-contained list data-source examples (no required input, no `[0]` chaining) use `mock_provider "anthropic" {}` instead (plan + apply, zero credentials); note `override_data` cannot inject elements into a computed `list(object)` — the mocked list is always empty — so examples that index `list[0].id` keep a `tests/fixtures/<name>_plan/` with the `depends_on` seed trick until CI can read live
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

`.tflint.hcl` enables the full `terraform` ruleset (`preset = "all"`, `terraform_standard_module_structure` off); the `tflint` CI job runs `tflint --recursive` on every push, so every example `output` needs a `description` and every `versions.tf` a `required_version` (#229).

Before committing, run pre-commit hooks:
```bash
pre-commit run -a
```

## Provider coding conventions

### API key model

The provider has three optional credentials — at least one must be configured:

| Credential | Provider arg | Env var | Client field | Used by |
|---|---|---|---|---|
| Standard | `api_key` | `ANTHROPIC_API_KEY` | `pd.Client` | All standard resources and data sources |
| Admin | `admin_api_key` | `ANTHROPIC_ADMIN_API_KEY` | `pd.AdminClient` | Organization endpoints (`/v1/organizations/*`, e.g. workspaces) |
| OAuth bearer (`org:admin`) | `auth_token` | `ANTHROPIC_AUTH_TOKEN` | `pd.OAuthClient` | Endpoints that reject API keys and require `Authorization: Bearer` (Workload Identity Federation, [#137](https://github.com/ippontech/terraform-provider-anthropic/issues/137)) |

Each is resolved by `resolveCredential` (`internal/provider/provider.go`): the provider argument wins when set, the env var otherwise, and an Unknown value (an unresolved reference at plan time) counts as unset.

Two details are load-bearing:

- **`pd.OAuthClient` is a `*providerdata.OAuthClient` wrapper, not a bare `*anthropic.Client`.** The SDK carries no notion of which credential a client holds, so two bare clients are mutually assignable and mixing them up compiles silently, surfacing only as a 401 at apply time. The wrapper keeps the compiler in the loop; [#187](https://github.com/ippontech/terraform-provider-anthropic/issues/187) extends the same treatment to the other clients when `internal/admin` is retired. Because the wrapper adds a level of indirection the other guards do not have, `requireOAuthClient` checks `client.Client != nil` as well as `client != nil` — a non-nil wrapper around a nil SDK client would otherwise pass the guard and nil-deref on the first API call.
- **Every SDK client is built through `newSDKClient`, which passes `option.WithoutEnvironmentDefaults()`.** `anthropic.NewClient` otherwise prepends `DefaultClientOptions()`, whose chain has five sources: `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, the profile named by `ANTHROPIC_PROFILE`, env-var federation, and the fallback profile under the SDK config dir. The first two set a header *before* the explicit option is applied, so with both variables exported — the normal case — a client would present both credentials and the endpoints behind each reject the other. The last three are worse than a stray header: `option.WithConfig` applies a profile's non-credential settings **unconditionally**, so a profile left active by `ant auth login` (the command the provider docs tell operators to run) would override the base URL and stamp its `workspace_id` as an `anthropic-workspace-id` header on every request, none of it visible in the Terraform config. The marker option closes all five. Three tests cover it — `TestConfigureClientsCarryExactlyOneCredential`, `TestConfigureStandardClientDropsInheritedBearer`, `TestConfigureIgnoresTheAmbientProfile` (which plants a profile via `ANTHROPIC_CONFIG_DIR`) — and all fail if it is dropped.
- **`ANTHROPIC_BASE_URL` is re-applied by hand**, because the marker option skips it too. It is read with an explicit `!= ""` check: an exported-but-empty value must not replace the SDK's production default with `""`. The same trap applies in tests, so `clearCredentialEnv` genuinely `os.Unsetenv`s each variable (after a `t.Setenv` whose only purpose is the restore-on-cleanup it registers) rather than setting it to `""`.
- **The provider does not do the WIF token exchange**, so the SDK's federation variables alone cannot configure it — `Configure` fails with `Missing Credentials`. Documented as a caveat in `templates/index.md.tmpl`; revisit under [#137](https://github.com/ippontech/terraform-provider-anthropic/issues/137) if native federation is wanted.

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
// OAuth resource        →  providerrors.RequireOAuthResourceClient(pd.OAuthClient, ...)
// OAuth data source     →  providerrors.RequireOAuthDataSourceClient(pd.OAuthClient, ...)
```

### Shared helpers

- `internal/admin/` — HTTP client for Admin API; import as `"github.com/ippontech/terraform-provider-anthropic/internal/admin"`. `DoRequest` retries with exponential backoff plus jitter, honouring the `x-should-retry` override and the `retry-after-ms` / `retry-after` headers. **What gets retried depends on the method.** Idempotent requests (`GET`, `DELETE`, and the rest of the RFC 9110 set) replay on connection errors, on a response body that stops arriving mid-read, and on the transient statuses the Anthropic SDK retries (408, 409, 429, 5xx). A `POST` replays **only on 429** — the one answer that states the call was not processed; on a 5xx, a 409 or a dropped connection the write may already have landed, and a second `POST /v1/organizations/workspaces` would create a duplicate workspace (names are not unique) while a second member-add would burn the budget on a 409 and leave the membership untracked. An explicit `x-should-retry` header from the server still wins in both directions. Retries are **opt-in**: `admin.NewClient` sets `MaxRetries` to `DefaultMaxRetries` (2), while the zero-value `Client` built by `admintest.NewClient` performs a single attempt, so unit tests stay fast and deterministic. Tests that do exercise retrying should set `MaxRetries` along with `BaseRetryDelay`/`MaxRetryDelay` (both default when zero) to keep delays in the millisecond range, and their stub must **not** send `retry-after` / `retry-after-ms`: a server-supplied delay deliberately bypasses both knobs (shortening it would only earn another 429) and is bounded only by the 60s `maxRetryAfter` ceiling
- `internal/errors/` (import alias `providerrors`) — nil-client guards for `Configure` methods; `api_key.go` holds the standard and admin guards, `auth_token.go` the OAuth ones
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

`POST /v1/vaults/{id}` returns the updated object, but a `GET` issued immediately after can still return the **pre-update** object. Measured against the live API on 2026-09-01: stale at t+493ms, converged between 520ms and 1.04s across three trials, with every field back at its prior value (`display_name` included, so it is a stale read rather than a `buildMetadataPatch` artefact). A `GET` straight after a **create** is consistent, so only writes to an existing object are affected.

The window covers **delete** too: on 2026-09-02 `TestAccVaultResource_withMetadata` failed on `main` because `CheckDestroy`'s immediate `Get` still saw the destroyed vault (probed afterwards: authenticated 404, so the delete had landed). Fixed in [#199](https://github.com/ippontech/terraform-provider-anthropic/pull/199) at the **test** layer only — the resource's `Delete` needs no wait since Terraform does not read after a destroy. All four destroy/archive check functions in the vaults acceptance tests poll via the shared helpers in `internal/services/vaults/helpers_test.go`: `awaitGone` (until an API 404 — any other error is surfaced rather than counted as "destroyed") and `awaitArchived` (until non-zero `archived_at`), reusing the 5s/200ms parameters below. Reuse the typed wrappers there (`awaitVaultGone`, `awaitVaultArchived`, `awaitCredentialGone`, `awaitCredentialArchived`, `newAccTestClient`, `hardDeleteVault`) in any new vaults acceptance test instead of a bare post-destroy `Get`.

Left alone, Terraform's post-apply refresh lands inside that window and the next plan shows a phantom in-place update — this is what made `TestAccVaultResource_update` flaky. `vault_resource.go` therefore calls `awaitVaultUpdateVisible` after a successful update: it polls `Get` until `updated_at` is no older than the timestamp the write returned. The wait is **best-effort** — a read error, a timeout or a cancelled context returns without a diagnostic, because the write already succeeded and failing the apply would turn a cosmetic staleness window into a hard error.

Two details of that loop are load-bearing. The ceiling (5s) and interval (200ms) are **consts passed as arguments**, not package-level `var`s: the unit tests need to shrink them to milliseconds, and the vaults acceptance tests exercise the real `Update` in the same test binary, so a mutable global would race the moment any vault test opted into `t.Parallel()` (`make test` runs with `-parallel=10`). And a `401`, `403` or `404` from the poll (`isTerminalVaultReadError`) bails out immediately instead of retrying to the deadline — a vault deleted out-of-band, or a key that lost access to it, will never converge, and retrying would stall the apply for seconds. Every other failure keeps polling, since it says nothing about visibility.

Compare the two timestamps with a **non-strict** comparison — `!vault.UpdatedAt.Before(writtenAt)`, not `vault.UpdatedAt.After(writtenAt)`: an `updated_at` **equal** to the write timestamp must count as visible, or the loop always times out. `anthropic_vault_credential` has the same structural exposure but no observed flakiness, and its endpoint was not probed — check before assuming it needs the same wait.

#### WIF endpoints (probed 2026-09-08)

The WIF endpoints (`/v1/organizations/{service_accounts,federation_issuers,federation_rules}`) share the managed-agents backend family and were probed the same way on 2026-09-08 ([#238](https://github.com/ippontech/terraform-provider-anthropic/issues/238)). The probe is checked in as `TestAccWIFStalenessProbe` — `internal/services/federation/wif_staleness_probe_test.go` (issuer, rule, rule-workspaces) and `internal/services/serviceaccounts/wif_staleness_probe_test.go` (service account), both built on the shared harness in `internal/wifprobetest` (like `admintest`, so a fix to the polling or the 401 bail-out lands once) — and is doubly gated (`wifprobetest.PreCheck`): `acctest.PreCheckOAuth` (needs an org:admin bearer) plus an explicit `ANTHROPIC_WIF_STALENESS_PROBE=1` opt-in, so `make testacc` never runs it. It is an instrument, not a regression test: it never fails on a stale read, it prints a table. **Do not run it against Ippon's production organization again**: each trial creates issuers, service accounts and rules that can only be archived, never deleted, and the 2026-09-08 run already left its archived fixtures there. Re-run it only against a dedicated test organization ([#58](https://github.com/ippontech/terraform-provider-anthropic/issues/58)), and only before changing any of the waits below:

```bash
TF_ACC=1 ANTHROPIC_AUTH_TOKEN=... ANTHROPIC_WIF_STALENESS_PROBE=1 \
  go test -run TestAccWIFStalenessProbe -v ./internal/services/...
```

Method per trial: one write (`POST …/{id}` update, or rule-workspace `Add`/`Remove`), then a read every 50ms up to 5s until it reflects the write — non-strict `updated_at` comparison against the write response's `updated_at` plus a check on the mutated field (presence/absence for the list endpoint, which carries no timestamp). The first read landed 220–340ms after the write (request latency), so "stale" below means the object was still stale at ≥220ms. Nine trials per endpoint over three runs (issuer: twelve, nine before any rule referenced it and three with a live rule):

| Endpoint | Stale trials | Convergence when stale | Action |
|---|---|---|---|
| `GET /service_accounts/{id}` after `POST …/{id}` | 1 / 9 | 494ms | existing `awaitServiceAccountUpdateVisible` wait kept, now backed by measurement |
| `GET /federation_issuers/{id}` after `POST …/{id}` | 0 / 12 | — | **no wait** (see caveat) |
| `GET /federation_rules/{id}` after `POST …/{id}` | 1 / 9 | 556ms | `awaitFederationRuleUpdateVisible` added to `Update` |
| `GET /federation_rules/{id}/workspaces` after `POST` Add | 1 / 9 | 1.07s (two stale reads) | `awaitFederationRuleWorkspaceListed` added to `Read` |
| `GET /federation_rules/{id}/workspaces` after `DELETE` Remove | 0 / 9 | — | none (only the tests' `CheckDestroy` reads after a destroy) |

Two details of the outcome are load-bearing:

- **The rule-workspace hazard is state loss, not a phantom diff.** `anthropic_federation_rule_workspace`'s `Read` is find-in-list and treats an absent entry as an authoritative out-of-band removal (`RemoveResource`), and Terraform runs that `Read` right after `Create`. A stale, still-empty list there would drop a freshly created resource from state — hence the wait sits in `Read` (bounded by `federationRuleWorkspaceConsistencyTimeout`/`Interval`, 5s/200ms, passed as arguments; it shares `isTerminalFederationRuleReadError` with the rule's wait, so a 401/403/404 — the last meaning the rule itself is gone — returns on the first occurrence, while a transient failure such as a 5xx keeps polling and is only surfaced, as the last error seen, once the deadline passes). The cost is symmetric: a `Read` of an entry that really is gone now waits the full 5s before reporting it, which only happens on genuine drift. `Create`'s own lookup (it only enriches the Computed `workspace_name`, which the post-apply refresh fills anyway) deliberately stays a single call, so an empty first list there is harmless and `TestFederationRuleWorkspaceCreate_AddWiring` does not spend 5s on its mocked empty list.
- **The issuer's negative result is thin, not conclusive.** At the ~1-in-9 rate the sibling endpoints showed, twelve clean trials have roughly a one-in-four chance of simply missing the window. It gets no wait today because the coordinating rule for this section is measurement first, but if `TestAccFederationIssuerResource_*` ever shows a phantom diff, re-run the probe and add the same wait shape rather than debating it. One further oddity was seen once and never reproduced: on the very first probe run, the issuer `POST …/{id}` update — issued ~1–2s after the issuer's own create, and after a rule referencing it had been created successfully — answered `404 Federation issuer not found`; the 16 update writes of the following runs all returned 200 on the first attempt (the probe now retries a 404 on the write and would report it in the `write 404s` column). Treat a create-then-immediately-update 404 on issuers as a possible read-after-write effect on the write path, not as a client bug.

`anthropic_service_account`'s `Update` (`internal/services/serviceaccounts/service_account_resource.go`) had applied `awaitServiceAccountUpdateVisible` defensively since before the probe existed; the 1-in-9 stale trial above is what justifies it. `anthropic_federation_rule`'s wait (`federation_rule_resource.go`) has the same shape and the same load-bearing details as the vaults one: 5s/200ms consts passed as arguments, terminal on 401/403/404, non-strict `!rule.UpdatedAt.Before(writtenAt)`, best-effort (never a diagnostic). Both are unit-tested against `httptest` servers in `*_await_internal_test.go` files.

A related create-path quirk, distinct from the staleness above: `POST /v1/organizations/federation_rules` returns the new rule with an **empty `issuer_name`**, while `GET …/federation_rules` and `GET …/{id}` return it (checked read-only against the live API on 2026-09-17, [#243](https://github.com/ippontech/terraform-provider-anthropic/issues/243)). `issuer_name` is Computed and mapped from the SDK zero value, so without a fix the state carried `""` until the next refresh and `TestAccFederationRuleResource_basic` failed on `TestCheckResourceAttrSet`. `Create` therefore calls `completeFederationRuleIssuerName` (`federation_rule_resource.go`): a **single** best-effort `GET` when the create response's `issuer_name` is empty (a read straight after a create is consistent, so no bounded wait), skipped when the name is present, and a failing `GET` keeps the create response rather than failing the apply. The data sources are unaffected since they `GET`. Before deprecating `issuer_name` anywhere, re-check the list endpoint the same way: the field still exists, it is only the create payload that omits it.

### PII attributes and example outputs

Do **not** mark user PII attributes (`email`, `name`, etc.) as `Sensitive: true` in a data source/resource schema. They are the legitimate product of a lookup, not credentials, and schema-level sensitivity forces every consumer into `sensitive = true` outputs or `nonsensitive()` wrappers. This matches the GitLab provider, whose `gitlab_user` resource and `gitlab_user`/`gitlab_users` data sources mark only `password` sensitive, never `email`/`name`. Reserve `Sensitive: true` for true secrets (see write-only attributes above).

Instead, prevent PII from leaking into CI logs at the **example-output** layer: any example `output` that surfaces an email/name must set `sensitive = true` (e.g. `examples/data-sources/organization_member[s]/data_source.tf`). The native tests run those example modules via `terraform test`, and the live-API jobs (`testacc.yml`: `push` to `main` + `workflow_dispatch`, never `pull_request`) run under the public repo, so a non-sensitive output would print real addresses in world-readable Actions logs. Marking the output sensitive renders `(sensitive value)` instead. An output can always upgrade a non-sensitive source attribute to sensitive, so no schema change is needed.

### Agent built-in tool configs are an SDK union

Since SDK v1.66.0 `agent_toolset.configs` maps to `BetaManagedAgentsAgentToolConfigParamsUnion` — one struct per built-in tool, so the Terraform `name` attribute selects a union branch instead of filling an enum field. Two places must stay in sync when the API gains a built-in tool: the `stringvalidator.OneOf(...)` list on the `name` attribute and the `switch` in `buildAgentToolConfigParams` (`internal/services/agents/agent_resource.go`). The helper returns an error diagnostic on an unknown name rather than silently sending an empty config. Note where each net catches: the validator rejects a name outside the `OneOf` list **at plan time**, but a name that passes the validator and has no `switch` branch is only reached from `Create` and `Update`, so it fails **mid-apply** — which is why the branch coverage in `agent_resource_internal_test.go` matters. Permission policies are still just the two `alwaysAllowPolicyParam` / `alwaysAskPolicyParam` shapes, wrapped in each tool's own `PermissionPolicyUnion`.

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

Breaking changes (`feat!:`, `BREAKING CHANGE:` footer) never target `main` directly: they go to the long-lived staging branch for the next major (`major/2.0.0` for the first one) and reach `main` in a single merge commit at release time. `.releaserc` pins `branches` to `["main"]` so no staging branch name can become a semantic-release release branch. Full procedure in `RELEASE.md`, section "Major versions".

### Go unit tests

After any bug fix, refactoring, or new helper added under `internal/`, write or update Go unit tests in the same package before considering the task done. Unit tests live next to the code they test (e.g. `internal/retry/multipart_test.go` for `internal/retry/multipart.go`). Run `make test` to verify they pass.
