// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

const workspaceFullFixture = `{
	"id": "wrkspc_01ABC",
	"type": "workspace",
	"name": "test-workspace",
	"created_at": "2026-01-01T00:00:00Z",
	"archived_at": null,
	"display_color": "#FF5733",
	"data_residency": {"allowed_inference_geos": "unrestricted", "default_inference_geo": "global", "workspace_geo": "us"},
	"external_key_id": "ekey_01ABC",
	"compartment_id": "847b8722-1419-4c0c-9f27-0d80149c495c",
	"user_profile_id": "uprof_01ABC",
	"inference_data_retention": {"type": "disabled"},
	"tags": {"env": "prod", "team": "platform"}
}`

func mapFixture(t *testing.T, raw string) WorkspaceResourceModel {
	t.Helper()
	var ws workspaceAPIResponse
	if err := json.Unmarshal([]byte(raw), &ws); err != nil {
		t.Fatalf("parse: %v", err)
	}
	var data WorkspaceResourceModel
	if diags := mapWorkspaceToState(context.Background(), &ws, &data); diags.HasError() {
		t.Fatalf("mapWorkspaceToState: %v", diags)
	}
	// The mapped model must fit the resource schema.
	state := schematest.NullState(t, &WorkspaceResource{})
	if diags := state.Set(context.Background(), &data); diags.HasError() {
		t.Fatalf("state.Set against resource schema: %v", diags)
	}
	return data
}

func TestMapWorkspaceToState_newAttributesPresent(t *testing.T) {
	data := mapFixture(t, workspaceFullFixture)

	if got := data.ExternalKeyID.ValueString(); got != "ekey_01ABC" {
		t.Errorf("external_key_id = %q", got)
	}
	if got := data.CompartmentID.ValueString(); got != "847b8722-1419-4c0c-9f27-0d80149c495c" {
		t.Errorf("compartment_id = %q", got)
	}
	if got := data.UserProfileID.ValueString(); got != "uprof_01ABC" {
		t.Errorf("user_profile_id = %q", got)
	}
	if got := data.InferenceDataRetention.Attributes()["type"]; !got.Equal(types.StringValue("disabled")) {
		t.Errorf("inference_data_retention.type = %v", got)
	}
	want := map[string]attr.Value{"env": types.StringValue("prod"), "team": types.StringValue("platform")}
	if got := data.Tags.Elements(); len(got) != 2 || !got["env"].Equal(want["env"]) || !got["team"].Equal(want["team"]) {
		t.Errorf("tags = %v", got)
	}
}

func TestMapWorkspaceToState_newAttributesNull(t *testing.T) {
	data := mapFixture(t, workspaceFixture())

	if !data.ExternalKeyID.IsNull() || !data.CompartmentID.IsNull() || !data.UserProfileID.IsNull() {
		t.Errorf("absent strings must be null: %v %v %v", data.ExternalKeyID, data.CompartmentID, data.UserProfileID)
	}
	if !data.InferenceDataRetention.IsNull() {
		t.Errorf("absent inference_data_retention must be a null object, got %v", data.InferenceDataRetention)
	}
	if !data.Tags.IsNull() {
		t.Errorf("absent tags must be a null map, got %v", data.Tags)
	}
}

func TestMapWorkspaceToState_explicitNullsAndEmptyTags(t *testing.T) {
	data := mapFixture(t, `{
		"id": "wrkspc_01ABC", "type": "workspace", "name": "n", "created_at": "2026-01-01T00:00:00Z",
		"archived_at": null, "display_color": "#FF5733",
		"data_residency": {"allowed_inference_geos": "unrestricted", "default_inference_geo": "global", "workspace_geo": "us"},
		"external_key_id": null, "compartment_id": "c1", "user_profile_id": null,
		"inference_data_retention": null, "tags": {}
	}`)

	if !data.ExternalKeyID.IsNull() || !data.UserProfileID.IsNull() || !data.InferenceDataRetention.IsNull() {
		t.Error("JSON nulls must map to null")
	}
	if data.Tags.IsNull() || len(data.Tags.Elements()) != 0 {
		t.Errorf("empty API tags must map to an empty, non-null map, got %v", data.Tags)
	}
}

func tagsMap(t *testing.T, kv map[string]string) types.Map {
	t.Helper()
	if kv == nil {
		return types.MapNull(types.StringType)
	}
	elems := map[string]attr.Value{}
	for k, v := range kv {
		elems[k] = types.StringValue(v)
	}
	m, diags := types.MapValue(types.StringType, elems)
	if diags.HasError() {
		t.Fatal(diags)
	}
	return m
}

func TestWorkspaceCreateRequest_tagsAndExternalKey(t *testing.T) {
	b, err := json.Marshal(workspaceCreateRequest{
		Name:          "ws",
		Tags:          map[string]string{"env": "prod"},
		ExternalKeyID: "ekey_01ABC",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	if got["external_key_id"] != "ekey_01ABC" {
		t.Errorf("external_key_id = %v", got["external_key_id"])
	}
	if tags, _ := got["tags"].(map[string]any); tags["env"] != "prod" {
		t.Errorf("tags = %v", got["tags"])
	}

	b, _ = json.Marshal(workspaceCreateRequest{Name: "ws"})
	got = nil
	_ = json.Unmarshal(b, &got)
	if _, ok := got["tags"]; ok {
		t.Error("tags must be omitted when unset")
	}
	if _, ok := got["external_key_id"]; ok {
		t.Error("external_key_id must be omitted when unset")
	}
}

func TestBuildWorkspaceTagsPatch(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name        string
		plan, state map[string]string
		want        map[string]any
	}{
		{"unchanged", map[string]string{"a": "1"}, map[string]string{"a": "1"}, nil},
		{"plan null", nil, map[string]string{"a": "1"}, nil},
		{"added", map[string]string{"a": "1", "b": "2"}, map[string]string{"a": "1"}, map[string]any{"a": "1", "b": "2"}},
		{"changed", map[string]string{"a": "2"}, map[string]string{"a": "1"}, map[string]any{"a": "2"}},
		{"removed", map[string]string{"a": "1"}, map[string]string{"a": "1", "b": "2"}, map[string]any{"a": "1", "b": nil}},
		{"all removed", map[string]string{}, map[string]string{"a": "1"}, map[string]any{"a": nil}},
		{"empty over null state", map[string]string{}, nil, nil},
		{"from null state", map[string]string{"a": "1"}, nil, map[string]any{"a": "1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, diags := buildWorkspaceTagsPatch(ctx, tagsMap(t, tc.plan), tagsMap(t, tc.state))
			if diags.HasError() {
				t.Fatal(diags)
			}
			gj, _ := json.Marshal(got)
			wj, _ := json.Marshal(tc.want)
			if string(gj) != string(wj) {
				t.Errorf("patch = %s, want %s", gj, wj)
			}
		})
	}
}

func TestWorkspaceUpdateRequest_tagsPatchBody(t *testing.T) {
	patch, _ := buildWorkspaceTagsPatch(context.Background(),
		tagsMap(t, map[string]string{"a": "1"}), tagsMap(t, map[string]string{"a": "1", "b": "2"}))
	b, _ := json.Marshal(workspaceUpdateRequest{Name: "ws", Tags: patch})
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	tags, _ := got["tags"].(map[string]any)
	if v, ok := tags["b"]; !ok || v != nil {
		t.Errorf("removed key must be sent as null, got %v", got["tags"])
	}

	b, _ = json.Marshal(workspaceUpdateRequest{Name: "ws"})
	got = nil
	_ = json.Unmarshal(b, &got)
	if _, ok := got["tags"]; ok {
		t.Error("tags key must be absent without a patch")
	}
}

func TestTagKeyValidator(t *testing.T) {
	v := mapvalidator.KeysAre(noAnthropicPrefixValidator{})
	for key, wantErr := range map[string]bool{
		"env": false, "my-anthropic": false,
		"anthropic": true, "anthropic_internal": true, "Anthropic-Team": true, "ANTHROPIC": true,
	} {
		resp := &validator.MapResponse{}
		v.ValidateMap(context.Background(), validator.MapRequest{
			ConfigValue: tagsMap(t, map[string]string{key: "x"}),
		}, resp)
		if resp.Diagnostics.HasError() != wantErr {
			t.Errorf("key %q: error = %v, want %v", key, resp.Diagnostics.HasError(), wantErr)
		}
	}
}
