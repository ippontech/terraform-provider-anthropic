---
name: Test file placement — tests must live next to the code they test
description: Flag tests whose subject belongs to a different package or file than where they're written
type: project
---

Tests must be co-located with the code they test:

- Tests for `admin.Client`, `admin.APIError`, `admin.IsNotFound` → `internal/provider/admin/admin_client_test.go` (package `admin`)
- Tests for workspace-specific logic (e.g. `parseAllowedInferenceGeos`) → `workspace_resource_unit_test.go`
- Tests for a `*_resource.go` file → a `*_unit_test.go` in the same package, testing only symbols from that file

**Why:** Prior to 2026-05-04, `TestAdminAPIError_Error`, `TestIsNotFound`, and all `TestAdminClient_*` tests lived in `workspace_resource_unit_test.go`, making it hard to find tests and causing `admin` package behaviour to be tested from the wrong package.

**How to apply:** When reviewing a `*_unit_test.go` file, grep for test function names and check that each tested symbol is defined in the corresponding source file. If a test imports `admin` and calls `admin.X` on something that's not used by the resource itself, it's misplaced — flag as a Warning.
