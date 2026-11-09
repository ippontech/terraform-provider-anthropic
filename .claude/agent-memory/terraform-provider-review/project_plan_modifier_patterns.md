---
name: Plan modifier patterns for Computed attributes
description: A bare Computed attribute keeps no-op plans empty; UseStateForUnknown is for stable values only and never for updated_at
type: project
---

Checked on 2026-11-02 against terraform-plugin-framework v1.19.0 and the resources in `internal/services/`. Re-check: `grep -n 'MarkComputedNilsAsUnknown' -B10 $(go env GOMODCACHE)/github.com/hashicorp/terraform-plugin-framework@*/internal/fwserver/server_planresourcechange.go` and `grep -rn '"updated_at": schema' -A4 internal/services/*/*_resource.go`. Attribute-specific plan modifiers documented in a service's `CLAUDE.md` (for example `anthropic_agent`'s `model_effort` in `internal/services/agents/CLAUDE.md`) take precedence over this general pattern.

## A bare `Computed` attribute does not make a no-op plan non-empty

The framework marks a `Computed` attribute whose configuration is null as `(known after apply)` only when the planned state differs from the prior state (`PlanResourceChange`, before calling `MarkComputedNilsAsUnknown`). On a plan with no change, every such attribute keeps its prior state value, so the acceptance tests' empty-plan check passes without any plan modifier.

## `updated_at` and other values that change on every update

Leave them bare `Computed: true`, as every resource in the tree does. On an update plan the framework marks them unknown, which is what the apply returns. Never add `UseStateForUnknown` to them: the plan would commit to the old value, the update returns a new one, and Terraform fails with "Provider produced inconsistent result after apply". The `ModifyPlan` in `internal/services/environments/environment_resource.go` produces the same plan as the framework default; it is not a pattern to require.

## `UseStateForUnknown` for stable server-assigned values

On IDs, `created_at` and other values that an update does not change, `UseStateForUnknown` keeps the prior value in an update plan instead of `(known after apply)`, so the plan shows only the real change and references to the attribute stay known. It is an improvement, not a fix for a non-empty plan.

**How to apply:** Flag `UseStateForUnknown` on an attribute the API changes on update (`updated_at`, `last_modified_at`, a version counter). Do not flag a bare `Computed` timestamp, and do not ask for a `ModifyPlan` to keep it stable.
