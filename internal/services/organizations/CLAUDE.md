# Organizations service notes

Load-bearing details for `internal/services/organizations/`: the `anthropic_invite` and `anthropic_organization_member` resources and the `anthropic_organization`, `anthropic_organization_member(s)` and `anthropic_invite(s)` data sources (Admin API, `internal/admin` client). The root `CLAUDE.md` still applies; where a note here says "see above" or "see below" and the target is not in this file, it is a section of the root `CLAUDE.md`. Record new details about this service here, not in the root file.

Since this was written, `anthropic_organization_member` became an import-only resource (its Create fails and points to `terraform import`). It cannot create a user, so the `organization_member` data source example still has to chain off `organization_members`.

**Resources:**
- `anthropic_invite` (`internal/services/organizations/invite_resource.go`) — manages an organization invite (Admin API, raw `admin.Client`); immutable — Create/Read/Delete only, `email`/`role` are `RequiresReplace`, no Update endpoint exists; the invite endpoints (`POST`/`GET`/`DELETE /v1/organizations/invites[/{id}]`) require a literal `?beta=true` query parameter not mentioned in the original issue spec; Read treats any non-`pending` `status` (`accepted`/`expired`/`deleted`) as gone and calls `RemoveResource` so Terraform re-creates the invite on next apply; Delete tolerates a 404 as already-gone; no live acceptance test (inviting sends a real email, no dedicated test org per #58) — `command = plan` native test only

**Data sources:**
- `anthropic_organization` (`internal/services/organizations/organization_data_source.go`) — fetches the organization tied to the admin key (admin API `GET /v1/organizations/me`); takes no input; exposes `id`, `name`, `type`
- `anthropic_organization_member` (`internal/services/organizations/organization_member_data_source.go`) — fetches a single organization member (User) by user ID (admin API `GET /v1/organizations/users/{user_id}`); input `id`; exposes `email`, `name`, `role`, `added_at`, `type`. No managed "user" resource exists to anchor an unknown at plan time, so the example chains off `organization_members` (`members[0].id`) to resolve a real ID — a constant `id` would trigger a live 404 during the native `command = plan` test
- `anthropic_organization_members` (`internal/services/organizations/organization_members_data_source.go`) — lists all organization members (admin API `GET /v1/organizations/users`) with an optional `email` filter and transparent cursor pagination
