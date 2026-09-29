// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package userprofiles

import (
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// userProfileTrustGrantAttrTypes is the object type of each element of a
// user profile's trust_grants map, shared by the resource and both data
// sources.
var userProfileTrustGrantAttrTypes = map[string]attr.Type{
	"status": types.StringType,
}

// userProfileTrustGrantObjectType is the types.ObjectType built from
// userProfileTrustGrantAttrTypes, ready to use as a map's ElemType.
var userProfileTrustGrantObjectType = types.ObjectType{AttrTypes: userProfileTrustGrantAttrTypes}

// userProfileNullableString maps an SDK field that round-trips through the
// API as an empty string when unset (external_id, name) to a null
// types.String, so state distinguishes "unset" from an actual empty value.
func userProfileNullableString(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// userProfileTimestamps formats the API's created_at/updated_at fields as
// RFC 3339 strings.
func userProfileTimestamps(profile *anthropic.BetaUserProfile) (createdAt, updatedAt types.String) {
	return types.StringValue(profile.CreatedAt.Format(time.RFC3339)), types.StringValue(profile.UpdatedAt.Format(time.RFC3339))
}

// userProfileMetadataToMap converts the API's metadata into a types.Map,
// mapping the empty case to null since the API response can't distinguish
// an empty map from metadata being absent (both round-trip as a zero-length
// Go map).
func userProfileMetadataToMap(metadata map[string]string) (types.Map, diag.Diagnostics) {
	var diags diag.Diagnostics

	if len(metadata) == 0 {
		return types.MapNull(types.StringType), diags
	}

	elements := make(map[string]attr.Value, len(metadata))
	for k, v := range metadata {
		elements[k] = types.StringValue(v)
	}
	m, d := types.MapValue(types.StringType, elements)
	diags.Append(d...)
	return m, diags
}

// userProfileTrustGrantsToMap converts the API's trust grants into a
// types.Map when there is at least one, leaving how to represent the empty
// case (null for the data sources, an empty map for the resource) to the
// caller: ok is false when grants is empty, and the returned map is the
// zero value in that case.
func userProfileTrustGrantsToMap(grants map[string]anthropic.BetaUserProfileTrustGrant) (m types.Map, ok bool, diags diag.Diagnostics) {
	if len(grants) == 0 {
		return types.Map{}, false, diags
	}

	elements := make(map[string]attr.Value, len(grants))
	for k, v := range grants {
		obj, d := types.ObjectValue(userProfileTrustGrantAttrTypes, map[string]attr.Value{
			"status": types.StringValue(string(v.Status)),
		})
		diags.Append(d...)
		elements[k] = obj
	}
	m, d := types.MapValue(userProfileTrustGrantObjectType, elements)
	diags.Append(d...)
	return m, true, diags
}
