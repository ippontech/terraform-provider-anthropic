// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func newPluginVersionModel() PluginVersionResourceModel {
	return PluginVersionResourceModel{
		ContentScan: types.ObjectNull(pluginContentScanAttrTypes),
		Components:  types.ListNull(types.ObjectType{AttrTypes: pluginComponentAttrTypes}),
		CreatedBy:   types.ObjectNull(pluginActorAttrTypes),
	}
}

func createPluginVersion(t *testing.T, r *PluginVersionResource, plan PluginVersionResourceModel) (resource.CreateResponse, PluginVersionResourceModel) {
	t.Helper()
	resp := resource.CreateResponse{State: emptyState(t, r)}
	r.Create(context.Background(), resource.CreateRequest{Plan: planOf(t, r, plan)}, &resp)
	var got PluginVersionResourceModel
	if !resp.State.Raw.IsNull() {
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading state: %v", diags)
		}
	}
	return resp, got
}

func sourceHashOf(t *testing.T, dir, archive string) string {
	t.Helper()
	src, err := listPluginSource(dir, archive)
	if err != nil {
		t.Fatal(err)
	}
	h, err := src.hash()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestPluginVersionResource_Create(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins/plugin_01/versions": {ok(pluginVersionJSON)},
	})
	r := &PluginVersionResource{client: client}
	root := writePluginDir(t)

	plan := newPluginVersionModel()
	plan.PluginID = types.StringValue("plugin_01")
	plan.SourceDir = types.StringValue(root)
	plan.ReleaseNotes = types.StringValue("Adds the call-prep command.")
	plan.SourceHash = types.StringValue(sourceHashOf(t, root, ""))
	resp, got := createPluginVersion(t, r, plan)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	reqs := requests()
	assertPluginsBeta(t, reqs)
	files, fields := uploadParts(t, reqs[0].ContentType, reqs[0].Body)
	if len(files) != 2 || fields["release_notes"] != "Adds the call-prep command." || len(fields) != 1 {
		t.Errorf("upload = %v / %v, want the plugin's files and the release notes", files, fields)
	}

	if got.ID.ValueString() != "pluginver_03" || got.PluginID.ValueString() != "plugin_01" {
		t.Errorf("state = %s / %s", got.ID, got.PluginID)
	}
	if got.SourceHash.ValueString() != plan.SourceHash.ValueString() {
		t.Errorf("source_hash = %s, want the planned %s", got.SourceHash, plan.SourceHash)
	}
	if got.ManifestVersion.ValueString() != "1.5.0" || !got.Reach.IsNull() {
		t.Errorf("manifest_version = %s, reach = %s", got.ManifestVersion, got.Reach)
	}
	if got.ContentScan.Attributes()["status"].(types.String).ValueString() != "processing" ||
		!got.ContentScan.Attributes()["assessment"].IsNull() {
		t.Errorf("content_scan = %v", got.ContentScan)
	}
	if got.Components.IsNull() || len(got.Components.Elements()) != 0 {
		t.Errorf("components = %v, want an empty list", got.Components)
	}
	if !got.CreatedBy.Attributes()["email_address"].IsNull() ||
		got.CreatedBy.Attributes()["user_id"].(types.String).ValueString() != "user_01" {
		t.Errorf("created_by = %v", got.CreatedBy)
	}
}

func TestPluginVersionResource_CreateRefusesContentChangedSincePlan(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, nil)
	r := &PluginVersionResource{client: client}

	plan := newPluginVersionModel()
	plan.PluginID = types.StringValue("plugin_01")
	plan.SourceDir = types.StringValue(writePluginDir(t))
	plan.SourceHash = types.StringValue(strings.Repeat("0", 64))
	resp, _ := createPluginVersion(t, r, plan)
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Plugin source changed since the plan" {
		t.Errorf("diagnostics = %v", resp.Diagnostics)
	}
	if n := len(requests()); n != 0 {
		t.Errorf("requests = %d, want none: the upload cannot be undone", n)
	}
}

func TestPluginVersionResource_CreateResendsAPendingRegistrationWhenAsked(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins/plugin_01/versions": {
			apiError(http.StatusServiceUnavailable, "api_error", "registration pending",
				`{"error_code":"registration_pending","plugin_version_id":"pluginver_02"}`,
				map[string]string{"x-should-retry": "true"}),
			ok(pluginVersionJSON),
		},
	})
	r := &PluginVersionResource{client: client}
	archive := filepath.Join(t.TempDir(), "sales-toolkit.zip")
	writeFile(t, archive, "PK")

	plan := newPluginVersionModel()
	plan.PluginID = types.StringValue("plugin_01")
	plan.SourceArchive = types.StringValue(archive)
	resp, got := createPluginVersion(t, r, plan)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}
	reqs := requests()
	if len(reqs) != 2 || string(reqs[0].Body) != string(reqs[1].Body) {
		t.Errorf("requests = %d, want the same upload resent once, as x-should-retry: true asks", len(reqs))
	}
	if got.ID.ValueString() != "pluginver_03" {
		t.Errorf("id = %s, want the version of the completed upload", got.ID)
	}
	if got.SourceHash.ValueString() != sourceHashOf(t, "", archive) {
		t.Error("source_hash must be the archive's hash")
	}
}

func TestPluginVersionResource_CreateIsNotRetriedOnAServerError(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins/plugin_01/versions": {apiError(http.StatusBadGateway, "api_error", "bad gateway", "", nil)},
	})
	r := &PluginVersionResource{client: client}

	plan := newPluginVersionModel()
	plan.PluginID = types.StringValue("plugin_01")
	plan.SourceDir = types.StringValue(writePluginDir(t))
	resp, _ := createPluginVersion(t, r, plan)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error")
	}
	if n := len(requests()); n != 1 {
		t.Errorf("requests = %d, want 1: a resent upload stores a second version", n)
	}
}

func statePluginVersionModel(t *testing.T) PluginVersionResourceModel {
	t.Helper()
	m := newPluginVersionModel()
	m.PluginID = types.StringValue("plugin_01")
	m.SourceDir = types.StringValue("/plugins/sales")
	m.SourceHash = types.StringValue(strings.Repeat("a", 64))
	v, err := decodePluginVersion([]byte(pluginVersionJSON))
	if err != nil {
		t.Fatal(err)
	}
	if diags := mapPluginVersionToState(v, &m); diags.HasError() {
		t.Fatal(diags)
	}
	return m
}

func TestPluginVersionResource_Read(t *testing.T) {
	t.Parallel()
	scanned := strings.Replace(pluginVersionJSON, `{"status": "processing", "assessment": null, "reason": null}`,
		`{"status": "completed", "assessment": "warn", "reason": "credential-exposure"}`, 1)
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"GET /v1/organizations/plugins/plugin_01/versions/pluginver_03": {ok(scanned)},
	})
	r := &PluginVersionResource{client: client}
	prior := statePluginVersionModel(t)

	resp := resource.ReadResponse{State: stateOf(t, r, prior)}
	r.Read(context.Background(), resource.ReadRequest{State: stateOf(t, r, prior)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	assertPluginsBeta(t, requests())
	var got PluginVersionResourceModel
	resp.State.Get(context.Background(), &got)
	if got.ContentScan.Attributes()["reason"].(types.String).ValueString() != "credential-exposure" {
		t.Errorf("content_scan = %v, want the verdict that arrived after the upload", got.ContentScan)
	}
	if got.SourceHash.ValueString() != prior.SourceHash.ValueString() || got.SourceDir.ValueString() != "/plugins/sales" {
		t.Error("a refresh must keep the inputs and the hash")
	}
}

func TestPluginVersionResource_ReadRemovesAVersionThatIsGone(t *testing.T) {
	t.Parallel()
	client, _ := fakePluginsAPI(t, map[string][]fakeResponse{
		"GET /v1/organizations/plugins/plugin_01/versions/pluginver_03": {apiError(http.StatusNotFound, "not_found_error", "not found", "", nil)},
	})
	r := &PluginVersionResource{client: client}
	prior := statePluginVersionModel(t)

	resp := resource.ReadResponse{State: stateOf(t, r, prior)}
	r.Read(context.Background(), resource.ReadRequest{State: stateOf(t, r, prior)}, &resp)
	if resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
		t.Errorf("diagnostics = %v, state null = %v: a deleted plugin takes its versions with it", resp.Diagnostics, resp.State.Raw.IsNull())
	}
}

func TestPluginVersionResource_DeleteOnlyForgets(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, nil)
	r := &PluginVersionResource{client: client}

	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: stateOf(t, r, statePluginVersionModel(t))}, &resp)
	if resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 1 {
		t.Errorf("diagnostics = %v, want one warning", resp.Diagnostics)
	}
	if n := len(requests()); n != 0 {
		t.Errorf("requests = %d, want none: there is no per-version delete", n)
	}
}

func TestPluginSourceHashModifier(t *testing.T) {
	t.Parallel()
	r := &PluginVersionResource{}
	root := writePluginDir(t)
	stale := types.StringValue(strings.Repeat("a", 64))

	modify := func(dir string) types.String {
		t.Helper()
		plan := newPluginVersionModel()
		plan.PluginID = types.StringValue("plugin_01")
		plan.SourceDir = types.StringValue(dir)
		plan.SourceHash = stale
		req := planmodifier.StringRequest{Plan: planOf(t, r, plan), PlanValue: stale, StateValue: stale}
		resp := planmodifier.StringResponse{PlanValue: req.PlanValue}
		pluginSourceHashModifier{}.PlanModifyString(context.Background(), req, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatal(resp.Diagnostics)
		}
		return resp.PlanValue
	}

	if got := modify(root); got.ValueString() != sourceHashOf(t, root, "") {
		t.Errorf("plan value = %s, want the hash of the files on disk", got)
	}
	if got := modify(filepath.Join(root, "missing")); !got.Equal(stale) {
		t.Errorf("plan value = %s, want the prior value kept when the source cannot be read at plan time", got)
	}
}
