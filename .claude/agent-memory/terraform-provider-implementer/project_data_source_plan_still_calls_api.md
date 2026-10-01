---
name: project-data-source-plan-still-calls-api
description: A Terraform native test with command = plan still invokes a data source's real Read when all inputs are known — the plan-only trick only works for resources.
metadata:
  type: project
---

Discovered implementing `anthropic_memory`/`anthropic_memories` (#122). CLAUDE.md's convention for Admin/WIF **resources** ("`command = plan` never calls the API since a resource never applies") does **not** carry over to data sources: Terraform evaluates a data source's Read during `plan` whenever its config attributes are all known, since the result is needed to compute the plan. A `tests/<name>.tftest.hcl` with `command = plan` plus constant well-formed IDs (the pattern used for e.g. `anthropic_invite`) still made a real, unauthenticated request and failed with 401.

**Fix:** for a data-source-only native test where the example can't chain off a real created resource (no anchor to produce a valid ID), use `mock_provider "anthropic" {}` instead of a `provider "anthropic" { <dummy credential> }` + `command = plan` block. `mock_provider` fully stubs the provider so `terraform test` never calls the API at all, at either plan or apply. Applies to any read-only data source whose only example inputs are constants (see [[project_memory_store_sdk_quirks]] for the sibling resource's conventions).

**How to apply:** before assuming `command = plan` is enough for a new data source's native test with constant/dummy inputs, actually run `terraform -chdir=tests test -filter=<name>.tftest.hcl` once against the dummy-provider version first — if it errors with a live 401/network call rather than a schema error, switch to `mock_provider "anthropic" {}`.
