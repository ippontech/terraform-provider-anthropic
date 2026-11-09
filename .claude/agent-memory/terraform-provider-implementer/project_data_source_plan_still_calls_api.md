---
name: project-data-source-plan-still-calls-api
description: A Terraform native test with command = plan still invokes a data source's real Read when all inputs are known — the plan-only trick only works for resources.
metadata:
  type: project
---

Checked on 2026-10-25: still the pattern in `tests/` (`grep -l mock_provider tests/*.tftest.hcl`). Discovered implementing `anthropic_memory`/`anthropic_memories` (#122). CLAUDE.md's convention for Admin/WIF **resources** ("`command = plan` never calls the API since a resource never applies") does **not** carry over to data sources: Terraform evaluates a data source's Read during `plan` whenever its config attributes are all known, since the result is needed to compute the plan. A `tests/<name>.tftest.hcl` with `command = plan` plus constant well-formed IDs (the pattern used for e.g. `anthropic_invite`) still made a real, unauthenticated request and failed with 401.

**Fix:** for a data-source-only native test where the example can't chain off a real created resource (no anchor to produce a valid ID), use `mock_provider "anthropic" {}` instead of a `provider "anthropic" { <dummy credential> }` + `command = plan` block. `mock_provider` fully stubs the provider so `terraform test` never calls the API at all, at either plan or apply. Applies to any read-only data source whose only example inputs are constants.

**How to apply:** before assuming `command = plan` is enough for a new data source's native test with constant/dummy inputs, run it once against your build, through the dev override: `GOBIN="$PWD/.gobin" make install .dev.tfrc && TF_CLI_CONFIG_FILE="$PWD/.dev.tfrc" terraform -chdir=tests test -filter=<name>.tftest.hcl` (a bare `terraform test` uses the published provider). A live 401 or network error means switch to `mock_provider "anthropic" {}`. A schema error saying the data source is unknown means the dev override was not used, not that plan-only is enough.
