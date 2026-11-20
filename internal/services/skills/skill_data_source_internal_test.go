// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// readDataSource runs d.Read with a config holding vals (every other attribute
// null) and fails the test on an error diagnostic.
func readDataSource(t *testing.T, d datasource.DataSource, vals map[string]tftypes.Value) tfsdk.State {
	t.Helper()
	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", schemaResp.Diagnostics)
	}
	typ := schemaResp.Schema.Type().TerraformType(context.Background()).(tftypes.Object)
	req := datasource.ReadRequest{Config: tfsdk.Config{Raw: tfObjectValue(typ, vals), Schema: schemaResp.Schema}}
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	d.Read(context.Background(), req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	return resp.State
}

func TestSkillDataSourceRead_betaShape(t *testing.T) {
	t.Parallel()
	srv := newBetaSkillsServer(t, map[string]http.HandlerFunc{
		"GET /v1/skills/" + fixtureSkillID: jsonResponse(betaSkillJSON),
	})
	state := readDataSource(t, &SkillDataSource{client: srv.client(t)}, map[string]tftypes.Value{
		"skill_id": tfString(fixtureSkillID),
	})
	assertStringAttrs(t, state, map[string]*string{
		"id":             ptr(fixtureSkillID),
		"display_title":  ptr("Test Skill"),
		"created_at":     ptr(fixtureSkillCreated),
		"updated_at":     ptr(fixtureSkillUpdated),
		"latest_version": ptr(fixtureSkillVersion),
		"source":         ptr("custom"),
		"type":           ptr("skill"),
	})
}

// An empty display_title reads as null, as with SDK v1.67.0.
func TestSkillDataSourceRead_emptyDisplayTitle(t *testing.T) {
	t.Parallel()
	srv := newBetaSkillsServer(t, map[string]http.HandlerFunc{
		"GET /v1/skills/skill_x": jsonResponse(`{"id":"skill_x","display_title":"","source":"anthropic","type":"skill"}`),
	})
	state := readDataSource(t, &SkillDataSource{client: srv.client(t)}, map[string]tftypes.Value{
		"skill_id": tfString("skill_x"),
	})
	assertStringAttrs(t, state, map[string]*string{
		"display_title": nil,
		"source":        ptr("anthropic"),
	})
}

// A 200 whose body is not a JSON object maps to empty values, not an error,
// exactly as SDK v1.67.0 did.
func TestSkillDataSourceRead_notAnObject(t *testing.T) {
	t.Parallel()
	srv := newBetaSkillsServer(t, map[string]http.HandlerFunc{
		"GET /v1/skills/skill_x": jsonResponse(`[]`),
	})
	state := readDataSource(t, &SkillDataSource{client: srv.client(t)}, map[string]tftypes.Value{
		"skill_id": tfString("skill_x"),
	})
	assertStringAttrs(t, state, map[string]*string{
		"id":            ptr(""),
		"display_title": nil,
		"source":        ptr(""),
	})
}
