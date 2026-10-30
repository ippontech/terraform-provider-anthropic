// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package messages

import (
	"context"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestMessageConfigValidator_diagnostics pins each diagnostic of the thinking
// rules, with its path, and that a null or unknown block, an unknown type
// and unknown leaves raise none.
func TestMessageConfigValidator_diagnostics(t *testing.T) {
	t.Parallel()
	unknown := tftypes.UnknownValue
	var sresp resource.SchemaResponse
	NewMessageResource().Schema(context.Background(), resource.SchemaRequest{}, &sresp)
	thinkingType := sresp.Schema.Type().TerraformType(context.Background()).(tftypes.Object).AttributeTypes["thinking"]

	const (
		missingBudget    = "Error: Missing budget_tokens @ thinking.budget_tokens"
		unexpectedBudget = "Error: Unexpected budget_tokens @ thinking.budget_tokens"
		unexpectedDisp   = "Error: Unexpected display @ thinking.display"
	)
	for name, tc := range map[string]struct {
		thinking map[string]tftypes.Value
		want     []string
	}{
		"thinking null":            {thinking: map[string]tftypes.Value{"thinking": tftypes.NewValue(thinkingType, nil)}},
		"thinking unknown":         {thinking: map[string]tftypes.Value{"thinking": tftypes.NewValue(thinkingType, unknown)}},
		"type unknown":             {thinking: thinkingValue(t, unknown, 1024, "omitted")},
		"type null":                {thinking: thinkingValue(t, nil, 1024, "omitted")},
		"enabled":                  {thinking: thinkingValue(t, "enabled", 1024, "summarized")},
		"enabled no budget":        {thinking: thinkingValue(t, "enabled", nil, "summarized"), want: []string{missingBudget}},
		"enabled budget unknown":   {thinking: thinkingValue(t, "enabled", unknown, nil)},
		"adaptive":                 {thinking: thinkingValue(t, "adaptive", nil, "omitted")},
		"adaptive with budget":     {thinking: thinkingValue(t, "adaptive", 1024, nil), want: []string{unexpectedBudget}},
		"adaptive budget unknown":  {thinking: thinkingValue(t, "adaptive", unknown, nil)},
		"disabled with both":       {thinking: thinkingValue(t, "disabled", 1024, "omitted"), want: []string{unexpectedBudget, unexpectedDisp}},
		"disabled display unknown": {thinking: thinkingValue(t, "disabled", nil, unknown)},
		"between_tools with both":  {thinking: thinkingValue(t, "between_tools", 2048, "summarized"), want: []string{unexpectedBudget, unexpectedDisp}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := diagStrings(validateConfig(t, tc.thinking))
			if !slices.Equal(got, tc.want) {
				t.Errorf("diagnostics = %q, want %q", got, tc.want)
			}
		})
	}
}
