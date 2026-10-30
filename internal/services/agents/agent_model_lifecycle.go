// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Plan-time check of the lifecycle of the models an agent names. The Models
// API's "get" (GET /v1/models/{model_id},
// https://platform.claude.com/docs/en/api/models/retrieve) takes a "Model
// identifier or alias" and returns its lifecycle (active, deprecated,
// retired), deprecated_at and retires_at; "retired: The model is no longer
// available for use; inference requests naming it fail", and "Requests to
// retired models fail" (https://platform.claude.com/docs/en/about-claude/model-deprecations).

// modelLookupTimeout bounds the lookup of one model, retries included (the
// client's policy replays a GET on 429, 5xx and connection errors). The check
// only advises, so a slow or unreachable API delays a plan by this much per
// model at most, and the lookup is then skipped.
const modelLookupTimeout = 5 * time.Second

// modelDeprecationsURL lists the deprecated and retired models with their
// recommended replacements.
const modelDeprecationsURL = "https://platform.claude.com/docs/en/about-claude/model-deprecations"

// Slots name what a model is for. A slot of the prior state holds the model
// the agent uses there now: the agent's own model, the multiagent_20261001
// advisor, or the advisor entry of a coordinator roster. A roster has at most
// one advisor entry, so its slot is not tied to a list index, which shifts
// when entries move.
const (
	modelSlotAgent         = "model"
	modelSlotAdvisor       = "multiagent.advisor.model"
	modelSlotRosterAdvisor = "multiagent.agents.advisor"
)

// modelRef is one attribute that names a model.
type modelRef struct {
	path  path.Path
	slot  string
	value types.String
}

// subject is what stops working when the model of r retires.
func (r modelRef) subject() string {
	if r.slot == modelSlotAgent {
		return "this agent"
	}
	return "this agent's advisor"
}

// modelLookup returns the Models API entry of a model ID or alias.
type modelLookup func(ctx context.Context, id string) (*anthropic.ModelInfo, error)

// lookupModels returns the lookup of the resource's client, cached per model
// for the provider configuration (providerdata.ModelInfoCache), each request
// bounded by timeout.
func (r *AgentResource) lookupModels(timeout time.Duration) modelLookup {
	fetch := func(ctx context.Context, id string) (*anthropic.ModelInfo, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return r.client.Models.Get(ctx, id, anthropic.ModelGetParams{})
	}
	return func(ctx context.Context, id string) (*anthropic.ModelInfo, error) {
		return r.models.Get(ctx, id, fetch)
	}
}

// attributeGetter is what tfsdk.Plan and tfsdk.State share.
type attributeGetter interface {
	GetAttribute(ctx context.Context, p path.Path, target any) diag.Diagnostics
}

// agentModelRefs returns the attributes of an agent plan or state that name a
// model: model, multiagent.advisor.model and the model of every
// multiagent.agents entry. Values are as found, null or unknown included.
func agentModelRefs(ctx context.Context, from attributeGetter) []modelRef {
	advisor := path.Root("multiagent").AtName("advisor").AtName("model")
	refs := []modelRef{
		{path: path.Root("model"), slot: modelSlotAgent, value: stringAt(ctx, from, path.Root("model"))},
		{path: advisor, slot: modelSlotAdvisor, value: stringAt(ctx, from, advisor)},
	}
	roster := path.Root("multiagent").AtName("agents")
	var entries types.List
	if d := from.GetAttribute(ctx, roster, &entries); d.HasError() {
		return refs
	}
	for i := range len(entries.Elements()) {
		p := roster.AtListIndex(i).AtName("model")
		refs = append(refs, modelRef{path: p, slot: modelSlotRosterAdvisor, value: stringAt(ctx, from, p)})
	}
	return refs
}

// stringAt returns the string at p, or null when it cannot be read. Under a
// null or unknown parent the framework returns null.
func stringAt(ctx context.Context, from attributeGetter, p path.Path) types.String {
	var v types.String
	if d := from.GetAttribute(ctx, p, &v); d.HasError() {
		tflog.Debug(ctx, "Model lifecycle check: attribute not readable", map[string]any{"path": p.String()})
		return types.StringNull()
	}
	return v
}

// priorModels returns the model each slot of the prior state holds, nil on
// create.
func priorModels(ctx context.Context, state tfsdk.State) map[string]string {
	if state.Raw.IsNull() {
		return nil
	}
	prior := map[string]string{}
	for _, ref := range agentModelRefs(ctx, state) {
		if !ref.value.IsNull() && !ref.value.IsUnknown() {
			prior[ref.slot] = ref.value.ValueString()
		}
	}
	return prior
}

// checkModelLifecycle looks up every model refs name and reports its
// lifecycle:
//   - deprecated: a warning, since requests naming it fail once it retires;
//   - retired and set by this plan (a create, or a change to that model): an
//     error, since the apply would fail anyway;
//   - retired and kept from the prior state: a warning that the agent can no
//     longer run, so that changes to other attributes still apply;
//   - active, or anything else: nothing.
//
// A null or unknown value is skipped, and a failed lookup (404, 401, 403,
// network, timeout) is only logged: the check fails a plan in the
// retired-and-set case only.
func checkModelLifecycle(ctx context.Context, lookup modelLookup, refs []modelRef, prior map[string]string) diag.Diagnostics {
	var diags diag.Diagnostics
	for _, ref := range refs {
		if ref.value.IsNull() || ref.value.IsUnknown() {
			continue
		}
		id := ref.value.ValueString()
		info, err := lookup(ctx, id)
		if err != nil {
			tflog.Debug(ctx, "Model lifecycle check skipped: the Models API lookup failed", map[string]any{"model": id, "error": err.Error()})
			continue
		}
		if d := modelLifecycleDiagnostic(ref, id, info, prior[ref.slot] == id); d != nil {
			diags.Append(d)
		}
	}
	return diags
}

// modelLifecycleDiagnostic is the diagnostic for the model id of ref, whose
// Models API entry is info; kept tells whether the prior state already had it.
func modelLifecycleDiagnostic(ref modelRef, id string, info *anthropic.ModelInfo, kept bool) diag.Diagnostic {
	name, dates := modelName(id, info), modelDates(info)
	switch info.Lifecycle {
	case anthropic.ModelInfoLifecycleDeprecated:
		return diag.NewAttributeWarningDiagnostic(ref.path, "Deprecated Model", fmt.Sprintf(
			"The model %s is deprecated (%s). Requests naming a model fail once it is retired, so %s will stop working then: "+
				"move it to an active model. The anthropic_models data source lists the models with their lifecycle_stage, "+
				"and %s gives the recommended replacements.", name, dates, ref.subject(), modelDeprecationsURL))
	case anthropic.ModelInfoLifecycleRetired:
		if kept {
			return diag.NewAttributeWarningDiagnostic(ref.path, "Retired Model", fmt.Sprintf(
				"The model %s is retired (%s), and requests naming a retired model fail: %s can no longer run. "+
					"Changes to other attributes still apply; move it to an active model (see %s).", name, dates, ref.subject(), modelDeprecationsURL))
		}
		return diag.NewAttributeErrorDiagnostic(ref.path, "Retired Model", fmt.Sprintf(
			"The model %s is retired (%s), and requests naming a retired model fail, so applying this plan would fail. "+
				"Choose an active model (see %s).", name, dates, modelDeprecationsURL))
	case anthropic.ModelInfoLifecycleActive:
		// Nothing to report, as for a stage this provider does not know yet.
	}
	return nil
}

// modelName quotes id, followed by the model it resolves to when it is an
// alias.
func modelName(id string, info *anthropic.ModelInfo) string {
	if info.ID != "" && info.ID != id {
		return fmt.Sprintf("%q (resolved to %q)", id, info.ID)
	}
	return fmt.Sprintf("%q", id)
}

// modelDates lists the lifecycle dates the API returned, by their API names.
func modelDates(info *anthropic.ModelInfo) string {
	var parts []string
	for _, d := range []struct {
		name string
		at   time.Time
	}{{"deprecated_at", info.DeprecatedAt}, {"retires_at", info.RetiresAt}} {
		if !d.at.IsZero() {
			parts = append(parts, d.name+" "+d.at.UTC().Format(time.RFC3339))
		}
	}
	if len(parts) == 0 {
		return "no lifecycle date returned"
	}
	return strings.Join(parts, ", ")
}
