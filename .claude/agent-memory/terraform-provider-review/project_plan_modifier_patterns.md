---
name: Plan modifier patterns for Computed attributes
description: UseStateForUnknown and ModifyPlan patterns to avoid non-empty plans and inconsistent-result errors on Computed/Optional+Computed attributes
type: project
---

## Required plan modifiers on Computed and Optional+Computed attributes

Without plan modifiers, `Computed` and `Optional+Computed` attributes are marked
`(known after apply)` on every plan — even when nothing changed — causing the
"non-empty plan after apply" test failure.

### UseStateForUnknown — for attributes that don't change unless the resource is updated

Add to **all** `Computed` and `Optional+Computed` attributes that hold a stable
server-assigned value (IDs, creation timestamps, optional fields the API may
populate but the user didn't configure):

```go
PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
// also: mapplanmodifier, objectplanmodifier, boolplanmodifier, etc.
```

**Why:** Without it, every `terraform plan` after apply shows these as
`(known after apply)`, producing a non-empty plan and failing the idempotency
check in acceptance tests.

### ModifyPlan — for timestamps that change on every update

`UseStateForUnknown()` alone is **wrong** for attributes like `updated_at` that
the API silently mutates whenever the resource is updated. The plan would commit
to the old value, but apply returns a new one → "Provider produced inconsistent
result after apply" error.

Implement `resource.ResourceWithModifyPlan` instead:

```go
var _ resource.ResourceWithModifyPlan = &MyResource{}

func (r *MyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
    if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
        return // create or destroy — nothing to do
    }
    var state, plan MyResourceModel
    resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
    resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
    if resp.Diagnostics.HasError() {
        return
    }
    if !plan.Name.Equal(state.Name) || /* other mutable attrs */ {
        plan.UpdatedAt = types.StringUnknown() // will change — show as unknown
    } else {
        plan.UpdatedAt = state.UpdatedAt // no-op plan — keep stable
    }
    resp.Diagnostics.Append(resp.Plan.Set(ctx, plan)...)
}
```

**How to apply:** Flag any resource PR where a `Computed: true` timestamp
attribute (e.g. `updated_at`, `last_modified_at`) lacks both `UseStateForUnknown`
and a `ModifyPlan` implementation. Check that pure `UseStateForUnknown` on such
attributes is never used alone without `ModifyPlan`.
