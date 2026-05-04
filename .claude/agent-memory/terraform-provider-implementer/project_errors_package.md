---
name: internal/errors package — providerrors pattern
description: All Configure nil-client guards must use helpers from internal/errors/, never inline checks
type: project
---

The `internal/errors/` package (`package errors`) contains shared helpers for guarding against unconfigured clients in resource and data source `Configure` methods. Import it as `providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"`.

Four exported functions cover all cases:
- `providerrors.RequireResourceAPIClient(pd.Client, &resp.Diagnostics)` — standard resources
- `providerrors.RequireDataSourceAPIClient(pd.Client, &resp.Diagnostics)` — standard data sources
- `providerrors.RequireAdminResourceClient(pd.AdminClient, &resp.Diagnostics)` — admin resources
- `providerrors.RequireAdminDataSourceClient(pd.AdminClient, &resp.Diagnostics)` — admin data sources

Each returns `bool`; the caller must `return` when it returns `false`.

**Why:** Eliminates 8-line repeated nil-check blocks across 14+ files. Established in session 2026-05-04 when the provider was refactored to make `ANTHROPIC_API_KEY` optional.

**How to apply:** Use the appropriate helper in every new `Configure` method. Never write `if pd.Client == nil { resp.Diagnostics.AddError(...) }` inline.
