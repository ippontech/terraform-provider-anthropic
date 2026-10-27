// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

// newPluginModel returns a model whose every attribute is null, with the
// nested values typed so it converts to a plan or state.
func newPluginModel() PluginResourceModel {
	return PluginResourceModel{
		ContentScan: types.ObjectNull(pluginContentScanAttrTypes),
		Components:  types.ListNull(types.ObjectType{AttrTypes: pluginComponentAttrTypes}),
		CreatedBy:   types.ObjectNull(pluginActorAttrTypes),
	}
}

// statePluginModel is a managed plugin as state holds it after a refresh of
// pluginJSON(servedVersionID, pinned), created from sourceDir.
func statePluginModel(t *testing.T, sourceDir, servedVersionID string, pinned bool) PluginResourceModel {
	t.Helper()
	m := newPluginModel()
	m.SourceDir = types.StringValue(sourceDir)
	p, err := decodePlugin([]byte(pluginJSON(servedVersionID, pinned)))
	if err != nil {
		t.Fatal(err)
	}
	if diags := mapPluginToState(p, &m); diags.HasError() {
		t.Fatal(diags)
	}
	return m
}

func createPluginResource(t *testing.T, r *PluginResource, plan PluginResourceModel) (resource.CreateResponse, PluginResourceModel) {
	t.Helper()
	resp := resource.CreateResponse{State: emptyState(t, r)}
	r.Create(context.Background(), resource.CreateRequest{Plan: planOf(t, r, plan)}, &resp)
	var got PluginResourceModel
	if !resp.State.Raw.IsNull() {
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading state: %v", diags)
		}
	}
	return resp, got
}

func TestPluginResource_CreateFromDirectory(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins": {ok(pluginJSON("pluginver_02", false))},
	})
	r := &PluginResource{client: client}
	root := writePluginDir(t)

	plan := newPluginModel()
	plan.SourceDir = types.StringValue(root)
	plan.ReleaseNotes = types.StringValue("First release")
	plan.MarketplaceID = types.StringUnknown()
	plan.ServedVersionID = types.StringUnknown()
	resp, got := createPluginResource(t, r, plan)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	reqs := requests()
	assertPluginsBeta(t, reqs)
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want one upload", len(reqs))
	}
	files, fields := uploadParts(t, reqs[0].ContentType, reqs[0].Body)
	if _, ok := files[".claude-plugin/plugin.json"]; !ok || len(files) != 2 {
		t.Errorf("uploaded files = %v, want the plugin's files under their paths", files)
	}
	if fields["release_notes"] != "First release" {
		t.Errorf("release_notes field = %q", fields["release_notes"])
	}
	if _, sent := fields["marketplace_id"]; sent {
		t.Error("marketplace_id was sent although unset: the API must pick the library marketplace")
	}

	if got.ID.ValueString() != "plugin_01" || got.Name.ValueString() != "sales-toolkit" ||
		got.MarketplaceID.ValueString() != "marketplace_01" || got.ServedVersionID.ValueString() != "pluginver_02" {
		t.Errorf("state = %+v, want the created plugin's identity", got)
	}
	if got.SourceDir.ValueString() != root || got.ReleaseNotes.ValueString() != "First release" {
		t.Error("the create-only inputs must stay in state as configured")
	}
	if got.ServedVersionPinned.ValueBool() || !got.OrganizationInstallationPreferenceInherited.ValueBool() {
		t.Errorf("pinned = %v, inherited = %v", got.ServedVersionPinned, got.OrganizationInstallationPreferenceInherited)
	}
	if got.ContentScan.Attributes()["assessment"].(types.String).ValueString() != "pass" ||
		!got.ContentScan.Attributes()["reason"].IsNull() {
		t.Errorf("content_scan = %v", got.ContentScan)
	}
	components := got.Components.Elements()
	if len(components) != 2 || !components[1].(types.Object).Attributes()["description"].IsNull() {
		t.Errorf("components = %v, want two with a null MCP server description", got.Components)
	}
	if got.CreatedBy.Attributes()["api_key_id"].(types.String).ValueString() != "apikey_01" ||
		!got.CreatedBy.Attributes()["user_id"].IsNull() {
		t.Errorf("created_by = %v", got.CreatedBy)
	}
}

func TestPluginResource_CreateFromArchiveIntoAMarketplace(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins": {ok(pluginJSON("pluginver_02", false))},
	})
	r := &PluginResource{client: client}
	archive := filepath.Join(t.TempDir(), "sales-toolkit.plugin")
	writeFile(t, archive, "PK archive bytes")

	plan := newPluginModel()
	plan.SourceArchive = types.StringValue(archive)
	plan.MarketplaceID = types.StringValue("marketplace_01")
	resp, _ := createPluginResource(t, r, plan)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	files, fields := uploadParts(t, requests()[0].ContentType, requests()[0].Body)
	if files["sales-toolkit.plugin"] != "PK archive bytes" || len(files) != 1 {
		t.Errorf("uploaded files = %v, want the archive alone under its base name", files)
	}
	if fields["marketplace_id"] != "marketplace_01" {
		t.Errorf("marketplace_id field = %q", fields["marketplace_id"])
	}
}

func TestPluginResource_CreateCompletesAPendingRegistration(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins": {apiError(http.StatusServiceUnavailable, "api_error", "registration pending",
			`{"error_code":"registration_pending","plugin_id":"plugin_01","plugin_version_id":"pluginver_01"}`,
			map[string]string{"x-should-retry": "false"})},
		"POST /v1/organizations/plugins/plugin_01/versions": {ok(pluginVersionJSON)},
		"GET /v1/organizations/plugins/plugin_01":           {ok(pluginJSON("pluginver_03", false))},
	})
	r := &PluginResource{client: client}

	plan := newPluginModel()
	plan.SourceDir = types.StringValue(writePluginDir(t))
	plan.MarketplaceID = types.StringValue("marketplace_01")
	plan.ReleaseNotes = types.StringValue("First release")
	resp, got := createPluginResource(t, r, plan)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	reqs := requests()
	assertPluginsBeta(t, reqs)
	var calls []string
	for _, req := range reqs {
		calls = append(calls, req.Method+" "+req.Path)
	}
	want := "POST /v1/organizations/plugins,POST /v1/organizations/plugins/plugin_01/versions,GET /v1/organizations/plugins/plugin_01"
	if strings.Join(calls, ",") != want {
		t.Fatalf("calls = %v, want the create (never resent), a version upload of the same files, then a read", calls)
	}
	files, fields := uploadParts(t, reqs[1].ContentType, reqs[1].Body)
	if len(files) != 2 || fields["release_notes"] != "First release" {
		t.Errorf("version upload = %v / %v, want the same files and release notes", files, fields)
	}
	if _, sent := fields["marketplace_id"]; sent {
		t.Error("a version upload takes no marketplace_id")
	}
	if got.ID.ValueString() != "plugin_01" || got.ServedVersionID.ValueString() != "pluginver_03" {
		t.Errorf("state = %s / %s, want the completed plugin", got.ID, got.ServedVersionID)
	}
}

func TestPluginResource_CreateIsNotRetriedOnAServerError(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins": {apiError(http.StatusInternalServerError, "api_error", "boom", "", nil)},
	})
	r := &PluginResource{client: client}

	plan := newPluginModel()
	plan.SourceDir = types.StringValue(writePluginDir(t))
	resp, _ := createPluginResource(t, r, plan)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error")
	}
	if n := len(requests()); n != 1 {
		t.Errorf("requests = %d, want 1: a 500 on an upload may have stored the plugin, so it is never resent", n)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("a failed create must not record state")
	}
}

func TestPluginResource_CreateNamesThePluginHoldingTheName(t *testing.T) {
	t.Parallel()
	client, _ := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins": {apiError(http.StatusConflict, "invalid_request_error", "name taken",
			`{"error_code":"plugin_name_taken","plugin_id":"plugin_09"}`, nil)},
	})
	r := &PluginResource{client: client}

	plan := newPluginModel()
	plan.SourceDir = types.StringValue(writePluginDir(t))
	resp, _ := createPluginResource(t, r, plan)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error")
	}
	detail := resp.Diagnostics.Errors()[0].Detail()
	if !strings.Contains(detail, "plugin_09") || !strings.Contains(detail, "terraform import") {
		t.Errorf("error = %q, want the existing plugin's ID and the import hint", detail)
	}
}

func TestPluginResource_CreateServesTheConfiguredVersion(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins":           {ok(pluginJSON("pluginver_02", false))},
		"POST /v1/organizations/plugins/plugin_01": {ok(pluginJSON("pluginver_02", true))},
	})
	r := &PluginResource{client: client}

	plan := newPluginModel()
	plan.SourceDir = types.StringValue(writePluginDir(t))
	plan.ServedVersionID = types.StringValue("pluginver_02")
	resp, got := createPluginResource(t, r, plan)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	reqs := requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want the upload then the pin (even on the version already served)", len(reqs))
	}
	var body map[string]any
	if err := json.Unmarshal(reqs[1].Body, &body); err != nil || body["served_version_id"] != "pluginver_02" || len(body) != 1 {
		t.Errorf("update body = %s, want exactly {\"served_version_id\": ...}", reqs[1].Body)
	}
	if !got.ServedVersionPinned.ValueBool() {
		t.Error("expected the plugin to be pinned")
	}
}

func TestPluginResource_Read(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"GET /v1/organizations/plugins/plugin_01": {ok(pluginJSON("pluginver_01", true))},
	})
	r := &PluginResource{client: client}
	prior := statePluginModel(t, "/plugins/sales", "pluginver_02", false)

	resp := resource.ReadResponse{State: stateOf(t, r, prior)}
	r.Read(context.Background(), resource.ReadRequest{State: stateOf(t, r, prior)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	assertPluginsBeta(t, requests())

	var got PluginResourceModel
	resp.State.Get(context.Background(), &got)
	if got.ServedVersionID.ValueString() != "pluginver_01" || !got.ServedVersionPinned.ValueBool() {
		t.Errorf("served = %s pinned = %v, want the refreshed values", got.ServedVersionID, got.ServedVersionPinned)
	}
	if got.SourceDir.ValueString() != "/plugins/sales" {
		t.Error("a refresh must keep the create-only inputs")
	}
}

func TestPluginResource_ReadRemovesADeletedPlugin(t *testing.T) {
	t.Parallel()
	client, _ := fakePluginsAPI(t, map[string][]fakeResponse{
		"GET /v1/organizations/plugins/plugin_01": {apiError(http.StatusNotFound, "not_found_error", "not found", "", nil)},
	})
	r := &PluginResource{client: client}
	prior := statePluginModel(t, "/plugins/sales", "pluginver_02", false)

	resp := resource.ReadResponse{State: stateOf(t, r, prior)}
	r.Read(context.Background(), resource.ReadRequest{State: stateOf(t, r, prior)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("expected the plugin to be removed from state after a 404")
	}
}

// updatePluginResource runs Update with config = plan.
func updatePluginResource(t *testing.T, r *PluginResource, prior, plan PluginResourceModel) (resource.UpdateResponse, PluginResourceModel) {
	t.Helper()
	resp := resource.UpdateResponse{State: stateOf(t, r, prior)}
	r.Update(context.Background(), resource.UpdateRequest{
		Config: configOf(t, r, plan),
		Plan:   planOf(t, r, plan),
		State:  stateOf(t, r, prior),
	}, &resp)
	var got PluginResourceModel
	resp.State.Get(context.Background(), &got)
	return resp, got
}

func TestPluginResource_UpdateServesAnotherVersion(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins/plugin_01": {ok(pluginJSON("pluginver_01", true))},
	})
	r := &PluginResource{client: client}
	prior := statePluginModel(t, "/plugins/sales", "pluginver_02", true)
	plan := prior
	plan.ServedVersionID = types.StringValue("pluginver_01")

	resp, got := updatePluginResource(t, r, prior, plan)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}
	reqs := requests()
	assertPluginsBeta(t, reqs)
	if len(reqs) != 1 || string(reqs[0].Body) != `{"served_version_id":"pluginver_01"}` {
		t.Fatalf("requests = %+v, want one served-version change", reqs)
	}
	if got.ServedVersionID.ValueString() != "pluginver_01" {
		t.Errorf("served_version_id = %s", got.ServedVersionID)
	}
}

func TestPluginResource_UpdatePinsTheVersionAlreadyServed(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins/plugin_01": {ok(pluginJSON("pluginver_02", true))},
	})
	r := &PluginResource{client: client}
	prior := statePluginModel(t, "/plugins/sales", "pluginver_02", false)
	plan := prior
	plan.ServedVersionPinned = types.BoolValue(true)

	resp, got := updatePluginResource(t, r, prior, plan)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}
	if len(requests()) != 1 || !got.ServedVersionPinned.ValueBool() {
		t.Errorf("requests = %d, pinned = %v: configuring the served version must pin an unpinned plugin", len(requests()), got.ServedVersionPinned)
	}
}

func TestPluginResource_UpdateOfACreateOnlyInputCallsNothing(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, nil)
	r := &PluginResource{client: client}
	prior := statePluginModel(t, "/plugins/sales", "pluginver_02", false)
	plan := prior
	plan.SourceDir = types.StringValue("/plugins/sales-v2")
	plan.ReleaseNotes = types.StringValue("Edited")
	plan.LatestVersionID = types.StringUnknown()

	// served_version_id is not configured: the plan carries the state value.
	resp := resource.UpdateResponse{State: stateOf(t, r, prior)}
	config := plan
	config.ServedVersionID = types.StringNull()
	r.Update(context.Background(), resource.UpdateRequest{
		Config: configOf(t, r, config),
		Plan:   planOf(t, r, plan),
		State:  stateOf(t, r, prior),
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}
	if n := len(requests()); n != 0 {
		t.Errorf("requests = %d, want none", n)
	}
	var got PluginResourceModel
	resp.State.Get(context.Background(), &got)
	if got.SourceDir.ValueString() != "/plugins/sales-v2" || got.ReleaseNotes.ValueString() != "Edited" {
		t.Error("the new input values must be recorded")
	}
	if got.LatestVersionID.ValueString() != "pluginver_02" {
		t.Errorf("latest_version_id = %s, want the prior state's value", got.LatestVersionID)
	}
}

func TestPluginResource_UpdateExplainsAPendingScan(t *testing.T) {
	t.Parallel()
	client, _ := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins/plugin_01": {apiError(http.StatusConflict, "invalid_request_error", "scan pending",
			`{"error_code":"scan_pending","plugin_version_id":"pluginver_01"}`, nil)},
	})
	r := &PluginResource{client: client}
	prior := statePluginModel(t, "/plugins/sales", "pluginver_02", true)
	plan := prior
	plan.ServedVersionID = types.StringValue("pluginver_01")

	resp, _ := updatePluginResource(t, r, prior, plan)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "still running") {
		t.Errorf("diagnostics = %v, want the scan_pending explanation", resp.Diagnostics)
	}
}

func TestPluginResource_Delete(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]fakeResponse{
		"deleted":      ok(`{"type":"plugin_deleted","id":"plugin_01"}`),
		"already gone": apiError(http.StatusNotFound, "not_found_error", "not found", "", nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
				"DELETE /v1/organizations/plugins/plugin_01": {answer},
			})
			r := &PluginResource{client: client}
			prior := statePluginModel(t, "/plugins/sales", "pluginver_02", false)

			var resp resource.DeleteResponse
			r.Delete(context.Background(), resource.DeleteRequest{State: stateOf(t, r, prior)}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Delete: %v", resp.Diagnostics)
			}
			assertPluginsBeta(t, requests())
		})
	}
}

func TestPluginResource_DeleteSurfacesAGitSyncedPlugin(t *testing.T) {
	t.Parallel()
	client, _ := fakePluginsAPI(t, map[string][]fakeResponse{
		"DELETE /v1/organizations/plugins/plugin_01": {apiError(http.StatusBadRequest, "invalid_request_error",
			"plugin is synchronized from a repository", "", nil)},
	})
	r := &PluginResource{client: client}
	prior := statePluginModel(t, "/plugins/sales", "pluginver_02", false)

	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: stateOf(t, r, prior)}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Error("expected the 400 to surface")
	}
}

// modifyPluginPlan runs ModifyPlan; prior nil means a create.
func modifyPluginPlan(t *testing.T, config PluginResourceModel, prior *PluginResourceModel) resource.ModifyPlanResponse {
	t.Helper()
	r := &PluginResource{}
	plan := config
	if prior != nil {
		// The framework's UseStateForUnknown has already copied these.
		plan.ID, plan.ServedVersionPinned = prior.ID, prior.ServedVersionPinned
		if config.ServedVersionID.IsNull() {
			plan.ServedVersionID = prior.ServedVersionID
		}
	}
	req := resource.ModifyPlanRequest{Config: configOf(t, r, config), Plan: planOf(t, r, plan), State: emptyState(t, r)}
	if prior != nil {
		req.State = stateOf(t, r, *prior)
	}
	resp := resource.ModifyPlanResponse{Plan: req.Plan}
	r.ModifyPlan(context.Background(), req, &resp)
	return resp
}

func TestPluginResource_ModifyPlanRequiresASourceToCreate(t *testing.T) {
	t.Parallel()
	resp := modifyPluginPlan(t, newPluginModel(), nil)
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Missing plugin source" {
		t.Errorf("diagnostics = %v, want the missing source error", resp.Diagnostics)
	}

	config := newPluginModel()
	config.SourceArchive = types.StringValue("/dist/sales.zip")
	if resp := modifyPluginPlan(t, config, nil); resp.Diagnostics.HasError() {
		t.Errorf("an archive source must do: %v", resp.Diagnostics)
	}
}

func TestPluginResource_ModifyPlanAllowsAnImportedPluginWithoutSource(t *testing.T) {
	t.Parallel()
	prior := statePluginModel(t, "", "pluginver_02", false)
	prior.SourceDir = types.StringNull()
	config := newPluginModel()

	resp := modifyPluginPlan(t, config, &prior)
	if resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 0 {
		t.Errorf("diagnostics = %v, want none for an imported plugin", resp.Diagnostics)
	}
}

func TestPluginResource_ModifyPlanPinsAConfiguredServedVersion(t *testing.T) {
	t.Parallel()
	prior := statePluginModel(t, "/plugins/sales", "pluginver_02", false)
	config := newPluginModel()
	config.SourceDir = types.StringValue("/plugins/sales")
	config.ServedVersionID = types.StringValue("pluginver_02")

	resp := modifyPluginPlan(t, config, &prior)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var pinned types.Bool
	resp.Plan.GetAttribute(context.Background(), path.Root("served_version_pinned"), &pinned)
	if !pinned.ValueBool() {
		t.Error("configuring served_version_id must plan served_version_pinned = true, so an unpinned plugin gets pinned")
	}

	config.ServedVersionID = types.StringNull()
	resp = modifyPluginPlan(t, config, &prior)
	resp.Plan.GetAttribute(context.Background(), path.Root("served_version_pinned"), &pinned)
	if pinned.ValueBool() {
		t.Error("without served_version_id the plan must keep the state's pin")
	}
}

func TestPluginResource_ModifyPlanWarnsOnCreateOnlyChanges(t *testing.T) {
	t.Parallel()
	prior := statePluginModel(t, "/plugins/sales", "pluginver_02", false)
	config := newPluginModel()
	config.SourceDir = types.StringValue("/plugins/sales-v2")

	resp := modifyPluginPlan(t, config, &prior)
	if resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 1 {
		t.Fatalf("diagnostics = %v, want one warning", resp.Diagnostics)
	}
	if !strings.Contains(resp.Diagnostics.Warnings()[0].Detail(), "anthropic_plugin_version") {
		t.Errorf("warning = %q, want it to point at anthropic_plugin_version", resp.Diagnostics.Warnings()[0].Detail())
	}
}

func TestPluginResource_ImportState(t *testing.T) {
	t.Parallel()
	r := &PluginResource{}
	resp := resource.ImportStateResponse{State: schematest.NullState(t, r)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "plugin_01"}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var id types.String
	resp.State.GetAttribute(context.Background(), path.Root("id"), &id)
	if id.ValueString() != "plugin_01" {
		t.Errorf("id = %s", id)
	}
}

func TestComponentsValue_nullAndEmptyStayDistinct(t *testing.T) {
	t.Parallel()
	null, _ := componentsValue(nil)
	empty, _ := componentsValue([]pluginComponent{})
	if !null.IsNull() || empty.IsNull() || len(empty.Elements()) != 0 {
		t.Errorf("null = %v, empty = %v: a version without recorded components is null, one without components is []", null, empty)
	}
}

// Pinning an unpinned plugin moves updated_at, even onto the version it
// already serves: updated_at "Moves on a served-version change (including
// pinning an unpinned Plugin to the version it already serves)"
// (https://platform.claude.com/docs/en/api/beta/organization/plugins/update).
// Nothing else changes in that configuration, so the plan Terraform hands
// to ModifyPlan holds every computed value of the state, updated_at
// included: ModifyPlan must leave it unknown, or the apply fails with
// "Provider produced inconsistent result after apply: .updated_at". A plugin
// already pinned, or a plan without served_version_id, keeps it.
func TestPluginResource_ModifyPlanPinningLeavesUpdatedAtUnknown(t *testing.T) {
	t.Parallel()
	r := &PluginResource{}
	for _, tc := range []struct {
		name           string
		pinned         bool
		servedVersion  types.String
		wantUnknownNow bool
	}{
		{"pins an unpinned plugin to the version it serves", false, types.StringValue("pluginver_02"), true},
		{"already pinned to that version", true, types.StringValue("pluginver_02"), false},
		{"no served_version_id", false, types.StringNull(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prior := statePluginModel(t, "", "pluginver_02", tc.pinned)
			prior.SourceDir = types.StringNull() // an imported plugin
			config := newPluginModel()
			config.ServedVersionID = tc.servedVersion
			// The plan as Terraform builds it: no configured value differs from
			// the state, so every computed value is the state's.
			plan := prior

			req := resource.ModifyPlanRequest{Config: configOf(t, r, config), Plan: planOf(t, r, plan), State: stateOf(t, r, prior)}
			resp := resource.ModifyPlanResponse{Plan: req.Plan}
			r.ModifyPlan(context.Background(), req, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}

			var updatedAt types.String
			resp.Plan.GetAttribute(context.Background(), path.Root("updated_at"), &updatedAt)
			if tc.wantUnknownNow && !updatedAt.IsUnknown() {
				t.Errorf("updated_at = %s, want unknown: the pin moves it, so the planned value makes the apply inconsistent", updatedAt)
			}
			if !tc.wantUnknownNow && !updatedAt.Equal(prior.UpdatedAt) {
				t.Errorf("updated_at = %s, want the state's %s", updatedAt, prior.UpdatedAt)
			}
		})
	}
}
