// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// updateRuleModel is a rule as Update reads it from the plan or the state,
// with the given changes.
func updateRuleModel(t *testing.T, change func(*FederationRuleResourceModel)) FederationRuleResourceModel {
	t.Helper()
	m := FederationRuleResourceModel{
		ID: types.StringValue("fdrl_1"), Name: types.StringValue("ci"), Description: types.StringNull(),
		IssuerID: types.StringValue("fdis_1"), OAuthScope: types.StringValue("workspace:developer"),
		Match:       matchObject(t, types.StringNull(), types.StringNull(), types.StringValue("true"), types.MapNull(types.StringType)),
		Target:      targetObject(t, "svac_001"),
		WorkspaceID: types.StringValue("wrkspc_a"), AppliesToAllWorkspaces: types.BoolValue(false),
		TokenLifetimeSeconds: types.Int64Value(3600), Attributes: types.MapNull(types.StringType),
		IssuerName: types.StringValue("github"), WorkspaceIDs: types.ListNull(types.StringType),
		CreatedAt: types.StringNull(), UpdatedAt: types.StringNull(), ArchivedAt: types.StringNull(),
		CreatedByActorID: types.StringNull(), UpdatedByActorID: types.StringNull(), ArchivedByActorID: types.StringNull(),
	}
	if change != nil {
		change(&m)
	}
	return m
}

// TestFederationRuleUpdate_requestBody pins the update request Update sends
// for the plan and state values Terraform cannot produce through a
// configuration (unknown ones) as well as the usual ones: description
// unknown left out, null sent as null; applies_to_all_workspaces sent when
// known, false when unknown over a true state; token_lifetime_seconds and
// workspace_id left out when unknown; attributes sent only when changed, a
// null or unknown plan as null.
func TestFederationRuleUpdate_requestBody(t *testing.T) {
	t.Parallel()
	const required = `"name":"ci","oauth_scope":"workspace:developer","match":{"condition":"true"},` +
		`"target":{"type":"service_account","service_account_id":"svac_001"}`
	for name, tc := range map[string]struct {
		plan, state func(*FederationRuleResourceModel)
		want        string
	}{
		"known values": {
			plan: func(m *FederationRuleResourceModel) { m.Description = types.StringValue("d") },
			want: `{` + required + `,"description":"d","workspace_id":"wrkspc_a","applies_to_all_workspaces":false,"token_lifetime_seconds":3600}`,
		},
		"null description": {
			want: `{` + required + `,"description":null,"workspace_id":"wrkspc_a","applies_to_all_workspaces":false,"token_lifetime_seconds":3600}`,
		},
		"unknown values": {
			plan: func(m *FederationRuleResourceModel) {
				m.Description, m.WorkspaceID, m.TokenLifetimeSeconds = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
				m.AppliesToAllWorkspaces = types.BoolUnknown()
			},
			want: `{` + required + `}`,
		},
		"unknown applies over a true state": {
			plan: func(m *FederationRuleResourceModel) {
				m.AppliesToAllWorkspaces, m.WorkspaceID = types.BoolUnknown(), types.StringNull()
			},
			state: func(m *FederationRuleResourceModel) { m.AppliesToAllWorkspaces = types.BoolValue(true) },
			want:  `{` + required + `,"description":null,"applies_to_all_workspaces":false,"token_lifetime_seconds":3600}`,
		},
		"attributes added": {
			plan: func(m *FederationRuleResourceModel) {
				m.Attributes = types.MapValueMust(types.StringType, map[string]attr.Value{"env": types.StringValue("claims.env")})
			},
			want: `{` + required + `,"description":null,"workspace_id":"wrkspc_a","applies_to_all_workspaces":false,"token_lifetime_seconds":3600,"attributes":{"env":"claims.env"}}`,
		},
		"attributes removed": {
			state: func(m *FederationRuleResourceModel) {
				m.Attributes = types.MapValueMust(types.StringType, map[string]attr.Value{})
			},
			want: `{` + required + `,"description":null,"workspace_id":"wrkspc_a","applies_to_all_workspaces":false,"token_lifetime_seconds":3600,"attributes":null}`,
		},
		"attributes unknown": {
			plan: func(m *FederationRuleResourceModel) { m.Attributes = types.MapUnknown(types.StringType) },
			want: `{` + required + `,"description":null,"workspace_id":"wrkspc_a","applies_to_all_workspaces":false,"token_lifetime_seconds":3600,"attributes":null}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			srv := sdktest.NewServer(t)
			rule := map[string]any{
				"type": "federation_rule", "id": "fdrl_1", "name": "ci", "issuer_id": "fdis_1", "issuer_name": "github",
				"oauth_scope": "workspace:developer", "match": map[string]any{"condition": "true"},
				"target":     map[string]any{"type": "service_account", "service_account_id": "svac_001", "service_account_name": "sa-one"},
				"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:01Z",
			}
			srv.Respond("POST /v1/organizations/federation_rules/{id}", http.StatusOK, rule)
			srv.Respond("GET /v1/organizations/federation_rules/{id}", http.StatusOK, rule)
			r := &FederationRuleResource{client: oauthtest.NewClient(t, srv.Server)}

			sch := schematest.ResourceSchema(t, r)
			objType := schematest.ResourceObjectType(t, r)
			plan := tfsdk.Plan{Schema: sch, Raw: tftypes.NewValue(objType, nil)}
			state := tfsdk.State{Schema: sch, Raw: tftypes.NewValue(objType, nil)}
			planModel, stateModel := updateRuleModel(t, tc.plan), updateRuleModel(t, tc.state)
			if diags := plan.Set(ctx, &planModel); diags.HasError() {
				t.Fatal(diags)
			}
			if diags := state.Set(ctx, &stateModel); diags.HasError() {
				t.Fatal(diags)
			}
			resp := resource.UpdateResponse{State: tfsdk.State{Schema: sch, Raw: tftypes.NewValue(objType, nil)}}
			r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Update diagnostics: %v", resp.Diagnostics)
			}
			srv.AssertJSONBodies("POST /v1/organizations/federation_rules/{id}", tc.want)
		})
	}
}
