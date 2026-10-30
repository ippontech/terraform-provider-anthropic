// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package models_test

import (
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/knownvalue"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the Models API (GA: no beta header, standard API key).
// Reference: https://platform.claude.com/docs/en/api/models/list and
// https://platform.claude.com/docs/en/api/models/retrieve.
const (
	routeListModels = "GET /v1/models"
	routeGetModel   = "GET /v1/models/{model_id}"
)

// modelsFake serves a fixed list of models: the list in after_id pages of
// pageSize, newest first as the reference says, and each model by ID or by
// one of its aliases. An unknown ID is a 404 not_found_error.
type modelsFake struct {
	*sdktest.Server
	models  []map[string]any
	aliases map[string]string
}

func newModelsFake(t *testing.T, pageSize int, models ...map[string]any) *modelsFake {
	t.Helper()
	f := &modelsFake{Server: sdktest.NewServer(t), models: models, aliases: map[string]string{}}
	f.Handle(routeListModels, func(w http.ResponseWriter, r *http.Request) {
		f.Paginate(w, r, sdktest.AfterID, pageSize, f.models)
	})
	f.Handle(routeGetModel, func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("model_id")
		if target, ok := f.aliases[id]; ok {
			id = target
		}
		for _, m := range f.models {
			if m["id"] == id {
				sdktest.WriteJSON(w, http.StatusOK, m)
				return
			}
		}
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "model: "+r.PathValue("model_id"))
	})
	return f
}

// modelJSON returns a model as the API sends it (SDK v1.67.0 ModelInfo).
// Bit i of bits sets the i-th capability flag of capabilitiesCheck, so two
// models with different bits tell every flag apart. The "supported" of the
// parent objects and the fields the provider does not map (xhigh,
// thinking.types) carry the opposite of a neighbouring flag, so a mapping
// that reads the wrong level shows.
func modelJSON(id, displayName, createdAt string, maxInput, maxTokens int64, bits uint) map[string]any {
	on := func(i int) bool { return bits&(1<<i) != 0 }
	flag := func(i int) map[string]any { return map[string]any{"supported": on(i)} }
	opposite := func(i int) map[string]any { return map[string]any{"supported": !on(i)} }
	return map[string]any{
		"type":             "model",
		"id":               id,
		"display_name":     displayName,
		"created_at":       createdAt,
		"max_input_tokens": maxInput,
		"max_tokens":       maxTokens,
		"capabilities": map[string]any{
			"batch":              flag(0),
			"citations":          flag(1),
			"code_execution":     flag(2),
			"image_input":        flag(3),
			"pdf_input":          flag(4),
			"structured_outputs": flag(5),
			"thinking": map[string]any{
				"supported": on(6),
				"types":     map[string]any{"adaptive": opposite(6), "enabled": opposite(6)},
			},
			"context_management": map[string]any{
				"supported":                !on(7),
				"clear_thinking_20251015":  flag(7),
				"clear_tool_uses_20250919": flag(8),
				"compact_20260112":         flag(9),
			},
			"effort": map[string]any{
				"supported": !on(10),
				"high":      flag(10),
				"low":       flag(11),
				"max":       flag(12),
				"medium":    flag(13),
				"xhigh":     opposite(13),
			},
		},
	}
}

// capabilitiesCheck is the exact state value the provider must write for a
// model built by modelJSON with bits.
func capabilitiesCheck(bits uint) knownvalue.Check {
	b := func(i int) knownvalue.Check { return knownvalue.Bool(bits&(1<<i) != 0) }
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"batch":              b(0),
		"citations":          b(1),
		"code_execution":     b(2),
		"image_input":        b(3),
		"pdf_input":          b(4),
		"structured_outputs": b(5),
		"thinking":           b(6),
		"context_management": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"clear_thinking_20251015":  b(7),
			"clear_tool_uses_20250919": b(8),
			"compact_20260112":         b(9),
		}),
		"effort": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"high":   b(10),
			"low":    b(11),
			"max":    b(12),
			"medium": b(13),
		}),
	})
}

// Two complementary bit patterns: every flag is true in one and false in the
// other, and neighbours differ, so a swapped or constant mapping fails.
const (
	bitsA uint = 0b01_0101_0101_0101
	bitsB uint = 0b10_1010_1010_1010
)
