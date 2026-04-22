---
name: Data source Go acceptance test convention
description: All anthropic_* data sources in this repo have a matching Go acceptance test file; singletons without one should be flagged
type: project
---

Every data source in `internal/provider/` in this provider historically ships with a `*_data_source_test.go` containing a `TestAcc<Name>DataSource_basic` using `resource.Test` and `testAccProtoV6ProviderFactories`. Examples: `skill_data_source_test.go` (PR #44, commit 9fc4957), `agent_data_source_test.go`, `model_data_source_test.go`. PR #48 (`skill_version` data source) shipped only a `.tftest.hcl` — the only data source so far missing a Go acceptance test.

**Why:** The repo convention (visible in CLAUDE.md) is both unit/acc tests under `internal/provider/` AND a `.tftest.hcl` under `tests/`. Go acceptance tests run via `make testacc` and catch regressions CI-side; tftest requires a locally installed provider.

**How to apply:** When reviewing a new `*_data_source.go`, always check for a sibling `*_data_source_test.go` and flag as a warning if missing. Compare against the sibling data source's test (e.g. `skill_data_source_test.go`) for the idiomatic shape.
