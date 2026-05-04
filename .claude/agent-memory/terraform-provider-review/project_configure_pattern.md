---
name: Configure method — providerrors helpers are mandatory
description: Flag any Configure method that nil-checks pd.Client or pd.AdminClient inline instead of using internal/errors/ helpers
type: project
---

All `Configure` nil-client guards must use helpers from `internal/errors/` (aliased as `providerrors`). Inline `if pd.Client == nil { resp.Diagnostics.AddError(...) }` blocks are a code smell — the helper was introduced specifically to eliminate them.

Correct pattern:
```go
if !providerrors.RequireResourceAPIClient(pd.Client, &resp.Diagnostics) {
    return
}
r.client = pd.Client
```

Four helpers exist:
- `RequireResourceAPIClient` / `RequireDataSourceAPIClient` — for `pd.Client`
- `RequireAdminResourceClient` / `RequireAdminDataSourceClient` — for `pd.AdminClient`

**Why:** Established 2026-05-04 to remove 8-line repeated blocks across 14 files. Inline checks that bypass the helpers re-introduce duplication and diverge from the approved pattern.

**How to apply:** Flag as a Warning any Configure method with an inline nil check on `pd.Client` or `pd.AdminClient`. Also flag any resource that assigns `r.client = pd.Client` with no preceding guard.
