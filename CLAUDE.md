# CLAUDE.md

Repo-wide rules for this Terraform provider. Each `internal/services/<service>/` directory has its own `CLAUDE.md` with the details of its resources and data sources (see [Services](#services)): put per-resource notes there, and only repo-wide rules here.

## Architecture

A Terraform provider on the [Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework) v1.19.0, served by `main.go` as `registry.terraform.io/ippontech/anthropic`.

- `internal/provider/`: `provider.go` (schema, credentials, client construction, registration in `Resources()` / `DataSources()`) and `federation.go` (the `federation` block).
- `internal/services/<service>/`: resources and data sources, one directory per Anthropic service.
- `internal/admin/`: client for the Admin API (`/v1/organizations/*`), which the SDK does not cover.
- `internal/providerdata/`: `ProviderData`, passed to every `Configure`, and the `OAuthClient` wrapper.
- `internal/errors/` (import alias `providerrors`): nil-client guards for `Configure`.
- `internal/tfvalue/`: `StringOrNull` / `TimeOrNull` map the SDK zero values of absent API fields (`""`, the zero time) to null. Use them in every state mapping instead of a per-package helper.
- `internal/retry/` (import alias `provretry`): multipart bundle upload with 5xx retry.
- `internal/acctest/`, `admintest/`, `oauthtest/`, `schematest/`, `wifprobetest/`: test helpers, see [Testing](#testing).
- `examples/` (`provider/`, `resources/<name>/`, `data-sources/<name>/`): configs embedded in the docs and sourced by the native tests. `templates/`: doc templates that `make generate` renders to `docs/`. `tests/`: native tests. `tools/tools.go`: build-time tool imports only.

### Anthropic Go SDK pin

Keep the SDK at v1.67.0, including in `chore(deps)` changes. v1.68.0 renames skill fields (`display_title` → `display_name`, `latest_version` → `latest_version_id`) and drops `directory` and `version` from skill versions, but the deployed API still sends the v1.67.0 names: checked live on 2026-09-01 on `GET /v1/skills` and `GET /v1/skills/{id}/versions`, under `anthropic-beta: skills-2025-10-02` (the only skills beta, unchanged in v1.68.0). Bumping would silently read `""` into the renamed fields and drop two returned attributes; it needs the API rename first, then a documented migration. `renovate.json` holds the SDK below 1.68.0, security PRs included: an OSV vulnerability-fix PR's own `allowedVersions` replaces the cap, so two more rules disable its `minor`/`major` updates while the SDK is below v1.68.0, and its `major` updates always. 1.67.x patches still flow; `.github/renovate.md` explains why. Re-run the check before revisiting, and lift the cap only as part of [#192](https://github.com/ippontech/terraform-provider-anthropic/issues/192).

## Services

Each directory below has a `CLAUDE.md` with the load-bearing details of its resources and data sources: API quirks, live probes, test gating. Claude Code loads it when a file in that directory is read or edited; read it before changing the service. Each line gives the resources, then after the semicolon the data sources (names without the `anthropic_` prefix), then the credential (see [Credentials and clients](#credentials-and-clients)).

- `agents/`: `agent`; `agent`, `agents`. Standard key.
- `apikeys/`: `api_key` (import-only); `api_key`, `api_keys`. Admin key.
- `deployments/`: `deployment`; `deployment_runs`. Standard key.
- `environments/`: `environment`; `environment`, `environments`. Standard key.
- `federation/`: `federation_issuer`, `federation_rule`, `federation_rule_workspace`; `federation_issuer(s)`, `federation_rule(s)`, `federation_rule_workspaces`. OAuth bearer. Also holds the WIF staleness probe results.
- `files/`: `file`; `file`, `files` (Files API GA). Standard key.
- `memorystores/`: `memory_store`, `memory`; `memory_store(s)`, `memory`, `memories`. Standard key.
- `messages/`: `message`; `count_tokens`. Standard key.
- `models/`: none; `model`, `models`. Standard key.
- `organizations/`: `invite`, `organization_member` (import-only); `organization`, `organization_member(s)`, `invite(s)`. Admin key.
- `serviceaccounts/`: `service_account`, `service_account_workspace`; `service_account(s)`, `service_account_workspaces`. OAuth bearer.
- `skills/`: `skill`, `skill_version`; `skill(s)`, `skill_version(s)`. Standard key.
- `userprofiles/`: `user_profile`; `user_profile(s)`. Standard key (beta not enabled for the test organization).
- `vaults/`: `vault`, `vault_credential`; none. Standard key. Also holds the vaults read-after-write measurements.
- `workspaces/`: `workspace`, `workspace_member`; `workspace(s)`, `workspace_member(s)`, `workspace_rate_limits`. Admin key.

## Adding a resource or data source

1. Create `internal/services/<service>/<name>_resource.go` (or `_data_source.go`) in `package <service>`, implementing `resource.Resource` (or `datasource.DataSource`).
2. Register its factory in `Resources()` (or `DataSources()`) in `internal/provider/provider.go`.
3. Add an example under `examples/resources/<name>/` (or `examples/data-sources/<name>/`). The main file (`resource.tf` / `data_source.tf`) holds only the resource or data blocks and outputs, because the Registry docs embed it verbatim. The `terraform {}` block (`required_version`, and `required_providers` mapping `anthropic` to `ippontech/anthropic` for the native tests) goes in a sibling `versions.tf` ([#231](https://github.com/ippontech/terraform-provider-anthropic/issues/231)). Pin the provider with `~> 1.0`. CI runs tflint with `preset = "all"`, so every `output` needs a `description` and every `versions.tf` a `required_version` (#229).
4. Add `templates/resources/<name>.md.tmpl` (or `templates/data-sources/<name>.md.tmpl`) with a non-empty `subcategory` (e.g. `"Agents"`, `"Messages"`); without it the page appears ungrouped on the Terraform Registry.
5. Add a native test `tests/<name>.tftest.hcl` and Go tests, see [Testing](#testing).
6. Run `make`, which also regenerates the docs, then `pre-commit run -a`.
7. Record load-bearing details in the service's `CLAUDE.md`; for a new service directory, create one and add it to [Services](#services).

Guides live in `templates/guides/<name>.md.tmpl` (rendered to `docs/guides/`, `subcategory` required too). Templates are Go text/templates: a literal `{{ ... }}`, such as `${{ vars.X }}`, fails `make generate` with `function "vars" not defined`, and since tfplugindocs wipes `docs/` first, every `docs/**/*.md` stays deleted until a successful re-run. Use placeholder values or escape as `{{"{{"}}`. Validate a guide's HCL by hand (`terraform validate` in a scratch directory, against a freshly built provider), since no native test covers it ([#240](https://github.com/ippontech/terraform-provider-anthropic/issues/240)).

## Testing

Unit tests need no environment variables. Go acceptance tests (`TestAcc` prefix, `resource.Test`) need `TF_ACC=1` and real keys; most native tests need real keys too.

### Go tests

- For every bug fix, refactoring or new helper under `internal/`, write or update unit tests in the same package, next to the code (e.g. `internal/retry/multipart_test.go`), and run `make test`.
- Acceptance tests import `internal/acctest`, so they live in `package <service>_test` (`acctest` imports `provider`, which imports every service). Tests that need unexported symbols use `package <service>`. For a single resource or data source with both, name them `<name>_internal_test.go` (internal package: mapping, pagination, query parameters, 404s on an `httptest` server) and `<name>_test.go` (acceptance), e.g. `agent_resource_internal_test.go` and `agent_resource_test.go`. The `TestAcc` / `Test` prefixes already separate the two kinds, so no file ends in `_unit_test.go` or `_acc_test.go`. Extract a `map<Resource>ToState` helper so the internal test covers the response-to-state mapping directly.
- Start every acceptance test, and every test that seeds fixtures through the SDK, with the matching `acctest.PreCheck` (standard key), `PreCheckAdmin`, `PreCheckOAuth` (skips without `ANTHROPIC_AUTH_TOKEN`) or `PreCheckMultiWorkspace` (skips without `ANTHROPIC_MULTI_WORKSPACE_API_KEY`) rather than a hand-written `TF_ACC` check. They call `SkipUnlessAcc` first, because fixture setup runs before `resource.Test`'s own `TF_ACC` gate, and a plain `make test` with credentials in `.env` would otherwise create real objects ([#296](https://github.com/ippontech/terraform-provider-anthropic/issues/296)). Providers come from `acctest.ProtoV6ProviderFactories`, live SDK clients from `acctest.NewAPIKeyClient()` / `acctest.NewOAuthClient()` (both `WithoutEnvironmentDefaults`, so the other credential is not sent), never from an inline `anthropic.NewClient`.
- Admin and OAuth clients pointed at an `httptest` server come from the shared helpers, in every package: `admintest.NewClient(t, srv)` for an `*admin.Client`, `oauthtest.NewClient(t, srv)` for a `*providerdata.OAuthClient`, `oauthtest.NewSDKClient(t, srv)` for a bare `*anthropic.Client` that holds a bearer (both `oauthtest` helpers take extra `option.RequestOption`s, e.g. `option.WithMaxRetries(0)`). There is no shared helper for the standard key: those tests build `anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))` themselves. ImportState tests get their null state from `schematest`.

What runs live depends on the credential:

- All tests run in the dedicated `terraform-tests` workspace. The test `ANTHROPIC_API_KEY` is scoped to it; Admin API tests target it with `acctest.TerraformTestsWorkspaceID`, never a `wrkspc_...` literal. A second workspace, `terraform-tests-2` (`acctest.TerraformTests2WorkspaceID`), serves only the `workspace_id` provider-argument tests (`vault_resource_workspace_test.go`). They run with `ANTHROPIC_MULTI_WORKSPACE_API_KEY`, a service account key member of both test workspaces and scoped to neither (a CI secret), and read the API out of band through `acctest.NewMultiWorkspaceClient(workspaceID)`. Use that key and that workspace for nothing else: every other test relies on `ANTHROPIC_API_KEY` being single-workspace, so that its objects land in `terraform-tests` without a header.
- Standard key: full CRUD acceptance tests, and native tests that apply for real.
- Admin key: there is no test organization yet ([#58](https://github.com/ippontech/terraform-provider-anthropic/issues/58)), so tests create no Admin API objects: native tests of Admin API resources use `command = plan`, while those of read-only data sources may read `terraform-tests` live. Read-only data sources get smoke acceptance tests (`PreCheckAdmin`, `terraform-tests`) that assert presence (`members.#`), not values, since live data is not deterministic; the `httptest` unit tests cover pagination, filters, mapping and 404s. When no constant ID is reliably valid, chain off the list data source, as `organization_member` does.
- OAuth bearer (`PreCheckOAuth`: every acceptance test in `federation/` and `serviceaccounts/`): not run for now. CI has no durable org:admin token ([#137](https://github.com/ippontech/terraform-provider-anthropic/issues/137)), and the only local one belongs to Ippon's production organization, where WIF objects can only be archived, so every run leaves archived issuers, service accounts and rules behind. Do not run these tests, or any live WIF probe, yourself or through an agent until a test organization exists (#58). Their native tests use `command = plan` (`tests/service_account.tftest.hcl`).

### Terraform native tests

- One `tests/<name>.tftest.hcl` per resource or data source. `make terraform-test` installs the provider, writes `.dev.tfrc` and runs `terraform -chdir=tests test`, so `tests/` is the root module: `tests/versions.tf` maps `anthropic` to `ippontech/anthropic` for the test files' `provider` and `mock_provider` blocks, and the repository root holds no Terraform configuration. Keep that file ([#233](https://github.com/ippontech/terraform-provider-anthropic/issues/233)).
- Each test sources the public example, relative to `tests/`: `source = "../examples/resources/message"`. A copy is not what users read, and the two drift ([#213](https://github.com/ippontech/terraform-provider-anthropic/pull/213) broke `generate` that way).
- Start the file with `test { parallel = true }`, which covers every `run` block. Check computed values with `assert` blocks.
- Plan-only resource tests (Admin API, OAuth) declare a dummy credential in the test file, `provider "anthropic" { admin_api_key = "dummy-admin-api-key" }` (or `auth_token = "dummy-auth-token"`): under `command = plan` a resource never calls the API and `Configure` only needs a non-empty string. Assert on the module's resources (`anthropic_workspace.example.name == ...`) rather than adding outputs. A data source is read during plan once its arguments are known, so this does not hold for data sources.
- Data sources CI cannot read live use `mock_provider "anthropic" {}` (plan and apply, no credentials); a single-object example takes a constant well-formed ID and `override_data` (`tests/federation_issuer_data_source.tftest.hcl`). `override_data` cannot add elements to a computed `list(object)`, so an example that indexes `list[0].id` reads the live API, as the Admin API `invite` and `organization_member` data sources do under `command = plan`.

## Environment

A gitignored `.env` at the project root holds machine-specific variables and local credentials. `mise.toml` loads it (`[env] _.file = ".env"`), so its variables are exported on entering the directory, in the Claude Code shell too. Never `source`, read or print `.env`: it holds secrets. A missing `.env` (CI, a fresh worktree) is fine. Claude Code's global settings also deny `Read` on `.env` files.

After upgrading Go via mise, run `go clean -cache` before `make`; otherwise golangci-lint's typecheck fails with "version does not match go tool version".

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
go test -run TestName -v ./internal/services/agents/  # One service
```

Before committing a feature or a fix, run `make`, then `pre-commit run -a`.

## Provider coding conventions

### Credentials and clients

Three optional credentials, plus a fourth way to obtain the third; at least one must be configured:

| Credential | Provider arg | Env var | Client field | Used by |
|---|---|---|---|---|
| Standard | `api_key` | `ANTHROPIC_API_KEY` | `pd.Client` | All standard resources and data sources |
| Admin | `admin_api_key` | `ANTHROPIC_ADMIN_API_KEY` | `pd.AdminClient` | Organization endpoints (`/v1/organizations/*`), through `internal/admin` |
| OAuth bearer (`org:admin`) | `auth_token` | `ANTHROPIC_AUTH_TOKEN` | `pd.OAuthClient` | Endpoints that reject API keys (Workload Identity Federation, [#137](https://github.com/ippontech/terraform-provider-anthropic/issues/137)) |
| OAuth bearer minted by WIF | `federation` block | `ANTHROPIC_FEDERATION_RULE_ID` etc. fill it, never enable it | `pd.OAuthClient` | Same as `auth_token`, which it conflicts with; it wins over `ANTHROPIC_AUTH_TOKEN` |

`resolveCredential` (`internal/provider/provider.go`) takes the provider argument when set, the env var otherwise; an Unknown value at plan time counts as unset.

- `pd.OAuthClient` is a `*providerdata.OAuthClient` wrapper, not a bare `*anthropic.Client`, so that mixing up two clients fails to compile instead of returning a 401 at apply time ([#187](https://github.com/ippontech/terraform-provider-anthropic/issues/187) does the same for the other clients when `internal/admin` is retired). `requireOAuthClient` therefore checks `client.Client != nil` as well as `client != nil`.
- Every SDK client is built through `newSDKClient`, which passes `option.WithoutEnvironmentDefaults()` so that a client carries only the credential passed in: otherwise the SDK adds the environment's credentials (each endpoint rejects the other key) and the active `ant auth login` profile's base URL and workspace header, none of it visible in the Terraform configuration. Only a non-empty `ANTHROPIC_BASE_URL` is re-applied. Details in the doc comments of `newSDKClient` and of the test helper `clearCredentialEnv`; three `TestConfigure*` tests fail if the option is dropped.
- The `federation` block (`internal/provider/federation.go`) mints `pd.OAuthClient` through `option.WithFederationTokenProvider`, which calls the identity source on every exchange, so the sources are functions (file re-read, command re-run): the exchange rejects a repeated `jti`. Only the block enables federation, never the SDK's federation variables alone; it conflicts with `auth_token`, even one unknown at plan, and wins over `ANTHROPIC_AUTH_TOKEN` with a warning; its client goes through `newSDKClient`; an attribute unknown at plan is an error. The file's comments and the `TestConfigureFederation*` tests hold the reasons and the command's timeout and stderr handling.
- `workspace_id`, a fifth argument, is not a credential: it becomes the `anthropic-workspace-id` default header on `pd.Client` only, because a key bound to no single workspace is rejected (400) without it (doc "Select a workspace", re-read 2026-10-13). The admin client is organization-wide and a federated bearer picks its workspace at exchange time, so neither gets the header (`TestConfigureWorkspaceIDHeader`). It has no environment fallback: `ANTHROPIC_WORKSPACE_ID` is the SDKs' federation variable, exported by other tools in the same job and by our WIF guide for `federation.workspace_id`, and a single-workspace key answers 404 `Workspace not found` to any `anthropic-workspace-id` header, its own workspace included (measured live on 2026-10-29 on `GET /v1/models` with the `terraform-tests` key; a malformed value is a 400). It is validated at plan time against `^wrkspc_[0-9A-Za-z]+$` (`TestWorkspaceIDValidator`), so a trailing newline fails the plan, and an unknown value at plan is an error (`TestConfigureRejectsUnknownWorkspaceID`), since it is routinely a reference to a resource of the same module.

### Configure method pattern

Every `Configure` casts `req.ProviderData` to `*providerdata.ProviderData`, guards the client with the helper from `internal/errors` (alias `providerrors`) for its kind, and returns when the helper reports false; never an inline `if pd.Client == nil` check:

```go
pd, ok := req.ProviderData.(*providerdata.ProviderData) // + ok check
if !providerrors.RequireResourceAPIClient(pd.Client, &resp.Diagnostics) {
    return
}
r.client = pd.Client
// data source: RequireDataSourceAPIClient(pd.Client, ...)
// admin:       RequireAdminResourceClient / RequireAdminDataSourceClient(pd.AdminClient, ...)
// OAuth:       RequireOAuthResourceClient / RequireOAuthDataSourceClient(pd.OAuthClient, ...)
```

### Admin client retries

`internal/admin` retries idempotent methods on connection errors and transient statuses (408, 409, 429, 5xx), but a `POST` only on 429: after a 5xx, a 409 or a dropped connection the write may have landed, and a replay would create a duplicate workspace or an untracked membership. Retries are opt-in: `admin.NewClient` retries twice, the zero-value client from `admintest.NewClient` makes one attempt. A test that exercises retrying sets `MaxRetries` and millisecond `BaseRetryDelay` / `MaxRetryDelay`, and its stub sends no `retry-after` header, which would bypass both delays. Details: the `admin.Client` doc comment.

### Multipart uploads

Upload a bundle of files under a common directory name (skills) with `provretry.MultipartUpload(ctx, filePaths, bundleRoot, dirName, fn)`, taking `bundleRoot` and `dirName` from `provretry.DeriveBundleRoot(filePaths)`. Each part is named `dirName/<path relative to bundleRoot>`, so subdirectories survive. `anthropic_file` uploads a single file with its own helper (see `internal/services/files/CLAUDE.md`).

Known inaccuracy, kept until the retry fix lands: `provretry` rests on the premise that the SDK cannot retry a multipart body, which is wrong for SDK v1.67.0. The SDK buffers the body and replays it, so both layers retry (up to nine `POST /v1/skills` for a persistent 5xx). A separate change will rework the upload retry policy.

### archive_on_destroy pattern

Where the API can archive as well as delete, expose an `archive_on_destroy` bool rather than a separate archive resource, in this exact shape (reference: `internal/services/environments/environment_resource.go`):

- schema: `Optional`, `Computed`, `Default: booldefault.StaticBool(false)`, `PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()}`, description "If `true`, destroying this resource archives it instead of permanently deleting it. Default: `false`.";
- `Delete` calls `Archive` when it is true, `Delete` otherwise;
- `Read` sets it to `false` when null or unknown (import), since the API does not return it.

An acceptance test whose `CheckDestroy` asserts archiving (`ArchivedAt != ""`) then hard-deletes the object, so archived objects do not pile up in the test organization.

### jsontypes.Normalized for JSON string attributes

An `Optional`, non-`Computed` attribute holding a JSON string uses `jsontypes.NormalizedType{}` as `CustomType` (schema and attr-type map) and `jsontypes.Normalized` as the model field type (`terraform-plugin-framework-jsontypes` v0.2.0). A plain `types.String` causes "Provider produced inconsistent result after apply" whenever the API's JSON differs from the user's `jsonencode()` in key order or whitespace. Map state from the raw JSON, never from `json.Marshal(sdkStruct)`, whose output changes with SDK upgrades:

```go
inputSchema := jsontypes.NewNormalizedNull()
if raw := t.SomeField.RawJSON(); raw != "" && raw != "null" {
    inputSchema = jsontypes.NewNormalizedValue(raw)
}
```

### Write-only secret attributes

Secrets that must never reach state use write-only attributes (plugin framework v1.11+, Terraform ≥ 1.11): `schema.StringAttribute{Optional: true, WriteOnly: true, Sensitive: true}` (`Optional` or `Required`, never `Computed`). Reference: `internal/services/vaults/vault_credential_resource.go`.

- In Create and Update, read them from `req.Config`, not the plan or state: `req.Config.GetAttribute(ctx, path.Root("token"), &v)`, nested via `path.Root("refresh").AtName("refresh_token")`. Never write them to state or fill them from API responses.
- Pair each with a normal `_wo_version` Int64 stored in state (e.g. `token_wo_version`): Terraform cannot diff a write-only value, and bumping the version triggers the Update that re-pushes the secret (rotation). Require it with a `ConfigValidator` whenever the secret is set, or changing the secret is a silent no-op (`vaultCredentialConfigValidator`).
- In Update, re-push the secret payload on any mutable-field diff, not only a version bump; otherwise a change to a non-secret field (e.g. `expires_at`) is dropped and Terraform reports an inconsistent result after apply. Mark truly immutable fields `RequiresReplace` so they never reach Update.

### ConfigValidator Unknown handling

In a `ConfigValidator`, treat Unknown values (unresolved references at plan time) as neither set nor missing, or valid configurations fail spuriously: `isMissing(v) = v.IsNull() && !v.IsUnknown()` for required checks, `isSet(v) = !v.IsNull() && !v.IsUnknown()` for conflicting-attribute checks. Reference: `vaultCredentialConfigValidator`.

### Map attributes with PATCH semantics (metadata)

The API's `metadata` uses PATCH semantics: omitted keys are kept, a key set to `null` is deleted. Sending only the planned map cannot remove a key dropped from the configuration; the API keeps it and Terraform reports an inconsistent result after apply. Use `buildMetadataPatch(ctx, plan, state)` (`internal/services/vaults/vault_resource.go`): it upserts planned keys and sets keys removed since the prior state to `nil`, sent with `params.SetExtraFields(map[string]any{"metadata": patch})` because the typed `map[string]string` cannot carry nulls. Clear a nullable scalar such as `display_name` with `param.Null[string]()`, not by omitting it.

### Read-after-write consistency waits

Some Beta managed-agents endpoints return the pre-update object on a `GET` right after an update, so the post-apply refresh shows a phantom in-place update on the next plan (this made `TestAccVaultResource_update` flaky; measurements in the `vaults/` and `federation/` `CLAUDE.md`). The fix is a wait at the end of `Update`, modelled on `awaitVaultUpdateVisible` (`vault_resource.go`): poll `Get` until `updated_at` is no older than the timestamp the write returned. Service accounts and federation rules use it on measured evidence, memories and deployments defensively. Keep its four properties:

- best-effort: a read error, a timeout or a cancelled context returns without a diagnostic, since the write already succeeded;
- the 5s ceiling and 200ms interval are consts passed as arguments, not package-level `var`s, so unit tests shrink them without racing parallel tests;
- 401, 403 and 404 end the poll at once (`isTerminalVaultReadError`); other errors keep polling;
- the comparison is non-strict (`!vault.UpdatedAt.Before(writtenAt)`), so an `updated_at` equal to the write timestamp counts as visible; a strict one always times out.

Probe an endpoint before adding a wait: `anthropic_vault_credential` has the same exposure but no observed flakiness and no probe.

### PII attributes and example outputs

Leave user PII attributes (`email`, `name`) without `Sensitive: true` in schemas: they are the product of a lookup, not credentials, and schema-level sensitivity forces every consumer into `sensitive` outputs or `nonsensitive()` (the GitLab provider's `gitlab_user` marks only `password`). Keep `Sensitive: true` for secrets. Instead, an example `output` that surfaces an email or a name sets `sensitive = true` (e.g. `examples/data-sources/organization_member[s]/data_source.tf`), because the native tests run the examples in the live-API jobs (`testacc.yml`: push to `main` and `workflow_dispatch`, never `pull_request`), whose logs are public.

## Conventions

Commit messages and PR titles follow [conventional commits](https://www.conventionalcommits.org/): `feat:`, `fix:`, `docs:` (documentation and examples), `refactor:`, `test:`, `ci:`, `chore:` (maintenance). PRs are squash-merged, and the PR title becomes the commit message.

Breaking changes (`feat!:`, a `BREAKING CHANGE:` footer) target the long-lived staging branch of the next major (`major/2.0.0` for the first one), not `main`, and reach `main` in a single merge commit at release time. `.releaserc` pins `branches` to `["main"]` so no staging branch can become a semantic-release release branch. Procedure: `RELEASE.md`, section "Major versions".
