# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Architecture

This is a Terraform provider built with [HashiCorp Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework) v1.19.0.

The Anthropic Go SDK is pinned to **v1.67.0** and must not be bumped past it in a plain `chore(deps)` change. v1.68.0 renames skill fields (`display_title` → `display_name`, `latest_version` → `latest_version_id`) and drops `directory` and `version` from the skill-version response — but **the deployed API still sends the v1.67.0 names**. Verified against the live API on 2026-09-01 under `anthropic-beta: skills-2025-10-02` (the only skills beta constant, unchanged in v1.68.0): `GET /v1/skills` returns `display_title` and `latest_version`, and `GET /v1/skills/{id}/versions` returns `directory` and `version`. v1.67.0's struct tags match that payload field for field.

So v1.68.0 is **ahead of the API, not behind it**: bumping now would silently deserialise `""` into `display_name` and `latest_version_id` (the API sends the old keys, which v1.68.0 no longer maps) and would drop two attributes the API still returns. Going past v1.67.0 needs the API rename to ship first, then a documented migration. `renovate.json` enforces this with an `allowedVersions: "<1.68.0"` rule on the SDK, plus two rules for OSV vulnerability-fix PRs, whose own `allowedVersions` replaces the cap: one disables its `minor`/`major` updates while it is below v1.68.0, the other its `major` updates always. So no bump PR can be opened, security PRs included (patch releases within `1.67.x` still flow) — the note in `.github/renovate.md` explains why guidance alone was not enough. Re-run the check above before revisiting, and remove the cap only as part of [#192](https://github.com/ippontech/terraform-provider-anthropic/issues/192).

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
    files/         — anthropic_file resource + file/files data sources (Files API, GA, `client.Files`; standard API key)
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

Code lives in `internal/services/<service>/`, one directory per Anthropic service. Each directory has its own `CLAUDE.md` with the load-bearing details of its resources and data sources (API quirks, live probes, test gating). Claude Code loads it when a file in that directory is read or edited; read it before changing that service.

- `agents/`: `anthropic_agent` resource, `agent` / `agents` data sources. Standard key.
- `apikeys/`: `anthropic_api_key` resource (import-only), `api_key` / `api_keys` data sources. Admin key.
- `deployments/`: `anthropic_deployment` resource, `deployment_runs` data source. Standard key.
- `environments/`: `anthropic_environment` resource, `environment` / `environments` data sources. Standard key.
- `federation/`: `anthropic_federation_issuer`, `_rule`, `_rule_workspace` resources and their data sources; WIF staleness probe results. OAuth bearer.
- `files/`: `anthropic_file` resource, `file` / `files` data sources (Files API GA). Standard key.
- `memorystores/`: `anthropic_memory_store`, `anthropic_memory` resources, `memory_store(s)`, `memory`, `memories` data sources. Standard key.
- `messages/`: `anthropic_message` resource, `count_tokens` data source. Standard key.
- `models/`: `model` / `models` data sources. Standard key.
- `organizations/`: `anthropic_invite`, `anthropic_organization_member` resources, `organization`, `organization_member(s)`, `invite(s)` data sources. Admin key.
- `serviceaccounts/`: `anthropic_service_account`, `anthropic_service_account_workspace` resources and their data sources. OAuth bearer.
- `skills/`: `anthropic_skill`, `anthropic_skill_version` resources, `skill(s)`, `skill_version(s)` data sources. Standard key.
- `userprofiles/`: `anthropic_user_profile` resource, `user_profile(s)` data sources (beta not enabled for the test org). Standard key.
- `vaults/`: `anthropic_vault`, `anthropic_vault_credential` resources; vaults read-after-write measurements. Standard key.
- `workspaces/`: `anthropic_workspace`, `anthropic_workspace_member` resources, `workspace(s)`, `workspace_member(s)`, `workspace_rate_limits` data sources. Admin key.

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
- `internal/acctest/acctest.go` exports `ProtoV6ProviderFactories`, `PreCheck` (standard API key), `PreCheckAdmin` (admin API key), `PreCheckOAuth` (`ANTHROPIC_AUTH_TOKEN`, `t.Skip`s when unset) and `PreCheckMultiWorkspace` (`ANTHROPIC_MULTI_WORKSPACE_API_KEY`, `t.Skip`s when unset) used by all acceptance tests. All four call `SkipUnlessAcc` first (`t.Skip` unless `TF_ACC` is set): fixture setup that seeds objects through the SDK runs **before** `resource.Test` and its own `TF_ACC` gate, so without this a plain `make test` with live credentials in `.env` would create real objects (archive-only ones in the production org for WIF, [#296](https://github.com/ippontech/terraform-provider-anthropic/issues/296)). Call the matching `PreCheck*` at the top of any test that seeds fixtures, never a hand-written `TF_ACC` check. Live SDK clients come from `acctest.NewAPIKeyClient()` / `acctest.NewOAuthClient()` (both `WithoutEnvironmentDefaults`, so the other exported credential is not sent along), never from an inline `anthropic.NewClient`. `PreCheckOAuth`-gated tests (currently `anthropic_service_account` and `anthropic_service_account_workspace`) are **not run at all for now**: CI's `testacc` job carries the standard and admin API keys but no durable org:admin OAuth token (WIF bootstrap pending, [#137](https://github.com/ippontech/terraform-provider-anthropic/issues/137)), and the only org:admin token available locally belongs to Ippon's **production** organization, where WIF objects are archive-only, so every run leaves archived issuers, service accounts and rules behind. Do not run them (or any live WIF probe) locally, and do not have an agent do it: they wait for a dedicated test organization, exactly like the Admin API resource tests blocked by [#58](https://github.com/ippontech/terraform-provider-anthropic/issues/58).
- **Test workspace isolation.** All acceptance/native tests operate within the dedicated `terraform-tests` workspace so test resources never touch production. The standard `ANTHROPIC_API_KEY` used for tests is **scoped to `terraform-tests`**, so standard-API resources created during tests land there automatically. Admin API tests (organization-wide) target it by ID via the single shared constant `acctest.TerraformTestsWorkspaceID` — use that constant, never re-hardcode the `wrkspc_...` literal in Go tests. A second workspace, `terraform-tests-2` (`acctest.TerraformTests2WorkspaceID`), exists only for the `workspace_id` provider-argument tests (`vault_resource_workspace_test.go`): they run with `ANTHROPIC_MULTI_WORKSPACE_API_KEY`, a service account key member of both test workspaces and scoped to neither, gated on `acctest.PreCheckMultiWorkspace` (`t.Skip`s when unset, the key is a CI secret) and read the API out of band through `acctest.NewMultiWorkspaceClient(workspaceID)`. Never use that key or that workspace for anything else: every other test relies on `ANTHROPIC_API_KEY` being single-workspace so its objects land in `terraform-tests` without a header.
- For an admin `*admin.Client` pointed at an `httptest` server in any package's unit tests, use `admintest.NewClient(t, srv)` (`internal/admintest`) — do not construct `admin.Client` inline or duplicate the helper per package
- For an OAuth `*providerdata.OAuthClient` pointed at an `httptest` server, every package must use `oauthtest.NewClient(t, srv)` (`internal/oauthtest`; `oauthtest.NewSDKClient(t, srv)` for a bare `*anthropic.Client`) — do not construct the client inline or keep a per-file copy. ImportState unit tests build their null state with `schematest` (`internal/schematest`), and acceptance tests build the live OAuth client with `acctest.NewOAuthClient()`, never inline.
- **Admin API acceptance tests for read-only data sources:** target the `terraform-tests` workspace via `acctest.TerraformTestsWorkspaceID` and gate on `acctest.PreCheckAdmin`. These are **smoke** tests (the live workspace's data isn't deterministic, so assert attribute presence like `members.#`, not specific values); they **complement, not replace**, the httptest-based unit tests that deterministically cover pagination, query-param/filter construction, mapping edge cases, and 404 handling. Covered so far: `workspace`, `workspaces`, `workspace_members`, `workspace_rate_limits`, `organization`, `organization_member`, `organization_members`, `api_keys`. (`organization_member`'s smoke test chains off `organization_members` to resolve a real user ID, since no constant ID is deterministically valid.) Resource tests (create/update/delete) on the Admin API remain blocked by [#58](https://github.com/ippontech/terraform-provider-anthropic/issues/58) because we do not yet have a dedicated test organization, so do not create new resources via Admin API in tests.
- Admin API **Terraform native tests**: use `command = plan` (not the default `apply`) until a test org is available; this validates schema without making live API calls. Read-only data source native tests may run against the `terraform-tests` workspace ID above.
- OAuth-gated (`PreCheckOAuth`) resources' native tests are pinned to `command = plan` too, but for a different reason than the Admin API ones above: CI has no durable org:admin token to apply with (not the #58 test-org blocker). See `tests/service_account.tftest.hcl`.
- Unit tests: no special env vars needed
- Acceptance tests: use `resource.Test(t, resource.TestCase{...})` with `TF_ACC=1`

**Terraform native tests** (`tests/`):
- One `.tftest.hcl` file per resource/data source (e.g. `tests/message.tftest.hcl`)
- Each test references the corresponding example config as its module source (e.g. `source = "../examples/resources/message"`, relative to `tests/`) — **always the public example, never a copy**: a fixture is never what users read, and the two drift ([#213](https://github.com/ippontech/terraform-provider-anthropic/pull/213) broke `generate` exactly that way). Plan-only tests (Admin API and WIF resources, no test org / no CI OAuth token) get their credential from a `provider "anthropic" { admin_api_key = "dummy-admin-api-key" }` (or `auth_token = "dummy-auth-token"`) block **in the `.tftest.hcl` itself**: under `command = plan` a resource never calls the API, Configure only needs a non-empty string, and assertions reference the module's resources directly (`anthropic_workspace.example.name == ...`) so the example needs no extra outputs. `make terraform-test` runs `terraform -chdir=tests test`, so **`tests/` is the root module**: test-file `provider`/`mock_provider` blocks resolve against it, which is why `tests/versions.tf` maps `anthropic` to `ippontech/anthropic` (the repo root stays free of Terraform config) — do not remove it, and write module sources as `../examples/...` ([#233](https://github.com/ippontech/terraform-provider-anthropic/issues/233)). Self-contained list data-source examples (no required input, no `[0]` chaining) use `mock_provider "anthropic" {}` instead (plan + apply, zero credentials); note `override_data` cannot inject elements into a computed `list(object)` — the mocked list is always empty — so examples that index `list[0].id` keep a `tests/fixtures/<name>_plan/` with the `depends_on` seed trick until CI can read live
- Tests use `assert` blocks to verify computed attribute values
- The top-level `test { parallel = true }` block opts every `run` block in the file into parallel execution; do not repeat `parallel = true` inside individual `run` blocks (it's redundant)
- Run with `make terraform-test` (builds and installs the provider first via `.dev.tfrc`)

## Environment

A `.env` file at the project root (gitignored) sets machine-specific variables and local credentials. `mise.toml` declares it under `[env] _.file = ".env"`, so mise exports its variables automatically on entering the directory, in the Claude Code shell included (the mise hook is active there). Never `source .env` by hand, and never read or print the file: its contents are secrets. A missing `.env` (CI, a fresh worktree) is tolerated silently by mise. Claude Code's global settings also carry a `Read` deny rule on `.env` files, so the IDE never shares a selection made in one.

After upgrading Go via mise, run `go clean -cache` before `make` to clear stale build artifacts. Without this, golangci-lint's typecheck step fails with a "version does not match go tool version" error because cached objects carry the old Go version tag.

`go.mod` carries two Go versions. `toolchain go1.26.10` is the Go CI tests with and releases are built with: `actions/setup-go` reads it before the `go` line, Renovate proposes its updates by default and its OSV check matches it against the standard-library advisories. `go 1.26.2` is the language version (minimum Go, and the `GODEBUG` defaults of the binary); Renovate never bumps it, on purpose. Never delete the `toolchain` line (`go mod tidy` drops it if it is not newer than the `go` line), and raise the `go` line only as a deliberate change. `govulncheck.yml` fails a pull request (or a merge-queue run) only on reachable known vulnerabilities its base branch does not already have, listing the others, and fails its weekly run on `main` on any; reproduce the weekly run with `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`. Details: `.github/renovate.md`, section "Go version".

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

The provider has three optional credentials, and a fourth way to obtain the third. At least one must be configured:

| Credential | Provider arg | Env var | Client field | Used by |
|---|---|---|---|---|
| Standard | `api_key` | `ANTHROPIC_API_KEY` | `pd.Client` | All standard resources and data sources |
| Admin | `admin_api_key` | `ANTHROPIC_ADMIN_API_KEY` | `pd.AdminClient` | Organization endpoints (`/v1/organizations/*`, e.g. workspaces) |
| OAuth bearer (`org:admin`) | `auth_token` | `ANTHROPIC_AUTH_TOKEN` | `pd.OAuthClient` | Endpoints that reject API keys and require `Authorization: Bearer` (Workload Identity Federation, [#137](https://github.com/ippontech/terraform-provider-anthropic/issues/137)) |
| OAuth bearer, minted by WIF | `federation` block | `ANTHROPIC_FEDERATION_RULE_ID` etc. fill it, never enable it | `pd.OAuthClient` | Same endpoints as `auth_token`, which it conflicts with; it wins over `ANTHROPIC_AUTH_TOKEN` |

Each is resolved by `resolveCredential` (`internal/provider/provider.go`): the provider argument wins when set, the env var otherwise, and an Unknown value (an unresolved reference at plan time) counts as unset.

A fifth argument, `workspace_id`, is not a credential: it becomes the `anthropic-workspace-id` default header on `pd.Client` **only**, since an identity-linked key not bound to one workspace is rejected with a 400 without it (doc "Select a workspace", re-read 2026-10-13). The admin client is organization-wide and a federated bearer picks its workspace at exchange time, so neither gets the header (`TestConfigureWorkspaceIDHeader`). It is **configuration only, no environment fallback**: `ANTHROPIC_WORKSPACE_ID` is the SDKs' federation variable, exported by other tools in the same job and by our own WIF guide for `federation.workspace_id`, and a **single-workspace key answers 404 `Workspace not found` to any `anthropic-workspace-id` header, its own workspace included** (measured live 2026-10-29 on `GET /v1/models` with the `terraform-tests` key; a malformed header value is a 400). An earlier draft of #333 shared the variable with `federation.workspace_id`, which would have broken every single-workspace key in a shell following the WIF guide; the PR review caught it. The value is validated at plan time against `^wrkspc_[0-9A-Za-z]+$` (`TestWorkspaceIDValidator`), so a `file()` with a trailing newline fails the plan rather than every request. Unknown at plan is an **error** (`TestConfigureRejectsUnknownWorkspaceID`), same rule as the federation block and unlike the credentials, since a workspace ID is routinely a reference to a resource of the same module.

Two details are load-bearing:

- **`pd.OAuthClient` is a `*providerdata.OAuthClient` wrapper, not a bare `*anthropic.Client`.** The SDK carries no notion of which credential a client holds, so two bare clients are mutually assignable and mixing them up compiles silently, surfacing only as a 401 at apply time. The wrapper keeps the compiler in the loop; [#187](https://github.com/ippontech/terraform-provider-anthropic/issues/187) extends the same treatment to the other clients when `internal/admin` is retired. Because the wrapper adds a level of indirection the other guards do not have, `requireOAuthClient` checks `client.Client != nil` as well as `client != nil` — a non-nil wrapper around a nil SDK client would otherwise pass the guard and nil-deref on the first API call.
- **Every SDK client is built through `newSDKClient`, which passes `option.WithoutEnvironmentDefaults()`.** `anthropic.NewClient` otherwise prepends `DefaultClientOptions()`, whose chain has five sources: `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, the profile named by `ANTHROPIC_PROFILE`, env-var federation, and the fallback profile under the SDK config dir. The first two set a header *before* the explicit option is applied, so with both variables exported — the normal case — a client would present both credentials and the endpoints behind each reject the other. The last three are worse than a stray header: `option.WithConfig` applies a profile's non-credential settings **unconditionally**, so a profile left active by `ant auth login` (the command the provider docs tell operators to run) would override the base URL and stamp its `workspace_id` as an `anthropic-workspace-id` header on every request, none of it visible in the Terraform config. The marker option closes all five. Three tests cover it — `TestConfigureClientsCarryExactlyOneCredential`, `TestConfigureStandardClientDropsInheritedBearer`, `TestConfigureIgnoresTheAmbientProfile` (which plants a profile via `ANTHROPIC_CONFIG_DIR`) — and all fail if it is dropped.
- **`ANTHROPIC_BASE_URL` is re-applied by hand**, because the marker option skips it too. It is read with an explicit `!= ""` check: an exported-but-empty value must not replace the SDK's production default with `""`. The same trap applies in tests, so `clearCredentialEnv` genuinely `os.Unsetenv`s each variable (after a `t.Setenv` whose only purpose is the restore-on-cleanup it registers) rather than setting it to `""`.
- **The `federation` block is the provider's own WIF exchange** (`internal/provider/federation.go`). It fills `pd.OAuthClient` through `option.WithFederationTokenProvider`, whose token cache re-exchanges inside the SDK's refresh window (advisory 120s, mandatory 30s before expiry) and calls the identity source each time. That is why the sources are functions: `identity_token_file` is re-read and `identity_token_command` re-run per exchange, so an assertion is never presented twice (the exchange rejects a repeated `jti`, [WIF reference](https://platform.claude.com/docs/en/manage-claude/wif-reference#jwt-verification)). The command gets a timeout and a `WaitDelay`, because the kill reaches only argv[0] and the children of `sh -c` otherwise hold stdout open; the timeout is a const passed to `commandIdentityToken` as an argument (same rule as the consistency waits: a test shortens it without a mutable global); an `exec.ErrWaitDelay` with a non-empty stdout keeps the token (the program exited, a child it left behind holds the pipe); a cancelled parent context returns `ctx.Err()` rather than a `signal: killed` pointing at stderr; its stderr goes to `tflog.Debug`, never to the diagnostic, because a failing CLI can echo the token there. Four rules are load-bearing:
  - **Opt-in by block only.** The SDK's federation variables fill the block's attributes but never activate it, because other tools in the same job export them (`TestConfigureFederationEnvironmentAloneDoesNothing`).
  - **It conflicts with `auth_token`, and wins over `ANTHROPIC_AUTH_TOKEN`.** Both would fill `pd.OAuthClient`. The check reads the configuration (`!IsNull()`), so an `auth_token` unknown at plan still conflicts (`TestConfigureFederationConflictsWithAuthToken`, `TestConfigureFederationConflictsWithUnknownAuthToken`). The variable gives way to the block, as the environment does to every provider argument, with a warning because the SDKs rank it above their own federation (`TestConfigureFederationIgnoresAuthTokenEnvironment`).
  - **The client still goes through `newSDKClient`**, so `WithoutEnvironmentDefaults` keeps profiles and env credentials out of it.
  - **Unknown at plan is an error, per attribute.** `resolveCredential` reads an Unknown as unset, which for this block would configure a different identity source or report a value as missing that the configuration does set (`TestConfigureFederationRejectsUnknownAttributes`).

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

Left alone, Terraform's post-apply refresh lands inside that window and the next plan shows a phantom in-place update — this is what made `TestAccVaultResource_update` flaky. `vault_resource.go` therefore calls `awaitVaultUpdateVisible` after a successful update: it polls `Get` until `updated_at` is no older than the timestamp the write returned. The wait is **best-effort** — a read error, a timeout or a cancelled context returns without a diagnostic, because the write already succeeded and failing the apply would turn a cosmetic staleness window into a hard error.

Two details of that loop are load-bearing. The ceiling (5s) and interval (200ms) are **consts passed as arguments**, not package-level `var`s: the unit tests need to shrink them to milliseconds, and the vaults acceptance tests exercise the real `Update` in the same test binary, so a mutable global would race the moment any vault test opted into `t.Parallel()` (`make test` runs with `-parallel=10`). And a `401`, `403` or `404` from the poll (`isTerminalVaultReadError`) bails out immediately instead of retrying to the deadline — a vault deleted out-of-band, or a key that lost access to it, will never converge, and retrying would stall the apply for seconds. Every other failure keeps polling, since it says nothing about visibility.

Compare the two timestamps with a **non-strict** comparison — `!vault.UpdatedAt.Before(writtenAt)`, not `vault.UpdatedAt.After(writtenAt)`: an `updated_at` **equal** to the write timestamp must count as visible, or the loop always times out. `anthropic_vault_credential` has the same structural exposure but no observed flakiness, and its endpoint was not probed — check before assuming it needs the same wait.

### PII attributes and example outputs

Do **not** mark user PII attributes (`email`, `name`, etc.) as `Sensitive: true` in a data source/resource schema. They are the legitimate product of a lookup, not credentials, and schema-level sensitivity forces every consumer into `sensitive = true` outputs or `nonsensitive()` wrappers. This matches the GitLab provider, whose `gitlab_user` resource and `gitlab_user`/`gitlab_users` data sources mark only `password` sensitive, never `email`/`name`. Reserve `Sensitive: true` for true secrets (see write-only attributes above).

Instead, prevent PII from leaking into CI logs at the **example-output** layer: any example `output` that surfaces an email/name must set `sensitive = true` (e.g. `examples/data-sources/organization_member[s]/data_source.tf`). The native tests run those example modules via `terraform test`, and the live-API jobs (`testacc.yml`: `push` to `main` + `workflow_dispatch`, never `pull_request`) run under the public repo, so a non-sensitive output would print real addresses in world-readable Actions logs. Marking the output sensitive renders `(sensitive value)` instead. An output can always upgrade a non-sensitive source attribute to sensitive, so no schema change is needed.

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
