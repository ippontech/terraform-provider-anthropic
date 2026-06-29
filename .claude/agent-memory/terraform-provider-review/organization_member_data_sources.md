---
name: organization_member_data_sources_review
description: organization_member and organization_members data sources review (June 2026); read-only smoke tests, comprehensive unit tests, consistent pattern
metadata:
  type: project
---

## Review: anthropic_organization_member and anthropic_organization_members data sources

**Branch**: feat/organization-member-data-sources
**Date**: 2026-06-30

### Implementation status: APPROVED

Both data sources are well-implemented, follow established patterns, and have adequate test coverage.

#### Key findings

1. **Configure methods correct** — Both use `providerrors.RequireAdminDataSourceClient()` guard before assignment; no inline `nil` checks.

2. **Admin API paths correct**
   - Single: `GET /v1/organizations/users/{user_id}`
   - List: `GET /v1/organizations/users?limit=1000&email=...&after_id=...`

3. **Pagination logic correct** — Cursor-based (`after_id`/`has_more`/`last_id`), aggregates all pages before state set.

4. **Email filter handling correct** — Checks both `IsNull()` and `IsUnknown()` before passing to query param; matches api_keys pattern.

5. **Error handling complete** — All API calls appended to diagnostics; parse and network errors captured; 404s properly handled in unit tests.

6. **Test coverage comprehensive**
   - **Unit tests** (internal package):
     - `TestOrganizationMemberDataSource_read` — fixtures, field-by-field assertions
     - `TestOrganizationMemberDataSource_notFound` — 404 error detection
     - `TestOrganizationMembersDataSource_listAll` — non-paginated list
     - `TestOrganizationMembersDataSource_pagination` — multi-page scenario with call count verification
     - `TestOrganizationMembersDataSource_buildQuery` — exercises the extracted `buildOrganizationMembersQuery` helper directly (email+cursor set; both omitted when empty)
   - **Acceptance tests** (smoke): Use `acctest.PreCheckAdmin()`, demonstrate chaining (members → member by ID), check field presence
   - **Terraform native tests**: Schema validation via `command = plan` (appropriate for Admin API read-only data sources)

7. **Test file naming correct** — Internal tests in `organization_member[s]_data_source_internal_test.go` (package `organizations`); acceptance in `organization_member[s]_data_source_test.go` (package `organizations_test`).

8. **Provider registration complete** — Both added to `DataSources()` in provider.go.

9. **Example configs meaningful** — Show direct lookup by ID (with comment about real-world usage), list all, filter by email, chaining; outputs useful.

10. **Schema and mapping aligned** — Model fields match API response; attr.Type map keys match JSON tags; state construction consistent across single/list.

11. **Templates have subcategory** — Both set `subcategory: "Organization"` for proper registry grouping.

12. **Version constraints correct** — Example configs pin `~> 1.0` per repo convention.

### No blocking issues

All checklist items pass. Code is production-ready.

### Follow-up (2026-06-30): tautological query-param test caught in re-review

This review's finding #4/#6 overstated coverage. The original
`TestOrganizationMembersDataSource_emailFilterPassedAsQueryParam` **hardcoded**
the request URL (`/v1/organizations/users?limit=1000&email=jane%40example.com`)
and asserted the test server received `email=jane@example.com` — it only proved
the HTTP client round-trips a URL it was handed. The data source's own param
construction (`params.Set("email", ...)`) was never executed, so a typo'd key or
dropped `Set` would not fail the test.

**Lesson for future reviews:** a unit test that builds the request URL itself and
then asserts the server saw that same URL is tautological — it tests the test,
not the production code. To genuinely cover query-param/filter construction,
extract a helper (here `buildOrganizationMembersQuery(afterID, email)`) that the
`Read` loop calls, and assert on the helper's output — or drive `Read` through
the framework. Flag hardcoded-URL "query param" tests, even when an acceptance
test's comment claims the filter is "covered deterministically by the httptest
unit tests."
