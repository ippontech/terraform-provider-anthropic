# CLAUDE.md

Repo-wide rules for this Terraform provider. Each `internal/services/<service>/` directory has its own `CLAUDE.md` with the details of its resources and data sources (see [Services](#services)): put per-resource notes there, and only repo-wide rules here.

## Architecture

A Terraform provider on the [Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework) v1.19.0, served by `main.go` as `registry.terraform.io/ippontech/anthropic`.

- `internal/provider/`: `provider.go` (schema, credentials, client construction, registration in `Resources()` / `DataSources()`) and `federation.go` (the `federation` block).
- `internal/services/<service>/`: resources and data sources, one directory per Anthropic service.
- `internal/admin/`: client for the Admin API (`/v1/organizations/*`), which the SDK does not cover. `DoRequest(ctx, method, path, body, opts...)` takes per-request options: `admin.WithBeta("…")` for the `anthropic-beta` header that beta organization endpoints require (they answer 404 without it: plugins, webhooks, telemetry destinations; listing spend limits needs `spend-limit-reads-2026-09-26`, while getting, setting or deleting one spend limit takes no beta header) and `admin.WithHeader`; a body of type `admin.RawBody{ContentType, Data}` is sent as is (multipart uploads) instead of being marshalled to JSON. An `*admin.APIError` carries the raw `error.details` object in `Details` when the API sends one (the Plugins API's `error_code`, `plugin_id`, ...).
- `internal/providerdata/`: `ProviderData`, passed to every `Configure`, and the `OAuthClient` wrapper.
- `internal/errors/` (import alias `providerrors`): nil-client guards for `Configure`, and `IsNotFound(err)` for SDK errors (see [404 handling](#404-handling)).
- `internal/tfvalue/`: `StringOrNull` / `TimeOrNull` map the SDK zero values of absent API fields (`""`, the zero time) to null. Use them in every state mapping instead of a per-package helper.
- `internal/retry/` (import alias `provretry`): the retry policy of every SDK client (`Middleware`, installed by `newSDKClient` through `SDKOptions`, see [SDK client retries](#sdk-client-retries)) and the multipart bundle helper `MultipartUpload`.
- `internal/acctest/`, `admintest/`, `oauthtest/`, `providertest/`, `schematest/`, `sdktest/`, `wifprobetest/`: test helpers, see [Testing](#testing).
- `examples/` (`provider/`, `resources/<name>/`, `data-sources/<name>/`): configs embedded in the docs and sourced by the native tests. `templates/`: doc templates that `make generate` renders to `docs/`. `tests/`: native tests. `tools/tools.go`: build-time tool imports only.
- `tools/` is its own Go module. `tools/coversummary/` is the standard-library-only program behind the coverage gate of the CI `coverage` job (rules and exclusions in `.coverage.json`, baseline in `.coverage-baseline`) and its per-file table.

### Anthropic Go SDK

The Anthropic Go SDK stays on the **v1** line, currently **v1.84.0**: API version `2023-06-01`, the API key sent as `x-api-key`, beta endpoints called with `?beta=true`. `renovate.json` holds it there with an `allowedVersions: "<2.0.0"` rule (v1 patches still automerge; each v1 minor gets its own PR, reviewed by hand, because v1 minors have changed the wire format without breaking the build): v2 moves the client to API version `2026-10-21`, which is breaking for this provider (`temperature`, `api_keys.workspace_id` and the rate-limit `group_type` are removed, skills come back in their GA shape only, the API key goes out as `Authorization: Bearer`), so it waits for [#192](https://github.com/ippontech/terraform-provider-anthropic/issues/192) in `major/2.0.0`. A second rule disables major SDK updates always, OSV vulnerability-fix PRs included, since their own `allowedVersions` replaces the hold. `.github/renovate.md` explains why the hold lives in config rather than in guidance. #192 is unblocked per the docs (the GA shapes are live without the beta headers) but stays a breaking change.

The v1.67.0 → v1.84.0 bump changed nothing on the wire beyond the SDK's own version headers (`User-Agent`, `X-Stainless-Package-Version`), and two places carry that guarantee:

- **Skills keep the beta shape.** SDK v1.68.0 retyped `client.Beta.Skills` to the GA shape (`display_name`, `latest_version_id`, a `source` object, `time.Time` timestamps, no `directory`/`version` on versions) and stopped sending `anthropic-beta: skills-2025-10-02`. Under that header the API keeps answering in the beta shape (`display_title`, `latest_version`, a string `source`, `directory`/`version` on versions: probed live on 2026-09-01 and 2026-09-29, see #192; the SDK v1.68.0 changelog says the same). So every skills call passes `Betas: skillsBetas()`, create sends `display_title` as a multipart extra field, and the exposed attributes are decoded from the response's `RawJSON()` into the provider-local `betaSkill`/`betaSkillVersion` (`internal/services/skills/skill_beta_shape.go`), never from the typed SDK fields. Each field is filled the way the v1.67.0 decoder filled a string (absent or null → `""`, any other non-string → its JSON text), and timestamps reach state verbatim, never re-formatted through `time.Time`. The package's `*_internal_test.go` files pin the header, `?beta=true`, the multipart field names and every mapped value against beta-shaped fixtures. All of it goes away with the GA migration in #192.
- **Other request bodies stayed as they were.** `buildMultiagentParams` fills the multiagent union branch the configured `type` selects (SDK v1.80.0; a `coordinator` roster is sent exactly as before). A custom tool's `input_schema` kept losing its root-level keys other than `type`/`properties`/`required` in the bump itself (v1.67.0 dropped them, v1.75.0+ keeps them in `ExtraFields`); a separate fix stopped clearing them, so `buildToolsParams` now sends `additionalProperties`, `$defs` and the like as configured, and the API stores the schema the configuration declares (`TestAccAgentResource_withCustomTools` checks the echo live).

## Services

Each directory below has a `CLAUDE.md` with the load-bearing details of its resources and data sources: API quirks, live probes, test gating. Claude Code loads it when a file in that directory is read or edited; read it before changing the service. Each line gives the resources, then after the semicolon the data sources (names without the `anthropic_` prefix), then the credential (see [Credentials and clients](#credentials-and-clients)).

- `agents/`: `agent`; `agent`, `agents`. Standard key.
- `apikeys/`: `api_key` (import-only); `api_key`, `api_keys`. Admin key.
- `compliance/`: anthropic_compliance_settings singleton resource (admin API, raw `admin.Client`).
- `compliance/`: see its `CLAUDE.md`.
- `deployments/`: `deployment`; `deployment_runs`. Standard key.
- `environments/`: `environment`; `environment`, `environments`. Standard key.
- `externalkeys/`: anthropic_external_key resource + external_key/external_keys data sources (CMEK key records; admin API, raw `admin.Client`).
- `externalkeys/`: see its `CLAUDE.md`.
- `federation/`: anthropic_federation_issuer + anthropic_federation_rule + anthropic_federation_rule_workspace resources + federation_issuer(s)/federation_rule(s)/federation_rule_workspaces data sources (Beta; WIF issuers and rules, org:admin OAuth bearer only).
- `files/`: `file`; `file`, `files` (Files API GA). Standard key.
- `memorystores/`: `memory_store`, `memory`; `memory_store(s)`, `memory`, `memories`. Standard key.
- `messages/`: `message`; `count_tokens`. Standard key.
- `models/`: none; `model`, `models`. Standard key.
- `organizations/`: `invite`, `organization_member` (import-only); `organization`, `organization_member(s)`, `invite(s)`. Admin key.
- `organizationwebhooks/`: anthropic_organization_webhook resource (admin API, raw `admin.Client`, beta header `webhooks-2026-10-07` on every call via `doOrganizationWebhookRequest`).
- `organizationwebhooks/`: see its `CLAUDE.md`.
- `plugins/`: Claude Enterprise Plugins API (beta `ce-plugins-2026-09-01`, raw `admin.Client`, Enterprise scoped admin key): anthropic_plugin + anthropic_plugin_version resources; multipart bodies built in plugin_source.go, requests in plugin_requests.go; no acceptance tests (Console test org), httptest + plan only.
- `plugins/`: see its `CLAUDE.md`.
- `rbac/`: anthropic_rbac_group + anthropic_rbac_group_member resources + rbac_group/rbac_groups data sources (Claude Enterprise only; raw `admin.Client` since RBAC is in the SDK only from v1.77.0; own package rather than organizations/ because groups are enterprise-wide with their own `rbac_groups` scopes, like federation/ and serviceaccounts/ sit beside organizations/).
- `rbac/`: see its `CLAUDE.md`.
- `serviceaccounts/`: `service_account`, `service_account_workspace`; `service_account(s)`, `service_account_workspaces`. OAuth bearer.
- `skills/`: `skill`, `skill_version`; `skill(s)`, `skill_version(s)`. Standard key.
- `spendlimits/`: anthropic_spend_limit resource (raw `admin.Client`, SDK has them only from v1.77.0; Claude Enterprise `user` limits, Console `organization`/`workspace` limits in early access).
- `spendlimits/`: see its `CLAUDE.md`.
- `telemetrydestinations/`: anthropic_telemetry_destination resource (OpenTelemetry export; admin API, raw `admin.Client`, beta header `telemetry-destinations-2026-08-11` on every call via `doTelemetryRequest`).
- `telemetrydestinations/`: see its `CLAUDE.md`.
- `tunnels/`: anthropic_tunnel + anthropic_tunnel_certificate resources + tunnel/tunnels data sources (MCP tunnels, research preview; `client.Beta.Tunnels`/`client.Beta.Tunnels.Certificates`, workspace-scoped `/v1/tunnels`, never the deprecated `/v1/organizations/tunnels`; beta header `mcp-tunnels-2026-06-22` sent by the SDK; `pd.OAuthClient` carrying a `workspace:manage_tunnels` token, API keys rejected).
- `tunnels/`: see its `CLAUDE.md`.
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
- Clients pointed at an `httptest` server come from the shared helpers, in every package: `admintest.NewClient(t, srv)` for an `*admin.Client`, `oauthtest.NewClient(t, srv)` for a `*providerdata.OAuthClient`, `oauthtest.NewSDKClient(t, srv)` for a bare `*anthropic.Client` that holds a bearer (it delegates to `sdktest.NewAuthTokenClient`; both `oauthtest` helpers take extra `option.RequestOption`s, e.g. `option.WithMaxRetries(0)`), and `sdktest.NewAPIKeyClient(t, srv)` for a standard-key client (older tests still build theirs inline with `option.WithAPIKey("test")`). `admintest.ListPage[T]` writes one Admin list page (`data`/`has_more`/`first_id`/`last_id`). ImportState tests get their null state from `schematest`, which also builds a `Config`, `Plan` or `State` with a few attributes set (`Value/Config/Plan/State(t, r, overrides)` and the data source twins `DataSourceSchema/ObjectType/NullValues`, `NullDataSourceConfig`, `DataSourceConfig`) and matches a diagnostic detail (`HasErrorDetail`).
- **Fake-API tests** drive a resource or data source through Terraform (`resource.UnitTest`, no `TF_ACC`, no credential, no network) against `internal/sdktest`, the shared fake of the API (no `provider` import): `sdktest.NewServer(t)` with a route table, request recorder, error envelope, `after_id` and `page` pagination with cursor checks, an in-memory `Collection`, `FailNext`/`ResetNext` and exact assertions, plus `NewAPIKeyClient`/`NewAuthTokenClient`, `NewStatusServer`, `NewSequenceServer`, `NewCursorPagedServer` and `TerminalReadStatuses`. The provider comes from `providertest.ProviderFactories(srv)` (`provider.WithBaseURL`, no `t.Setenv`, so `t.Parallel` works; `providertest` imports `provider`, so external test packages only) and the configuration starts with `providertest.ProviderConfig(sdktest.APIKey)` (or `AdminKey`, `AuthToken`; the credentials not named are set to `""`, so the environment cannot fill them). They live in `package <service>_test` in files named `<name>_fake_test.go`, plus one `fake_test.go` per service holding its routes and handlers (each behaviour citing its API reference page; payloads use the SDK's wire names, which can lag the reference). They call `t.Parallel()`. The standard per resource: every apply step checks `plancheck.ExpectResourceAction` in `PreApply` (update in place versus replace, as the schema promises) and `plancheck.ExpectEmptyPlan()` in `PostApplyPostRefresh`; `ConfigStateChecks` assert exact typed values (`statecheck.ExpectKnownValue` with `knownvalue.StringExact`, `Int64Exact`, `ObjectExact`, `ListExact`), not `TestCheckResourceAttrSet`; an import step keeps `ImportStateVerify`; a delete behind Terraform's back (remove the object from the fake in `PreConfig`) plans a create; a 404 on `Delete` succeeds (`srv.FailNext(route, 404, 1)`); `CheckDestroy` reads the fake's store. Per data source: exact values, an empty list as `knownvalue.ListExact([]knownvalue.Check{})`, two pages or more through the real `Read` with `srv.AssertWalks` and `srv.AssertAllPagesRead`, and the not-found error. Request shape after `resource.UnitTest` returns: `srv.AssertCalled(route, n)` with an exact count for writes (Terraform reads a data source a number of times that belongs to terraform-plugin-testing, so reads use `AssertWalks`), `AssertJSONBodies` for exact bodies, `AssertBeta`/`AssertNoBeta`, `AssertCredential`. `ExpectError` patterns use `\s+` between words: Terraform wraps diagnostics at 80 columns. A bug a fake test exposes stays in the test behind `t.Skip("bug: ...")` until its fix lands. The Terraform CLI must be on `PATH` (CI installs it with `setup-terraform` in the `test` job).

**Coverage gate.** The `coverage` job of `provider.yml` is a required check, and it fails rather than warns (`.coverage.json` holds the thresholds and the exclusions, `.coverage-baseline` the baseline; `tools/coversummary` applies them):

- at least 75% of the executable statements a pull request adds or changes in non-test Go files must be covered by unit tests, so write the tests with the code (fake-API tests through `resource.UnitTest` need no credential);
- the total, outside the exclusions, stays at or above `.coverage-baseline` minus 0.5 point; when a change raises it, the job summary gives the value to commit in `.coverage-baseline` in the same pull request;
- the total never drops under 80%, and the baseline never goes back under it: the baseline is past 80%, so this floor is absolute;
- every package outside the exclusions stays at 80% or more (`package_min` in `.coverage.json`), not only the total: a package under 80% fails the job. The job summary lists every package with its coverage and its distance to 80%, least covered first. A new package starts at 80%, with its tests.

The exclusions are the test-helper packages, `main.go` and `tools/`, nothing else, and there is no ignore comment: code that cannot be unit-tested is a reason to restructure it, not to exclude it. Run `make cover` before pushing; `go test -cover` needs the `covdata` tool (see Commands).

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
- On every pull request, `terraform.yml` runs `make terraform-fmt-check`, `make terraform-validate` and `make terraform-test-mock`, with no credentials and an egress list without `registry.terraform.io` or `api.anthropic.com` (dev_overrides: neither `init` nor `validate` downloads the provider). `terraform-test-mock` picks every test file containing `mock_provider` by grep, so such a file must never need a credential or the live API; a file that mixes a mock with a live run breaks every pull request. The live native tests still run only after merge (`testacc.yml`).

## Environment

A gitignored `.env` at the project root holds machine-specific variables and local credentials. `mise.toml` loads it (`[env] _.file = ".env"`), so its variables are exported on entering the directory, in the Claude Code shell too. Never `source`, read or print `.env`: it holds secrets. A missing `.env` (CI, a fresh worktree) is fine. Claude Code's global settings also deny `Read` on `.env` files.

Secrets never go into a commit. `.gitleaks.toml` extends gitleaks' default rules with Claude Enterprise scoped keys (`sk-ant-api01-`); the pre-commit hook runs it on the staged changes, and `gitleaks.yml` on the commits of every pull request and, weekly, on the whole history. A deliberate fake in a test (a truncated PEM block, a made-up key) takes an inline `gitleaks:allow` comment at the end of its line, in the commit that adds the line (the PR scan reads every commit, so a later commit adding the comment does not clear the finding); do not add a path or rule allowlist to `.gitleaks.toml` for it.

After upgrading Go via mise, run `go clean -cache` before `make`; otherwise golangci-lint's typecheck fails with "version does not match go tool version".

`go.mod` carries two Go versions. `toolchain go1.26.10` is the Go CI tests with and releases are built with: `actions/setup-go` reads it before the `go` line, Renovate proposes its updates by default and its OSV check matches it against the standard-library advisories. `go 1.26.2` is the language version (minimum Go, and the `GODEBUG` defaults of the binary); Renovate never bumps it, on purpose. Never delete the `toolchain` line (`go mod tidy` drops it if it is not newer than the `go` line), and raise the `go` line only as a deliberate change. `govulncheck.yml` fails a pull request (or a merge-queue run) only on reachable known vulnerabilities its base branch does not already have, listing the others, and fails its weekly run on `main` on any; reproduce the weekly run with `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`. Details: `.github/renovate.md`, section "Go version".

## Commands

```bash
make build          # Compile the provider
make install        # Build and install locally
make fmt            # Format Go code
make tidy-check     # Fail if go.mod/go.sum are not tidy (go mod tidy -diff)
make lint           # Run golangci-lint on the whole tree, the tools module included (.golangci.yml)
make test           # Run unit tests (300s timeout per package, 10 parallel workers)
make cover          # make test with a coverage profile, then the CI coverage gate against origin/main (COVER_BASE=<ref>) and the per-file table
make testacc        # Run Go acceptance tests (requires TF_ACC=1, 120m timeout)
make terraform-test # Run Terraform native tests (builds provider, uses .dev.tfrc)
make terraform-fmt-check  # terraform fmt -check -diff -recursive (offline)
make terraform-validate   # terraform validate in all 63 example directories against the provider just built (offline)
make terraform-test-mock  # Only the native test files with a mock_provider block (offline, no credentials)
make generate       # Regenerate docs and format examples
make complexity     # Cognitive complexity ratchet of the CI complexity job, against origin/main (COMPLEXITY_BASE=<ref>)
make                # Default: fmt tidy-check lint complexity test install generate
go test -run TestName -v ./internal/services/agents/  # One service
```

`.golangci.yml` (v2) enables the standard linters plus errorlint, gosec, depguard, gocognit (20, production code only: `_test.go` files and the test-helper packages are excluded) and 15 others, with gofmt and goimports (this module's imports in their own last group). It lints the whole tree, existing code and new alike, and reports 0 issues: keep it there. `tools/` is a module of its own, which a run from the root does not see, so `make lint` and the CI lint job also run golangci-lint in `tools/` with `--config ../.golangci.yml`. Fix a finding rather than silence it; when the code is right as it is (a gosec finding by design, a signature an interface imposes), put `//nolint:<linter> // <reason>` on that line, for that one linter, and never add a file-wide or configuration-wide exclusion. depguard keeps `acctest`, `admintest`, `oauthtest`, `providertest`, `schematest`, `sdktest`, `wifprobetest` and `net/http/httptest` out of non-test files, and `internal/provider` out of the services. CI pins golangci-lint in `provider.yml` (`version:` with a Renovate annotation); keep it in step with `mise.toml`. The pre-commit hooks run `golangci-lint fmt` on the Go files that differ from `origin/main` (the staged ones on commit; never an untouched file, even with `pre-commit run -a`), `make lint` and `go mod tidy -diff` through mise.

The complexity ratchet (`make complexity`, the required `complexity` job of `provider.yml`, `tools/complexity`) scores every function of the non-test Go files under `internal/` and `tools/` (test-helper packages, `testdata` directories and generated files aside) with gocognit, as golangci-lint does, in the working tree and at the merge base with `origin/main`. A function above 20 at the merge base may change only if its score does not rise; any other function, new or not, scores 20 or less. Nothing is committed: the functions allowed above 20 are those above it on `main`, and none is left since the refactoring lot, so the cap is absolute. When a change must touch one of them, put the new logic in a helper rather than in the function. `//gocognit:ignore` and `//nolint` exempt nothing from the ratchet. golangci-lint's own gocognit (20) checks every function too, whatever the change edits.

Before committing a feature or a fix, run `make`, then `pre-commit run -a`.

`go test -cover` needs the `covdata` tool, which the toolchains Go downloads through `GOTOOLCHAIN` (`golang.org/toolchain@v0.0.1-go1.26.10...`) lack: the run fails with `go: no such tool "covdata"` on packages without test files. The CI toolchain from `setup-go` has it. Locally, build it once into a GOROOT overlay and run that `go`:

    TC=$(go env GOROOT); G=$HOME/.cache/goroot-covdata/$(basename $TC)
    mkdir -p $G/pkg/tool/linux_amd64
    for e in $TC/*; do [ "$(basename $e)" = pkg ] || ln -sfn $e $G/; done
    for e in $TC/pkg/*; do [ "$(basename $e)" = tool ] || ln -sfn $e $G/pkg/; done
    ln -sf $TC/pkg/tool/linux_amd64/* $G/pkg/tool/linux_amd64/
    (cd $TC/src/cmd/covdata && GOROOT=$TC GOTOOLCHAIN=local $TC/bin/go build -o $G/pkg/tool/linux_amd64/covdata .)
    GOROOT=$G GOTOOLCHAIN=local $G/bin/go test -cover ./...

## Provider coding conventions

### Credentials and clients

Three optional credentials, plus a fourth way to obtain the third; at least one must be configured:

| Credential | Provider arg | Env var | Client field | Used by |
|---|---|---|---|---|
| Standard | `api_key` | `ANTHROPIC_API_KEY` | `pd.Client` | All standard resources and data sources |
| Admin | `admin_api_key` | `ANTHROPIC_ADMIN_API_KEY` | `pd.AdminClient` | Organization endpoints (`/v1/organizations/*`), through `internal/admin` |
| OAuth bearer (`org:admin`, or `workspace:manage_tunnels` for MCP tunnels) | `auth_token` | `ANTHROPIC_AUTH_TOKEN` | `pd.OAuthClient` | Endpoints that reject API keys (Workload Identity Federation, [#137](https://github.com/ippontech/terraform-provider-anthropic/issues/137); MCP tunnels, with a token of the other scope in a separate, aliased provider block) |
| OAuth bearer minted by WIF | `federation` block | `ANTHROPIC_FEDERATION_RULE_ID` etc. fill it, never enable it | `pd.OAuthClient` | Same as `auth_token`, which it conflicts with; it wins over `ANTHROPIC_AUTH_TOKEN` |

`resolveCredential` (`internal/provider/provider.go`) takes the provider argument when set, the env var otherwise; an Unknown value at plan time counts as unset.

`admin_api_key` is sent as `x-api-key`, so besides a Console Admin API key (`sk-ant-admin01-`) it carries a Claude Enterprise scoped key (`sk-ant-api01-`, member and invite endpoints only) or a personal / service account key not scoped to a workspace. The `> **Auth**:` line of each Admin API template names which of these the endpoint accepts and, for members and invites, the Enterprise scopes (`read:members` or `read:org_audit` to read, `write:members` to write, `write:rbac_groups` for the `managed` role); `templates/index.md.tmpl` holds the full table. Keep both in step when adding an organization resource.

- `pd.OAuthClient` is a `*providerdata.OAuthClient` wrapper, not a bare `*anthropic.Client`, so that mixing up two clients fails to compile instead of returning a 401 at apply time ([#187](https://github.com/ippontech/terraform-provider-anthropic/issues/187) does the same for the other clients when `internal/admin` is retired). `requireOAuthClient` therefore checks `client.Client != nil` as well as `client != nil`.
- Every SDK client is built through `newSDKClient`, which passes `option.WithoutEnvironmentDefaults()` so that a client carries only the credential passed in: otherwise the SDK adds the environment's credentials (each endpoint rejects the other key) and the active `ant auth login` profile's base URL and workspace header, none of it visible in the Terraform configuration. Only a non-empty `ANTHROPIC_BASE_URL` is re-applied. Details in the doc comments of `newSDKClient` and of the test helper `clearCredentialEnv`; three `TestConfigure*` tests fail if the option is dropped. Tests that run the whole provider against a fake do not touch the variable: `provider.New(version, provider.WithBaseURL(url))` points the standard, Admin and OAuth clients (and the federation exchange) at the fake and wins over the variable; `main.go` passes no option, so users cannot reach it.
- The `federation` block (`internal/provider/federation.go`) mints `pd.OAuthClient` through `option.WithFederationTokenProvider`, which calls the identity source on every exchange, so the sources are functions (file re-read, command re-run): the exchange rejects a repeated `jti`. Only the block enables federation, never the SDK's federation variables alone; it conflicts with `auth_token`, even one unknown at plan, and wins over `ANTHROPIC_AUTH_TOKEN` with a warning; its client goes through `newSDKClient`; an attribute unknown at plan is an error. The file's comments and the `TestConfigureFederation*` tests hold the reasons and the command's timeout and stderr handling.
- `workspace_id`, a fifth argument, is not a credential: it becomes the `anthropic-workspace-id` default header on `pd.Client` only, because a key bound to no single workspace is rejected (400) without it (doc "Select a workspace", re-read 2026-10-13). The admin client is organization-wide and a federated bearer picks its workspace at exchange time, so neither gets the header (`TestConfigureWorkspaceIDHeader`). It has no environment fallback: `ANTHROPIC_WORKSPACE_ID` is the SDKs' federation variable, exported by other tools in the same job and by our WIF guide for `federation.workspace_id`, and a single-workspace key answers 404 `Workspace not found` to any `anthropic-workspace-id` header, its own workspace included (measured live on 2026-10-29 on `GET /v1/models` with the `terraform-tests` key; a malformed value is a 400). It is validated at plan time against `^wrkspc_[0-9A-Za-z]+$` (`TestWorkspaceIDValidator`), so a trailing newline fails the plan, and an unknown value at plan is an error (`TestConfigureRejectsUnknownWorkspaceID`), since it is routinely a reference to a resource of the same module.

### SDK client retries

Every SDK client gets its retries from `provretry.Middleware`, installed by `newSDKClient` with `provretry.SDKOptions(provretry.DefaultPolicy)`, which also sets `option.WithMaxRetries(0)` so the SDK does not retry on top of it. It starts from the Admin client's policy (see [Admin client retries](#admin-client-retries)), applied per request, and also replays a POST on 529, on a connection error before it got a connection, and whenever it leaves the same state:

- idempotent requests keep the SDK's rule, 2 retries on 408, 409, 429, 5xx and connection errors: GET, DELETE and the rest of RFC 9110, and the POSTs that leave the same state when sent twice (`idempotentPOST` in `internal/retry/policy.go`): `/v1/messages/count_tokens`, which stores nothing, the `/archive`, `/pause` and `/unpause` actions, and the update of an object whose update has no precondition (environment, vault, vault credential, memory store, deployment, user profile, service account, federation issuer and rule);
- every other request (a POST that creates an object, uploads a file, sends a message, runs an action such as a deployment run or a token rotation, or updates with a precondition, the agent `version` or the memory `content_sha256`) is replayed only on 429 and 529 (rate limited, overloaded: a capacity limit, not a failure of the request) and on a connection error raised before the request got a connection (DNS, dial, TLS handshake, seen through an `httptrace` `GotConn` hook), when none of it reached the API. After another 5xx, a 408, a 409 or a connection lost once established, the write may have landed, and no endpoint documents an idempotency key: a replay could leave an archive-only duplicate, a second file or a second billed message, or turn an applied precondition update into a misleading 409. Such a failure fails the apply; the next apply converges. A new SDK update or action route is non-idempotent until it is added to `idempotentPOST` with a `TestIsIdempotent` case;
- `x-should-retry` from the server wins either way for an idempotent request; for any other it can only turn a retry off (`true` says the error is transient, not that the write did not land). `retry-after-ms` / `retry-after` set the delay, capped at 60s (`provretry.MaxRetryAfter`, as in `internal/admin`; the SDK honours it without a bound); otherwise 0.5s doubling to 8s with jitter.

Never add a retry loop around an SDK call, and never pass `option.WithMaxRetries` to a call: either multiplies the attempts (the skill upload used to send nine POSTs for one 5xx). A unit test that counts requests builds its client with `provretry.SDKOptions(...)` to get production behaviour; `option.WithMaxRetries(0)` on a bare client hides what production does (it hid three `POST /v1/files` per 5xx).

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

### 404 handling

A 404 means the object is gone, and every resource treats it the same way:

- `Read` calls `resp.State.RemoveResource(ctx)` and returns without a diagnostic, so an object deleted outside Terraform is re-created by the next apply instead of failing every refresh.
- `Delete` (and the archive call of `archive_on_destroy`) returns without a diagnostic: the object is already gone, which is what destroy wants. This covers a destroy run with `-refresh=false` and an object deleted between the refresh and the delete.

Test the error with `providerrors.IsNotFound(err)` for the SDK clients (standard and OAuth; the Go SDK has one error type, `*anthropic.Error`, for every status) and `admin.IsNotFound(err)` for `internal/admin`. Some older resources still inline `errors.As(err, &apierr) && apierr.StatusCode == 404`; converting them is part of [#296](https://github.com/ippontech/terraform-provider-anthropic/issues/296) PR 2. Any other error still produces a diagnostic. Only Terraform shows a refresh that fails, so test both cases with a fake-API test (see [Go tests](#go-tests)): remove the object from the fake between two steps, and `srv.FailNext(route, 404, 1)` before a `Destroy` step.

### Admin client retries

`internal/admin` retries idempotent methods on connection errors and transient statuses (408, 409, 429, 5xx), but a `POST` only on 429: after a 5xx, a 409 or a dropped connection the write may have landed, and a replay would create a duplicate workspace or an untracked membership. Retries are opt-in: `admin.NewClient` retries twice, the zero-value client from `admintest.NewClient` makes one attempt. A test that exercises retrying sets `MaxRetries` and millisecond `BaseRetryDelay` / `MaxRetryDelay`, and its stub sends no `retry-after` header, which would bypass both delays. Details: the `admin.Client` doc comment.

Admin list endpoints page with `after_id` / `has_more` / `last_id`, and `last_id` may be `null` (e.g. https://platform.claude.com/docs/en/api/organization/users/list). A hand-written list loop stops on `!has_more`, and when `has_more` comes with a null or empty `last_id` or with the cursor it just sent it returns an error diagnostic rather than requesting the same page again forever or returning a truncated list: `admin.NextAfterID(afterID, lastID)` gives the next cursor or that error (`api_keys`, `organization_members`, `invites`, `workspace_members`, `workspaces`). Test a list loop through the data source's real `Read` (`TestListDataSources_cursorGuard` in `list_cursor_guard_internal_test.go`), never through a copy of the loop in the test.

### Multipart uploads

Upload a bundle of files under a common directory name (skills) with `provretry.MultipartUpload(ctx, filePaths, bundleRoot, dirName, fn)`, taking `bundleRoot` and `dirName` from `provretry.DeriveBundleRoot(filePaths)`. Each part is named `dirName/<path relative to bundleRoot>`, so subdirectories survive; files outside `bundleRoot`, or a path equal to it, are rejected. It opens the files and calls `fn` once: the SDK buffers the multipart body, so the client's retry policy replays it (only on 429, 529 or a connection error before it got a connection, since the POST creates an object; see [SDK client retries](#sdk-client-retries)). `anthropic_file` uploads a single file with its own helper (see `internal/services/files/CLAUDE.md`).

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

### Secrets the API returns once
A secret the API **generates** and returns only on create (or on an explicit regenerate call), never on a read — `anthropic_organization_webhook.signing_secret` — cannot be write-only: the configuration does not hold it, and the user needs it from state. Make it Computed + Sensitive, set it from the create response before any follow-up call can fail, never touch it in the Read mapping (so a refresh keeps the prior value, null after an import), and pair it with an Optional `<name>_version` Int64 whose change to a new non-null value makes Update call the regenerate endpoint and store the new value. A custom plan modifier plans the state value unless that version changes (unknown then), so an unchanged resource shows no diff. Document in the template that the secret lives in state, as `aws_iam_access_key` does, and keep it out of any data source.
### ConfigValidator Unknown handling

In a `ConfigValidator`, treat Unknown values (unresolved references at plan time) as neither set nor missing, or valid configurations fail spuriously: `isMissing(v) = v.IsNull() && !v.IsUnknown()` for required checks, `isSet(v) = !v.IsNull() && !v.IsUnknown()` for conflicting-attribute checks. Reference: `vaultCredentialConfigValidator`.

### Defaults inside an Optional + Computed object
Do not put a schema `Default` on an attribute nested in an Optional + Computed single-nested attribute whose omission means "keep the API's values". The framework applies nested defaults to the proposed new state, which for an omitted block is the prior state's object, so the default overwrites the stored value, the plan differs from state, and every Computed attribute is marked unknown before `UseStateForUnknown` restores the block: a spurious update on every plan. Make the nested attribute Optional + Computed and plan the default with a modifier that only acts when the block is configured (`tracesDefaultString`/`tracesDefaultBool` in `internal/services/telemetrydestinations/telemetry_destination_resource.go`).
### Map attributes with PATCH semantics (metadata)

The API's `metadata` uses PATCH semantics on update: omitted keys are kept, a key set to `null` is deleted (agents: https://platform.claude.com/docs/en/api/beta/agents/update; environments also delete a key set to `""`). Sending only the planned map cannot remove a key dropped from the configuration, and `{}` changes nothing; the API keeps the keys and Terraform reports an inconsistent result after apply. Use `tfvalue.MetadataPatch(ctx, plan, state)`: it upserts planned keys and sets keys present in the prior state but absent from the plan to `nil` (a null plan deletes them all), sent with `params.SetExtraFields(map[string]any{"metadata": patch})` because the typed `map[string]string` cannot carry nulls; omit the field when the patch is empty. `SetExtraFields` replaces the extra fields, so a resource that sends another one (agent: `multiagent: null`) collects them in one map and calls it once. `anthropic_agent` and `anthropic_environment` use it; vaults, vault credentials, memory stores and deployments still have local copies (`buildMetadataPatch`), to be replaced in [#296](https://github.com/ippontech/terraform-provider-anthropic/issues/296) PR 1. Clear a nullable scalar such as `display_name` with `param.Null[string]()`, not by omitting it.

Two rules go with it:

- Keep such a `metadata` (and any user-provided field the API never fills) `Optional` without `Computed`: `Optional` + `Computed` + `UseStateForUnknown` plans the prior value when the attribute is removed, so removing it silently does nothing.
- The API returns `{}` both when there is no metadata and after `metadata = {}` cleared it: map an empty response to null, except when the plan or prior state is a known empty map, which is kept (`tfvalue.MetadataAfterEmptyResponse`; otherwise `metadata = {}` fails with an inconsistent result).

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

The `pr-title` check (`.github/workflows/pr-title.yml`) enforces this on the PR title: `<type>[(scope)][!]: <subject>` with a type among `feat`, `fix`, `perf`, `revert`, `docs`, `style`, `chore`, `refactor`, `test`, `build` and `ci` (the conventionalcommits preset types semantic-release reads, without the `feature` alias, which no release rule matches), or GitHub's `Revert "<title>"`. On PRs to `main` (except the `major/<version>` release PR, merged with a merge commit) it refuses `!` and a `BREAKING CHANGE` line in the description (the parser's rule: `BREAKING CHANGE` or `BREAKING-CHANGE` at the start of any line, any case, after spaces or `*`, then `:` or a space), either of which would publish a major release. Only `feat`, `fix`, `perf`, `build`, `refactor`, `docs`, reverts and breaking changes publish a release (`.releaserc`); `chore`, `ci`, `test` and `style` do not, so a change that must reach users (a dependency security fix, say) needs `fix(deps):` or `build:`.

Breaking changes (`feat!:`, a `BREAKING CHANGE:` footer) target the long-lived staging branch of the next major (`major/2.0.0` for the first one), not `main`, and reach `main` in a single merge commit at release time. `.releaserc` pins `branches` to `["main"]` so no staging branch can become a semantic-release release branch. Procedure: `RELEASE.md`, section "Major versions".
