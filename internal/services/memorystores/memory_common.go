// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores

import (
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// memoryCommonModel holds the BetaManagedAgentsMemory fields mapped
// identically by the anthropic_memory resource and both anthropic_memory /
// anthropic_memories data sources, regardless of which object reads the API
// response. It deliberately excludes memory_store_id: the resource's model
// carries it as a plan-supplied, RequiresReplace attribute rather than
// something re-derived from the response, and the data sources either take
// it as a required input (anthropic_memory, left untouched by Read) or don't
// expose it per-item at all (the "memories" list in anthropic_memories,
// which is already scoped to a single store).
type memoryCommonModel struct {
	ID               types.String
	Path             types.String
	Content          types.String
	ContentSha256    types.String
	ContentSizeBytes types.Int64
	MemoryVersionID  types.String
	Type             types.String
	CreatedAt        types.String
	UpdatedAt        types.String
}

// mapMemoryCommon maps the fields of a BetaManagedAgentsMemory shared by the
// resource and data source mappers. Content is null when the API omits it
// (a view=basic response, detected via JSON.Content's unmarshal-set validity
// flag). The resource's own mapper deliberately does not use this field: see
// the comment on mapMemoryToState in memory_resource.go.
func mapMemoryCommon(memory *anthropic.BetaManagedAgentsMemory) memoryCommonModel {
	m := memoryCommonModel{
		ID:               types.StringValue(memory.ID),
		Path:             types.StringValue(memory.Path),
		ContentSha256:    types.StringValue(memory.ContentSha256),
		ContentSizeBytes: types.Int64Value(memory.ContentSizeBytes),
		MemoryVersionID:  types.StringValue(memory.MemoryVersionID),
		Type:             types.StringValue(string(memory.Type)),
		CreatedAt:        tfvalue.TimeOrNull(memory.CreatedAt),
		UpdatedAt:        tfvalue.TimeOrNull(memory.UpdatedAt),
	}

	if memory.JSON.Content.Valid() {
		m.Content = types.StringValue(memory.Content)
	} else {
		m.Content = types.StringNull()
	}

	return m
}
