// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
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

func TestPluginVersionResource_CreateNamesTheVersionStillPendingAfterRetries(t *testing.T) {
	t.Parallel()
	pending := func(versionID string) fakeResponse {
		return apiError(http.StatusServiceUnavailable, "api_error", "registration pending",
			`{"error_code":"registration_pending","plugin_version_id":"`+versionID+`"}`,
			map[string]string{"x-should-retry": "true"})
	}
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins/plugin_01/versions": {
			pending("pluginver_02"), pending("pluginver_03"), pending("pluginver_04"), pending("pluginver_05"),
		},
	})
	r := &PluginVersionResource{client: client}

	plan := newPluginVersionModel()
	plan.PluginID = types.StringValue("plugin_01")
	plan.SourceDir = types.StringValue(writePluginDir(t))
	resp, _ := createPluginVersion(t, r, plan)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error")
	}
	n := len(requests())
	last := fmt.Sprintf("pluginver_%02d", n+1)
	if detail := resp.Diagnostics.Errors()[0].Detail(); !strings.Contains(detail, last) || !strings.Contains(detail, "plugin_01") {
		t.Errorf("detail = %q, want the version %s the last attempt stored", detail, last)
	}
	if n < 2 {
		t.Errorf("requests = %d, want the upload resent as x-should-retry asks", n)
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

// A source that exists but no longer validates must fail the plan. Keeping
// the prior hash, as for a missing source, made the plan of an existing
// version say "No changes": the edit was never published and nothing said
// why (Create, which reads the source again, only runs for a new version).
func TestPluginSourceHashModifier_invalidSourceFailsThePlan(t *testing.T) {
	t.Parallel()
	r := &PluginVersionResource{}
	stale := types.StringValue(strings.Repeat("a", 64))

	for name, damage := range map[string]func(t *testing.T, root string){
		"symbolic link added": func(t *testing.T, root string) {
			if err := os.Symlink(filepath.Join(root, "skills"), filepath.Join(root, "node_modules")); err != nil {
				t.Fatal(err)
			}
		},
		"manifest moved": func(t *testing.T, root string) {
			if err := os.Rename(filepath.Join(root, ".claude-plugin"), filepath.Join(root, "claude-plugin")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := writePluginDir(t)
			damage(t, root)

			plan := newPluginVersionModel()
			plan.PluginID = types.StringValue("plugin_01")
			plan.SourceDir = types.StringValue(root)
			plan.SourceHash = stale
			req := planmodifier.StringRequest{Path: path.Root("source_hash"), Plan: planOf(t, r, plan), PlanValue: stale, StateValue: stale}
			resp := planmodifier.StringResponse{PlanValue: req.PlanValue}
			pluginSourceHashModifier{}.PlanModifyString(context.Background(), req, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatalf("plan value = %s with no error, want the invalid source reported at plan time", resp.PlanValue)
			}
			if d := resp.Diagnostics.Errors()[0]; d.Summary() != "Invalid plugin source" {
				t.Errorf("diagnostic = %s: %s", d.Summary(), d.Detail())
			}
		})
	}
}

// A new version's directory can exist at plan time while the same apply still
// writes into it (its manifest rendered from a template, say). With no prior
// state the source is checked by Create, at apply, so the plan leaves
// source_hash unknown instead of failing; an existing version still fails the
// plan (previous test).
func TestPluginSourceHashModifier_newVersionDefersAnIncompleteSourceToApply(t *testing.T) {
	t.Parallel()
	r := &PluginVersionResource{}
	root := t.TempDir() // exists, but holds no manifest yet

	plan := newPluginVersionModel()
	plan.PluginID = types.StringValue("plugin_01")
	plan.SourceDir = types.StringValue(root)
	plan.SourceHash = types.StringUnknown()
	req := planmodifier.StringRequest{
		Path: path.Root("source_hash"), Plan: planOf(t, r, plan), State: emptyState(t, r),
		PlanValue: types.StringUnknown(), StateValue: types.StringNull(),
	}
	resp := planmodifier.StringResponse{PlanValue: req.PlanValue}
	pluginSourceHashModifier{}.PlanModifyString(context.Background(), req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("diagnostics = %v, want none: Create checks a new version's source at apply", resp.Diagnostics)
	}
	if !resp.PlanValue.IsUnknown() {
		t.Errorf("plan value = %s, want unknown", resp.PlanValue)
	}
}

// The version belongs to the plugin the request path names: a payload that
// leaves plugin_id out must not blank it in state, where the "" would differ
// from the configuration and, plugin_id being RequiresReplace, upload another
// version (which cannot be deleted) at every plan.
func TestPluginVersionResource_KeepsThePluginIDOfTheRequest(t *testing.T) {
	t.Parallel()
	withoutPluginID := strings.Replace(pluginVersionJSON, `"plugin_id": "plugin_01",`, "", 1)
	client, _ := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins/plugin_01/versions":             {ok(withoutPluginID)},
		"GET /v1/organizations/plugins/plugin_01/versions/pluginver_03": {ok(withoutPluginID)},
	})
	r := &PluginVersionResource{client: client}

	plan := newPluginVersionModel()
	plan.PluginID = types.StringValue("plugin_01")
	plan.SourceDir = types.StringValue(writePluginDir(t))
	resp, created := createPluginVersion(t, r, plan)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}
	if created.PluginID.ValueString() != "plugin_01" {
		t.Errorf("plugin_id after create = %s, want the configured plugin_01", created.PluginID)
	}

	readResp := resource.ReadResponse{State: stateOf(t, r, created)}
	r.Read(context.Background(), resource.ReadRequest{State: stateOf(t, r, created)}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", readResp.Diagnostics)
	}
	var read PluginVersionResourceModel
	readResp.State.Get(context.Background(), &read)
	if read.PluginID.ValueString() != "plugin_01" {
		t.Errorf("plugin_id after refresh = %s, want plugin_01", read.PluginID)
	}
}

// The admin client's 60-second timeout covers the whole exchange, body
// included. A large upload on a slow link, plus the registration the API
// runs before it answers, can outlast it while the API stores the upload
// anyway; an upload is never resent, so it would be stored but untracked.
// Uploads get a timeout sized for their body; every other call keeps the
// client's, and the shared client is left as it was.
func TestPluginsRequest_uploadOutlastsTheClientTimeout(t *testing.T) {
	t.Parallel()
	const clientTimeout, serverTime = 50 * time.Millisecond, 300 * time.Millisecond
	slowVersion, slowPlugin := ok(pluginVersionJSON), ok(pluginJSON("pluginver_03", false))
	slowVersion.delay, slowPlugin.delay = serverTime, serverTime
	client, _ := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins/plugin_01/versions": {slowVersion},
		"GET /v1/organizations/plugins/plugin_01":           {slowPlugin},
	})
	client.HTTPClient.Timeout = clientTimeout
	src, err := listPluginSource(writePluginDir(t), "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := uploadPluginVersion(context.Background(), client, src, "plugin_01", ""); err != nil {
		t.Errorf("upload: %v, want it to outlast the client's own timeout", err)
	}
	if _, err := getPlugin(context.Background(), client, "plugin_01"); err == nil {
		t.Error("a read must keep the client's timeout")
	}
	if client.HTTPClient.Timeout != clientTimeout {
		t.Errorf("client timeout = %s, want the shared client unchanged", client.HTTPClient.Timeout)
	}
}
