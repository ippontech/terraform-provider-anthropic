// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// workspacesListFixture is the API entry of workspace wrkspc_01.
func workspacesListFixture(name string, archivedAt *string) workspaceAPIResponse {
	return workspaceAPIResponse{
		ID:           "wrkspc_01",
		Name:         name,
		ArchivedAt:   archivedAt,
		CreatedAt:    "2026-01-01T00:00:00Z",
		DisplayColor: "#AABBCC",
		Type:         "workspace",
		DataResidency: workspaceAPIDataResidency{
			AllowedInferenceGeos: json.RawMessage(`"unrestricted"`),
			DefaultInferenceGeo:  "global",
			WorkspaceGeo:         "us",
		},
	}
}

func TestBuildWorkspacesListQuery(t *testing.T) {
	tests := []struct {
		name            string
		afterID         string
		includeArchived types.Bool
		includeDefault  types.Bool
		want            string
	}{
		{"defaults", "", types.BoolNull(), types.BoolNull(), "limit=1000"},
		{"false is not sent", "", types.BoolValue(false), types.BoolValue(false), "limit=1000"},
		{"unknown is not sent", "", types.BoolUnknown(), types.BoolUnknown(), "limit=1000"},
		{"include default", "", types.BoolNull(), types.BoolValue(true), "include_default=true&limit=1000"},
		{"both filters and cursor", "wrkspc_01", types.BoolValue(true), types.BoolValue(true), "after_id=wrkspc_01&include_archived=true&include_default=true&limit=1000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildWorkspacesListQuery(tt.afterID, tt.includeArchived, tt.includeDefault).Encode(); got != tt.want {
				t.Errorf("query = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMapWorkspaceToListObject_archivedAt(t *testing.T) {
	archived := "2026-04-01T00:00:00Z"
	ws := workspacesListFixture("archived", &archived)

	obj, diags := mapWorkspaceToListObject(&ws)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if obj == nil {
		t.Fatal("expected non-nil object")
	}
}

func TestMapWorkspaceToListObject_noArchivedAt(t *testing.T) {
	ws := workspacesListFixture("active", nil)

	obj, diags := mapWorkspaceToListObject(&ws)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if obj == nil {
		t.Fatal("expected non-nil object")
	}
}

func TestMapWorkspaceToListObject_tagsKeyAndRetention(t *testing.T) {
	key, comp, prof := "ekey_01", "comp-1", "uprof_01"
	ws := workspacesListFixture("full", nil)
	ws.Tags = map[string]string{"env": "prod"}
	ws.ExternalKeyID = key
	ws.CompartmentID = comp
	ws.UserProfileID = prof
	ws.InferenceDataRetention = &workspaceAPIInferenceDataRetention{Type: "disabled"}

	obj, diags := mapWorkspaceToListObject(&ws)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	attrs := obj.(types.Object).Attributes()
	if !attrs["external_key_id"].Equal(types.StringValue("ekey_01")) ||
		!attrs["compartment_id"].Equal(types.StringValue("comp-1")) ||
		!attrs["user_profile_id"].Equal(types.StringValue("uprof_01")) {
		t.Errorf("string attrs wrong: %v", attrs)
	}
	if got := attrs["inference_data_retention"].(types.Object).Attributes()["type"]; !got.Equal(types.StringValue("disabled")) {
		t.Errorf("inference_data_retention.type = %v", got)
	}
	if got := attrs["tags"].(types.Map).Elements()["env"]; !got.Equal(types.StringValue("prod")) {
		t.Errorf("tags.env = %v", got)
	}
}

func TestMapWorkspaceToListObject_tagsKeyAndRetentionAbsent(t *testing.T) {
	ws := workspacesListFixture("bare", nil)

	obj, diags := mapWorkspaceToListObject(&ws)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	attrs := obj.(types.Object).Attributes()
	for _, k := range []string{"external_key_id", "compartment_id", "user_profile_id", "inference_data_retention", "tags"} {
		if !attrs[k].IsNull() {
			t.Errorf("%s should be null when absent, got %v", k, attrs[k])
		}
	}
}
