---
name: Workspace Member implementation notes
description: Review of workspace_member resource implementation (vk/4679-workspace-member); validator pattern idiomatic, CRUD correct, import state works
type: project
---

## Review of anthropic_workspace_member resource (vk/4679-workspace-member)

**Date**: 2026-05-05

### Key findings

1. **Custom validator pattern is idiomatic** — The `noWorkspaceBillingValidator` implementing `validator.StringRequest` is appropriate and idiomatic for terraform-plugin-framework. It's used to reject `workspace_billing` role with a custom message (not just a list of valid values).

2. **ImportState logic is correct** — Sets `workspace_id` and `user_id` from the parsed ID, allowing subsequent Read to fetch the resource using those composite keys.

3. **CRUD operations complete and correct** — All five CRUD methods implemented:
   - Create: Calls POST, parses response, sets ID as `workspace_id:user_id`
   - Read: Handles 404s gracefully via `admin.IsNotFound()`
   - Update: Uses POST to update role (workspace_id/user_id immutable)
   - Delete: Calls DELETE endpoint
   - ImportState: Parses composite ID correctly

4. **Test coverage comprehensive** — Unit tests for response parsing and 404 detection; acceptance tests cover basic CRUD, update role, and billing role rejection.

5. **Test file naming correct** — `workspace_member_resource_test.go` uses `package workspaces_test` (external test package), following the convention.

6. **Terraform test coverage** — `.tftest.hcl` validates schema with `terraform plan`; fixture module outputs match assertions.

7. **Resource registered in provider** — Correctly added to `Resources()` in provider.go at line 115.

### No issues found

All checklist items passed:
- Configure method uses `providerrors.RequireAdminResourceClient()` helper
- No silent error swallows
- Full CRUD + ImportState implemented
- Example configs exist under `examples/resources/workspace_member/`
- Template has non-empty `subcategory: "Workspaces"`
- Terraform test assertions are meaningful (schema validation via plan)
