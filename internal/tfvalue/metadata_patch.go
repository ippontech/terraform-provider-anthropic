// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tfvalue

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// MetadataPatch builds the metadata body of an update for endpoints that
// apply metadata as a patch: an omitted key is kept, a key set to null is
// deleted. Sending the planned map alone can therefore never remove a key, so
// the patch upserts every planned key and sets every key present in the prior
// state but absent from the plan to nil. A null plan deletes every prior key.
//
// The result is a map[string]any (string values upsert, nil values delete),
// to send with params.SetExtraFields(map[string]any{"metadata": patch})
// because the SDK's typed map[string]string cannot carry a null. An empty
// result means nothing changes: omit metadata from the request.
func MetadataPatch(ctx context.Context, plan, state types.Map) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics
	patch := map[string]any{}

	if !plan.IsNull() && !plan.IsUnknown() {
		var planned map[string]string
		diags.Append(plan.ElementsAs(ctx, &planned, false)...)
		if diags.HasError() {
			return nil, diags
		}
		for k, v := range planned {
			patch[k] = v
		}
	}

	if !state.IsNull() && !state.IsUnknown() {
		var prior map[string]string
		diags.Append(state.ElementsAs(ctx, &prior, false)...)
		if diags.HasError() {
			return nil, diags
		}
		for k := range prior {
			if _, kept := patch[k]; !kept {
				patch[k] = nil
			}
		}
	}

	return patch, diags
}

// MetadataAfterEmptyResponse is the state value of a metadata map when the
// API returns none. The API returns {} both for an object without metadata
// and after `metadata = {}` cleared it, so a known empty map stays as
// configured; anything else (null, unknown, or with keys) becomes null.
func MetadataAfterEmptyResponse(current types.Map) types.Map {
	if !current.IsNull() && !current.IsUnknown() && len(current.Elements()) == 0 {
		return current
	}
	return types.MapNull(types.StringType)
}
