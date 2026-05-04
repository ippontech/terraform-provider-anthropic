---
name: Two-key provider model — standard vs admin client
description: Provider has two optional clients; which one a resource uses determines which API key it requires
type: project
---

The provider accepts two independent API keys, and at least one must be set:

- `ANTHROPIC_API_KEY` → initialises `ProviderData.Client` (`*anthropic.Client`) — standard SDK, used by most resources and data sources
- `ANTHROPIC_ADMIN_API_KEY` → initialises `ProviderData.AdminClient` (`*admin.Client`) — custom HTTP client for `/v1/organizations/*` endpoints (workspaces, etc.)

Either key alone is sufficient; both can be set simultaneously.

**Which client to use:**
- Default for all new resources/data sources: `pd.Client` (standard SDK)
- Resources managing organization-level objects (workspaces, members): `pd.AdminClient`

**Why:** Changed in session 2026-05-04 to unblock users who only manage workspaces and don't need the standard API key.

**How to apply:** When implementing a new resource, check the API endpoint. If it's under `/v1/organizations/`, use `pd.AdminClient` and `providerrors.RequireAdminResourceClient`. Otherwise use `pd.Client` and `providerrors.RequireResourceAPIClient`.
