// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package claudetag

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// The five settings every level stores, and their constraints (guide,
// Settings): instructions at most 8,192 characters after trimming, and blank
// text clears the setting; model at most 128 characters; environment_id
// prefixed env_ (or the ccpool_ runner pool an owner set in claude.ai);
// guest_policy one of three types.
const (
	maxInstructionsLength = 8192
	maxModelLength        = 128
	runnerPoolPrefix      = "ccpool_"
)

var guestPolicies = []string{"allow", "restrict", "channel_only"}

var environmentIDPattern = regexp.MustCompile(`^(env|ccpool)_[A-Za-z0-9]+$`)

// settingsModel holds the five settings; the resource and data source models
// embed it, so they sit at the top level of the schema.
type settingsModel struct {
	Enabled       types.Bool   `tfsdk:"enabled"`
	Instructions  types.String `tfsdk:"instructions"`
	Model         types.String `tfsdk:"model"`
	EnvironmentID types.String `tfsdk:"environment_id"`
	GuestPolicy   types.String `tfsdk:"guest_policy"`
}

// nullSettings is a level that stores nothing.
func nullSettings() settingsModel {
	return settingsModel{
		Enabled:       types.BoolNull(),
		Instructions:  types.StringNull(),
		Model:         types.StringNull(),
		EnvironmentID: types.StringNull(),
		GuestPolicy:   types.StringNull(),
	}
}

// settingsDescriptions describe each setting for a level ("channel" or
// "workspace").
func settingsDescriptions(level string) map[string]string {
	return map[string]string{
		"enabled": "Whether Claude is on in the " + level + " (claude.ai: **Enable Claude Tag in Slack**).",
		"instructions": "Custom instructions Claude reads in every session in the " + level + ". At most 8,192 characters " +
			"after leading and trailing white space is trimmed; blank text is refused here (omit the attribute to clear the setting). " +
			"Instructions are additive: the text of every level that sets them is joined, broadest first.",
		"model": "The model new sessions in the " + level + " start on: a model ID the organization can use with Claude Tag, " +
			"at most 128 characters. Checked when written, not again.",
		"environment_id": "The cloud environment sessions in the " + level + " run in: an active environment (`env_...`) of the " +
			"Claude Enterprise organization. Checked when written, not again. A self-hosted runner pool (`ccpool_...`) set in " +
			"claude.ai is kept, but cannot be set, changed or cleared through the API. Whether an environment created with " +
			"`anthropic_environment` in a Claude Console workspace is accepted here is not verified.",
		"guest_policy": "What Claude does in a channel while a Slack guest is in it: `allow` (claude.ai: **Full access**), " +
			"`restrict` (**Restrict**) or `channel_only` (**Channel only**; refused when not available to the organization).",
	}
}

// settingsResourceAttributes are the five settings as Optional attributes:
// what the level stores, null when it inherits the setting. They are not
// Computed, so a setting dropped from the configuration is cleared.
func settingsResourceAttributes(level string) map[string]schema.Attribute {
	desc := settingsDescriptions(level)
	omitted := " Omitted, the setting is cleared on the " + level + ", which then inherits it."
	return map[string]schema.Attribute{
		"enabled": schema.BoolAttribute{Optional: true, MarkdownDescription: desc["enabled"] + omitted},
		"instructions": schema.StringAttribute{
			Optional: true, MarkdownDescription: desc["instructions"] + omitted,
			Validators: []validator.String{instructionsValidator{}},
		},
		"model": schema.StringAttribute{
			Optional: true, MarkdownDescription: desc["model"] + omitted,
			Validators: []validator.String{stringvalidator.LengthBetween(1, maxModelLength)},
		},
		"environment_id": schema.StringAttribute{
			Optional: true, MarkdownDescription: desc["environment_id"] + " Declare a runner pool's ID to keep it." + omitted,
			Validators: []validator.String{stringvalidator.RegexMatches(environmentIDPattern,
				"must be an environment ID (env_...) or the runner pool ID (ccpool_...) the level already stores")},
		},
		"guest_policy": schema.StringAttribute{
			Optional: true, MarkdownDescription: desc["guest_policy"] + omitted,
			Validators: []validator.String{stringvalidator.OneOf(guestPolicies...)},
		},
	}
}

// settingsDataSourceAttributes are the five settings as Computed attributes.
func settingsDataSourceAttributes(level string) map[string]dschema.Attribute {
	desc := settingsDescriptions(level)
	read := " What the " + level + " stores (null when it inherits the setting), or with `include_inherited` what applies in it."
	return map[string]dschema.Attribute{
		"enabled":        dschema.BoolAttribute{Computed: true, MarkdownDescription: desc["enabled"] + read},
		"instructions":   dschema.StringAttribute{Computed: true, MarkdownDescription: desc["instructions"] + read},
		"model":          dschema.StringAttribute{Computed: true, MarkdownDescription: desc["model"] + read},
		"environment_id": dschema.StringAttribute{Computed: true, MarkdownDescription: desc["environment_id"] + read},
		"guest_policy":   dschema.StringAttribute{Computed: true, MarkdownDescription: desc["guest_policy"] + read},
	}
}

// instructionsValidator refuses what the API would not store as written:
// blank text, which the API takes as clearing the setting (so the state would
// not match the configuration), and text over 8,192 characters once trimmed,
// which it refuses with a 400.
type instructionsValidator struct{}

func (instructionsValidator) Description(context.Context) string {
	return fmt.Sprintf("must not be blank, and at most %d characters after trimming", maxInstructionsLength)
}

func (v instructionsValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (instructionsValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	trimmed := strings.TrimSpace(req.ConfigValue.ValueString())
	if trimmed == "" {
		resp.Diagnostics.AddAttributeError(req.Path, "Blank instructions",
			"The Claude Tag API takes blank instructions as clearing the setting. Omit the attribute to clear it.")
		return
	}
	if n := utf8.RuneCountInString(trimmed); n > maxInstructionsLength {
		resp.Diagnostics.AddAttributeError(req.Path, "Instructions too long",
			fmt.Sprintf("Instructions hold at most %d characters after leading and trailing white space is trimmed; these hold %d.",
				maxInstructionsLength, n))
	}
}

// settingsFromAPI maps a `settings` object. prior is the value Terraform
// holds (plan or state): when the API stored instructions without the
// leading and trailing white space the configuration has (a heredoc's final
// newline), the configuration's text is kept, as the two are the same
// setting.
func settingsFromAPI(api settingsAPIObject, prior settingsModel) settingsModel {
	s := nullSettings()
	if api.Enabled != nil {
		s.Enabled = types.BoolValue(*api.Enabled)
	}
	if api.Instructions != nil {
		s.Instructions = types.StringValue(*api.Instructions)
		if instructionsEqual(prior.Instructions, s.Instructions) {
			s.Instructions = prior.Instructions
		}
	}
	if api.Model != nil {
		s.Model = types.StringValue(*api.Model)
	}
	if api.EnvironmentID != nil {
		s.EnvironmentID = types.StringValue(*api.EnvironmentID)
	}
	if api.GuestPolicy != nil {
		s.GuestPolicy = tfvalue.StringOrNull(api.GuestPolicy.Type)
	}
	return s
}

// instructionsEqual compares two instructions as the API stores them: after
// trimming.
func instructionsEqual(a, b types.String) bool {
	if a.IsUnknown() || b.IsUnknown() || a.IsNull() != b.IsNull() {
		return false
	}
	return strings.TrimSpace(a.ValueString()) == strings.TrimSpace(b.ValueString())
}

// settingsPatch returns the `settings` object of a change from stored to
// plan: each setting that differs, with its planned value, or null when the
// plan no longer sets it, so the level inherits it again. A setting left out
// is unchanged (guide, Changing settings; Keep configuration in version
// control, step 3). Nothing differs: an empty map.
func settingsPatch(plan, stored settingsModel) map[string]any {
	patch := map[string]any{}
	if !plan.Enabled.Equal(stored.Enabled) {
		patch["enabled"] = nil
		if !plan.Enabled.IsNull() {
			patch["enabled"] = plan.Enabled.ValueBool()
		}
	}
	if !instructionsEqual(plan.Instructions, stored.Instructions) {
		patch["instructions"] = stringOrNil(plan.Instructions)
	}
	if !plan.Model.Equal(stored.Model) {
		patch["model"] = stringOrNil(plan.Model)
	}
	if !plan.EnvironmentID.Equal(stored.EnvironmentID) {
		patch["environment_id"] = stringOrNil(plan.EnvironmentID)
	}
	if !plan.GuestPolicy.Equal(stored.GuestPolicy) {
		patch["guest_policy"] = nil
		if !plan.GuestPolicy.IsNull() {
			patch["guest_policy"] = map[string]any{"type": plan.GuestPolicy.ValueString()}
		}
	}
	return patch
}

// stringOrNil is the JSON value of a planned string: null when unset.
func stringOrNil(v types.String) any {
	if v.IsNull() {
		return nil
	}
	return v.ValueString()
}

// clearAllPatch returns the change that clears every setting stored, so the
// level inherits all of them again, except a runner pool, which the API
// cannot clear: it is left in place and reported in kept.
func clearAllPatch(stored settingsModel) (map[string]any, string) {
	patch := settingsPatch(nullSettings(), stored)
	var kept string
	if env := stored.EnvironmentID.ValueString(); strings.HasPrefix(env, runnerPoolPrefix) {
		delete(patch, "environment_id")
		kept = env
	}
	return patch, kept
}

// runnerPoolConflict explains why a change cannot be sent, "" when it can.
// "This API cannot set a runner pool, and returns 400 for a request that
// changes or clears environment_id on a settings level that stores one"
// (guide, Settings): the API's 400 would not say which, so the provider
// checks before sending.
func runnerPoolConflict(patch map[string]any, stored, plan settingsModel, level string) string {
	if _, touched := patch["environment_id"]; !touched {
		return ""
	}
	if current := stored.EnvironmentID.ValueString(); strings.HasPrefix(current, runnerPoolPrefix) {
		return fmt.Sprintf("The %s stores the self-hosted runner pool %s as environment_id, which an organization owner "+
			"set in claude.ai. The Claude Tag API cannot change or clear a runner pool. Declare environment_id = %q to keep "+
			"it, or have an owner change it in claude.ai first.", level, current, current)
	}
	if planned := plan.EnvironmentID.ValueString(); strings.HasPrefix(planned, runnerPoolPrefix) {
		return fmt.Sprintf("environment_id %s is a self-hosted runner pool, which the Claude Tag API cannot set: an "+
			"organization owner chooses a runner pool in claude.ai. Use an environment ID (env_...).", planned)
	}
	return ""
}
