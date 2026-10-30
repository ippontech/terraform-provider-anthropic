// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func skillVersionResourceSchema(t *testing.T) (rschema.Schema, tftypes.Object) {
	t.Helper()
	var resp resource.SchemaResponse
	(&SkillVersionResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", resp.Diagnostics)
	}
	return resp.Schema, resp.Schema.Type().TerraformType(context.Background()).(tftypes.Object)
}

// betaSkillVersionState is what SDK v1.67.0 mapped from betaSkillVersionJSON.
func betaSkillVersionState() map[string]*string {
	return map[string]*string{
		"id":          new(fixtureVersionID),
		"skill_id":    new(fixtureSkillID),
		"version":     new(fixtureSkillVersion),
		"name":        new("test-skill"),
		"description": new("For testing."),
		"directory":   new("test-skill"),
		"created_at":  new(fixtureVersionCreate),
	}
}

func TestSkillVersionResourceCreate_betaShape(t *testing.T) {
	t.Parallel()
	upload := &multipartCapture{}
	srv := newBetaSkillsServer(t, map[string]http.HandlerFunc{
		"POST /v1/skills/" + fixtureSkillID + "/versions": upload.handler(t, betaSkillVersionJSON),
	})
	sch, typ := skillVersionResourceSchema(t)
	unknown := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	plan := tfObjectValue(typ, map[string]tftypes.Value{
		"skill_id":    tfString(fixtureSkillID),
		"files":       filesValue(writeSkillBundle(t)),
		"id":          unknown,
		"version":     unknown,
		"name":        unknown,
		"description": unknown,
		"directory":   unknown,
		"created_at":  unknown,
	})
	resp := &resource.CreateResponse{State: tfsdk.State{Raw: plan, Schema: sch}}
	(&SkillVersionResource{client: srv.client(t)}).Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Raw: plan, Schema: sch}}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("create: %v", resp.Diagnostics)
	}
	fields, files := upload.recorded()
	if len(fields) != 0 {
		t.Errorf("unexpected form fields %q", fields)
	}
	if !slices.Equal(files, []string{"files[]=test-skill/SKILL.md"}) {
		t.Errorf("file parts = %q", files)
	}
	assertStringAttrs(t, resp.State, betaSkillVersionState())
}

// Read looks the version up by its beta `version`, which is also what import sets.
func TestSkillVersionResourceRead_betaShape(t *testing.T) {
	t.Parallel()
	srv := newBetaSkillsServer(t, map[string]http.HandlerFunc{
		"GET /v1/skills/" + fixtureSkillID + "/versions/" + fixtureSkillVersion: jsonResponse(betaSkillVersionJSON),
	})
	sch, typ := skillVersionResourceSchema(t)
	state := tfsdk.State{Raw: tfObjectValue(typ, map[string]tftypes.Value{
		"skill_id": tfString(fixtureSkillID),
		"version":  tfString(fixtureSkillVersion),
	}), Schema: sch}
	resp := &resource.ReadResponse{State: state}
	(&SkillVersionResource{client: srv.client(t)}).Read(context.Background(), resource.ReadRequest{State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	assertStringAttrs(t, resp.State, betaSkillVersionState())
}

func TestSkillVersionResourceDelete_betaVersion(t *testing.T) {
	t.Parallel()
	srv := newBetaSkillsServer(t, map[string]http.HandlerFunc{
		"DELETE /v1/skills/" + fixtureSkillID + "/versions/" + fixtureSkillVersion: jsonResponse(`{"id":"` + fixtureVersionID + `","type":"skill_version_deleted"}`),
	})
	sch, typ := skillVersionResourceSchema(t)
	state := tfsdk.State{Raw: tfObjectValue(typ, map[string]tftypes.Value{
		"id":       tfString(fixtureVersionID),
		"skill_id": tfString(fixtureSkillID),
		"version":  tfString(fixtureSkillVersion),
	}), Schema: sch}
	resp := &resource.DeleteResponse{State: state}
	(&SkillVersionResource{client: srv.client(t)}).Delete(context.Background(), resource.DeleteRequest{State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("delete: %v", resp.Diagnostics)
	}
	if got := srv.Calls(); len(got) != 1 {
		t.Fatalf("calls = %q", got)
	}
}
