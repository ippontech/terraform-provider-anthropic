// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func skillResourceSchema(t *testing.T) (rschema.Schema, tftypes.Object) {
	t.Helper()
	var resp resource.SchemaResponse
	(&SkillResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", resp.Diagnostics)
	}
	return resp.Schema, resp.Schema.Type().TerraformType(context.Background()).(tftypes.Object)
}

// writeSkillBundle writes <tmp>/test-skill/SKILL.md and returns its path.
func writeSkillBundle(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "test-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(p, []byte("---\nname: test-skill\ndescription: For testing.\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func filesValue(paths ...string) tftypes.Value {
	vals := make([]tftypes.Value, len(paths))
	for i, p := range paths {
		vals[i] = tfString(p)
	}
	return tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, vals)
}

func TestSkillResourceCreate_betaShape(t *testing.T) {
	t.Parallel()
	upload := &multipartCapture{}
	srv := newBetaSkillsServer(t, map[string]http.HandlerFunc{"POST /v1/skills": upload.handler(t, betaSkillJSON)})
	sch, typ := skillResourceSchema(t)
	unknown := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	plan := tfObjectValue(typ, map[string]tftypes.Value{
		"display_title":  tfString("Test Skill"),
		"files":          filesValue(writeSkillBundle(t)),
		"force_destroy":  tftypes.NewValue(tftypes.Bool, false),
		"id":             unknown,
		"created_at":     unknown,
		"updated_at":     unknown,
		"latest_version": unknown,
		"source":         unknown,
	})
	resp := &resource.CreateResponse{State: tfsdk.State{Raw: plan, Schema: sch}}
	(&SkillResource{client: srv.client(t)}).Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Raw: plan, Schema: sch}}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("create: %v", resp.Diagnostics)
	}

	// Request: the beta multipart field names, the bundle directory kept in the filename.
	fields, files := upload.recorded()
	if got := fields["display_title"]; !slices.Equal(got, []string{"Test Skill"}) {
		t.Errorf("display_title field = %q", got)
	}
	if _, ok := fields["display_name"]; ok {
		t.Error("display_name must not be sent under the beta header")
	}
	if !slices.Equal(files, []string{"files[]=test-skill/SKILL.md"}) {
		t.Errorf("file parts = %q", files)
	}

	assertStringAttrs(t, resp.State, map[string]*string{
		"id":             ptr(fixtureSkillID),
		"display_title":  ptr("Test Skill"),
		"created_at":     ptr(fixtureSkillCreated),
		"updated_at":     ptr(fixtureSkillUpdated),
		"latest_version": ptr(fixtureSkillVersion),
		"source":         ptr("custom"),
	})
}

func TestSkillResourceCreate_noDisplayTitle(t *testing.T) {
	t.Parallel()
	upload := &multipartCapture{}
	srv := newBetaSkillsServer(t, map[string]http.HandlerFunc{"POST /v1/skills": upload.handler(t, betaSkillJSON)})
	sch, typ := skillResourceSchema(t)
	unknown := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	plan := tfObjectValue(typ, map[string]tftypes.Value{
		"files":          filesValue(writeSkillBundle(t)),
		"force_destroy":  tftypes.NewValue(tftypes.Bool, false),
		"id":             unknown,
		"created_at":     unknown,
		"updated_at":     unknown,
		"latest_version": unknown,
		"source":         unknown,
	})
	resp := &resource.CreateResponse{State: tfsdk.State{Raw: plan, Schema: sch}}
	(&SkillResource{client: srv.client(t)}).Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Raw: plan, Schema: sch}}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("create: %v", resp.Diagnostics)
	}
	if fields, _ := upload.recorded(); len(fields) != 0 {
		t.Errorf("no form field expected without display_title, got %q", fields)
	}
	// display_title not configured: the API's derived title is not adopted.
	assertStringAttrs(t, resp.State, map[string]*string{"display_title": nil, "id": ptr(fixtureSkillID)})
}

func TestSkillResourceRead_betaShape(t *testing.T) {
	t.Parallel()
	srv := newBetaSkillsServer(t, map[string]http.HandlerFunc{
		"GET /v1/skills/" + fixtureSkillID: jsonResponse(betaSkillJSON),
	})
	sch, typ := skillResourceSchema(t)
	r := &SkillResource{client: srv.client(t)}

	read := func(prior map[string]tftypes.Value) *resource.ReadResponse {
		t.Helper()
		state := tfsdk.State{Raw: tfObjectValue(typ, prior), Schema: sch}
		resp := &resource.ReadResponse{State: state}
		r.Read(context.Background(), resource.ReadRequest{State: state}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("read: %v", resp.Diagnostics)
		}
		return resp
	}

	// Refresh of a skill created without display_title: it stays null.
	resp := read(map[string]tftypes.Value{
		"id":         tfString(fixtureSkillID),
		"created_at": tfString(fixtureSkillCreated),
	})
	assertStringAttrs(t, resp.State, map[string]*string{
		"id":             ptr(fixtureSkillID),
		"display_title":  nil,
		"created_at":     ptr(fixtureSkillCreated),
		"updated_at":     ptr(fixtureSkillUpdated),
		"latest_version": ptr(fixtureSkillVersion),
		"source":         ptr("custom"),
	})

	// Import (only the ID in state): display_title is read from the API.
	resp = read(map[string]tftypes.Value{"id": tfString(fixtureSkillID)})
	assertStringAttrs(t, resp.State, map[string]*string{
		"display_title": ptr("Test Skill"),
		"created_at":    ptr(fixtureSkillCreated),
	})
}

// force_destroy deletes each version by its beta `version` (the Unix-timestamp
// identifier, not the version's `id`), then the skill.
func TestSkillResourceDelete_forceDestroyUsesBetaVersion(t *testing.T) {
	t.Parallel()
	const otherVersion = "1759178010641130"
	srv := newBetaSkillsServer(t, map[string]http.HandlerFunc{
		"GET /v1/skills/" + fixtureSkillID + "/versions": jsonResponse(`{"data":[` + betaSkillVersionJSON + `,
			{"id":"skillver_other","version":"` + otherVersion + `","skill_id":"` + fixtureSkillID + `","type":"skill_version"}],
			"has_more":false,"next_page":null}`),
		"DELETE /v1/skills/" + fixtureSkillID + "/versions/" + fixtureSkillVersion: jsonResponse(`{"id":"` + fixtureVersionID + `","type":"skill_version_deleted"}`),
		"DELETE /v1/skills/" + fixtureSkillID + "/versions/" + otherVersion:        jsonResponse(`{"id":"skillver_other","type":"skill_version_deleted"}`),
		"DELETE /v1/skills/" + fixtureSkillID:                                      jsonResponse(`{"id":"` + fixtureSkillID + `","type":"skill_deleted"}`),
	})
	sch, typ := skillResourceSchema(t)
	state := tfsdk.State{Raw: tfObjectValue(typ, map[string]tftypes.Value{
		"id":            tfString(fixtureSkillID),
		"force_destroy": tftypes.NewValue(tftypes.Bool, true),
	}), Schema: sch}
	resp := &resource.DeleteResponse{State: state}
	(&SkillResource{client: srv.client(t)}).Delete(context.Background(), resource.DeleteRequest{State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("delete: %v", resp.Diagnostics)
	}
	want := []string{
		"GET /v1/skills/" + fixtureSkillID + "/versions",
		"DELETE /v1/skills/" + fixtureSkillID + "/versions/" + fixtureSkillVersion,
		"DELETE /v1/skills/" + fixtureSkillID + "/versions/" + otherVersion,
		"DELETE /v1/skills/" + fixtureSkillID,
	}
	if got := srv.Calls(); !slices.Equal(got, want) {
		t.Fatalf("calls:\n got  %q\n want %q", got, want)
	}
}
