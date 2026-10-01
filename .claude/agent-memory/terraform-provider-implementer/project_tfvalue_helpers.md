---
name: tfvalue-helpers
description: Use internal/tfvalue (StringOrNull, TimeOrNull) for state mapping of absent API fields; never add a per-package nullable-string or RFC 3339 helper
metadata:
  type: project
---

`internal/tfvalue` exports `StringOrNull(s string) types.String` and `TimeOrNull(t time.Time) types.String`: the repo-wide way to map the SDK zero values ("" and the zero time) of absent API fields to null in state. On 2026-09-30 the userprofiles mappers re-implemented it as `userProfileNullableString`/`userProfileTimestamps` and memory_common.go hand-rolled `Format(time.RFC3339)`; the maintainer flagged the duplicate and it was removed in #293.

**Why:** the repo forbids per-package copies of shared helpers (same rule as admintest/oauthtest).

**How to apply:** import `github.com/ippontech/terraform-provider-anthropic/internal/tfvalue` in every `map<Resource>ToState`; before adding any unexported helper, grep `internal/` for an existing one.
