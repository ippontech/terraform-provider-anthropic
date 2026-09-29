---
name: invites-beta-query-param
description: Admin API invite endpoints (get/list/delete) require a literal ?beta=true query param, and list uses `statuses` (plural, repeatable) not `status`
metadata:
  type: project
---

`GET/DELETE /v1/organizations/invites[/{id}]` require `?beta=true` appended to the path — confirmed against the vendored Go SDK's `BetaOrganizationInviteService` (v1.67.0/v1.68.0 identical here), which appends it to every invite request. This is unlike `organization_member(s)` (`/v1/organizations/users`), which take no such flag. Live-checked 2026-09-29: `GET /v1/organizations/invites?beta=true&limit=5` against Ippon's org returned real accepted invites (`taufort@ippon.fr`, `bpinel@ippon.fr`), confirming the flag and the field set (`id`, `type`, `email`, `role`, `invited_at`, `expires_at`, `status`, `rbac_group_ids`, `accepted_at`).

The list endpoint's status filter is `statuses` (repeatable query param, OR'ed), not `status` — matches `BetaOrganizationInviteListParams.Statuses []string`.

**Why:** the task spec (issue #83) said plain `/v1/organizations/invites/{id}` and a `status` filter; both were stale relative to the SDK. Trust the vendored SDK struct/service over an issue body when the two disagree — see [[project_sdk_covers_admin_api]] and [[feedback_verify_sdk_against_origin_main]] for the general pattern.

**How to apply:** any future work touching `internal/services/organizations/invite*.go` must keep the `?beta=true` suffix and `statuses` param name. If the API drops the beta flag, `internal/services/organizations/invite_data_source_internal_test.go` and `invites_data_source_internal_test.go` will need the httptest server's `beta=true` assertion relaxed.
