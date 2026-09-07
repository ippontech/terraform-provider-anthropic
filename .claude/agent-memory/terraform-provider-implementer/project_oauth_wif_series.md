---
name: project-oauth-wif-series
description: "Patterns for OAuth-gated WIF resources (#137 series): PreCheckOAuth, param.Opt null-vs-omit, and native-test command=plan without an assert block"
metadata:
  type: project
---

The Workload Identity Federation admin endpoints (`client.Beta.Organization.*`) reject both standard and admin API keys — only the OAuth bearer client (`pd.OAuthClient` / `providerrors.RequireOAuthResourceClient`) works. `anthropic_service_account` (#200) is the first resource in this series; more are expected (federation rules, issuers, etc. — see #137).

**`acctest.PreCheckOAuth`**: unlike `PreCheck`/`PreCheckAdmin` (which `t.Fatal`), this one `t.Skip`s when `ANTHROPIC_AUTH_TOKEN` is unset — per the spec that shipped in the first WIF task prompt, not a convention I inferred. No test org exists for these writes and CI has no durable org:admin token, so OAuth-gated acceptance tests run locally only.

**Native test for a resource behind a credential this environment doesn't have**: with `command = plan`, do NOT add an `assert` block referencing a `Computed`-only attribute (e.g. `id`) — it's unknown at plan time for a not-yet-created resource and the assert would need a known boolean. The two prior resource-only `command = plan` tests (`api_key.tftest.hcl`, `workspace_member.tftest.hcl`) route around this with a fixture that hardcodes plan-time-known inputs and asserts only on those (never on `id`). The service_account spec instead shipped with **no assert block at all** — just `module { source = ... }` — matching `organization_data_source.tftest.hcl`'s bare-plan pattern. Confirmed empirically: in this environment `ANTHROPIC_AUTH_TOKEN` is not set, so `make terraform-test` fails Configure itself (`Missing OAuth Token`) for `service_account.tftest.hcl` while all 32 other native tests pass — this is expected/designed, not a bug, mirroring how admin-API `command = plan` tests would fail without `ANTHROPIC_ADMIN_API_KEY`.

**`param.Opt[string]` null vs omit, tested via the SDK's own exported helpers**: `param.NewOpt(v)` (has a value), `param.Null[string]()` (explicit JSON `null`), and the zero-value `Opt[string]{}` (omitted). Don't hand-roll a `MarshalJSON`-sniffing helper to distinguish them in unit tests — the SDK exports `param.IsNull(v)` and `param.IsOmitted(v)` directly (`anthropic-sdk-go/packages/param`). `Opt.Valid()` is *not* the right check for "is this an explicit null" — it returns `false` for both omitted and null, so a test asserting "field carries an explicit null" must use `param.IsNull`, not `!Valid()`.

Related: [[project_go_toolchain_mismatch]] (needed to get `go build`/`make` working in this worktree at all), [[project_api_key_model]].
