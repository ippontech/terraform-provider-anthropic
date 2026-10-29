// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

func installationSettingJSON(target, preference string) string {
	targetJSON := `{"type": "organization"}`
	if target != "organization" {
		targetJSON = fmt.Sprintf(`{"type": "rbac_group", "rbac_group_id": %q}`, target)
	}
	return fmt.Sprintf(`{
		"type": "plugin_installation_setting",
		"plugin_id": "plugin_01",
		"target": %s,
		"installation_preference": %q,
		"created_at": "2026-09-02T10:00:00Z",
		"updated_at": "2026-09-03T10:00:00Z"
	}`, targetJSON, preference)
}

func installationSettingModel(target, preference string) PluginInstallationSettingResourceModel {
	return PluginInstallationSettingResourceModel{
		PluginID:               types.StringValue("plugin_01"),
		Target:                 types.StringValue(target),
		InstallationPreference: types.StringValue(preference),
	}
}

func TestPluginInstallationSettingResource_Create(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"organization", "rbac_group_01F3xQ"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
				"POST /v1/organizations/plugins/plugin_01/installation_settings/" + target: {ok(installationSettingJSON(target, "auto_install"))},
			})
			r := &PluginInstallationSettingResource{client: client}

			resp := resource.CreateResponse{State: emptyState(t, r)}
			r.Create(context.Background(), resource.CreateRequest{Plan: planOf(t, r, installationSettingModel(target, "auto_install"))}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Create: %v", resp.Diagnostics)
			}
			reqs := requests()
			assertPluginsBeta(t, reqs)
			if string(reqs[0].Body) != `{"installation_preference":"auto_install"}` {
				t.Errorf("body = %s", reqs[0].Body)
			}

			var got PluginInstallationSettingResourceModel
			resp.State.Get(context.Background(), &got)
			wantType := map[string]string{"organization": "organization", "rbac_group_01F3xQ": "rbac_group"}[target]
			if got.ID.ValueString() != "plugin_01:"+target || got.Target.ValueString() != target || got.TargetType.ValueString() != wantType {
				t.Errorf("state = %+v", got)
			}
			if got.CreatedAt.ValueString() != "2026-09-02T10:00:00Z" || got.UpdatedAt.ValueString() != "2026-09-03T10:00:00Z" {
				t.Errorf("timestamps = %s / %s", got.CreatedAt, got.UpdatedAt)
			}
		})
	}
}

func TestListPluginsPages_StopsOnARepeatedOrEndlessCursor(t *testing.T) {
	t.Parallel()
	const path = "/v1/organizations/plugins/plugin_01/installation_settings"
	for name, tc := range map[string]struct {
		pages    []fakeResponse
		maxPages int
		wantErr  string
		wantReqs int
	}{
		"the cursor just sent": {
			pages:    []fakeResponse{ok(`{"data": [], "next_page": "page_2"}`)},
			maxPages: maxPluginsListPages, wantErr: `cursor "page_2" again`, wantReqs: 2,
		},
		"a cycle of cursors": {
			pages: []fakeResponse{
				ok(`{"data": [], "next_page": "page_2"}`), ok(`{"data": [], "next_page": "page_3"}`),
				ok(`{"data": [], "next_page": "page_2"}`),
			},
			maxPages: maxPluginsListPages, wantErr: `cursor "page_2" again`, wantReqs: 3,
		},
		"more pages than the bound": {
			pages: []fakeResponse{
				ok(`{"data": [], "next_page": "page_2"}`), ok(`{"data": [], "next_page": "page_3"}`),
				ok(`{"data": [], "next_page": "page_4"}`),
			},
			maxPages: 2, wantErr: "after 2 pages", wantReqs: 2,
		},
		"the last page": {
			pages:    []fakeResponse{ok(`{"data": [], "next_page": "page_2"}`), ok(`{"data": [], "next_page": null}`)},
			maxPages: 2, wantReqs: 2,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, requests := fakePluginsAPI(t, map[string][]fakeResponse{"GET " + path: tc.pages})
			err := listPluginsPagesUpTo(context.Background(), client, path, nil, 100, tc.maxPages,
				func(pluginInstallationSettingAPIObject) bool { return true })
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("err = %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
			}
			if n := len(requests()); n != tc.wantReqs {
				t.Errorf("requests = %d, want %d", n, tc.wantReqs)
			}
		})
	}
}

func TestPluginInstallationSettingResource_ReadPagesTheSettings(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"GET /v1/organizations/plugins/plugin_01/installation_settings": {
			ok(`{"data": [` + installationSettingJSON("rbac_group_other", "available") + `], "next_page": "page_2"}`),
			ok(`{"data": [], "next_page": "page_3"}`),
			ok(`{"data": [` + installationSettingJSON("rbac_group_01", "not_available") + `], "next_page": null}`),
		},
	})
	r := &PluginInstallationSettingResource{client: client}
	prior := installationSettingModel("rbac_group_01", "auto_install")
	prior.ID = types.StringValue("plugin_01:rbac_group_01")

	resp := resource.ReadResponse{State: stateOf(t, r, prior)}
	r.Read(context.Background(), resource.ReadRequest{State: stateOf(t, r, prior)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}

	reqs := requests()
	assertPluginsBeta(t, reqs)
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want three pages (an empty page with a cursor does not end the list)", len(reqs))
	}
	if reqs[0].Query.Get("target_type") != "rbac_group" || reqs[0].Query.Get("limit") != "100" || reqs[0].Query.Has("page") {
		t.Errorf("first page query = %v", reqs[0].Query)
	}
	if reqs[1].Query.Get("page") != "page_2" || reqs[2].Query.Get("page") != "page_3" || reqs[2].Query.Get("target_type") != "rbac_group" {
		t.Errorf("next page queries = %v / %v", reqs[1].Query, reqs[2].Query)
	}

	var got PluginInstallationSettingResourceModel
	resp.State.Get(context.Background(), &got)
	if got.InstallationPreference.ValueString() != "not_available" {
		t.Errorf("installation_preference = %s, want the value changed out of band", got.InstallationPreference)
	}
}

func TestPluginInstallationSettingResource_ReadRemovesAMissingSetting(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]fakeResponse{
		"setting removed": ok(`{"data": [], "next_page": null}`),
		"plugin deleted":  apiError(http.StatusNotFound, "not_found_error", "not found", "", nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
				"GET /v1/organizations/plugins/plugin_01/installation_settings": {answer},
			})
			r := &PluginInstallationSettingResource{client: client}
			prior := installationSettingModel("organization", "required")

			resp := resource.ReadResponse{State: stateOf(t, r, prior)}
			r.Read(context.Background(), resource.ReadRequest{State: stateOf(t, r, prior)}, &resp)
			if resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
				t.Errorf("diagnostics = %v, removed = %v", resp.Diagnostics, resp.State.Raw.IsNull())
			}
			if q := requests()[0].Query; q.Get("target_type") != "organization" {
				t.Errorf("query = %v, want target_type=organization", q)
			}
		})
	}
}

func TestPluginInstallationSettingResource_Update(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins/plugin_01/installation_settings/organization": {ok(installationSettingJSON("organization", "required"))},
	})
	r := &PluginInstallationSettingResource{client: client}
	prior := installationSettingModel("organization", "available")
	plan := installationSettingModel("organization", "required")

	resp := resource.UpdateResponse{State: stateOf(t, r, prior)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: planOf(t, r, plan), State: stateOf(t, r, prior)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}
	if body := string(requests()[0].Body); body != `{"installation_preference":"required"}` {
		t.Errorf("body = %s", body)
	}
}

func TestPluginInstallationSettingResource_Delete(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]fakeResponse{
		"removed":      ok(`{"type":"plugin_installation_setting_deleted","plugin_id":"plugin_01","target":{"type":"organization"}}`),
		"already gone": apiError(http.StatusNotFound, "not_found_error", "not found", "", nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
				"DELETE /v1/organizations/plugins/plugin_01/installation_settings/organization": {answer},
			})
			r := &PluginInstallationSettingResource{client: client}

			var resp resource.DeleteResponse
			r.Delete(context.Background(), resource.DeleteRequest{State: stateOf(t, r, installationSettingModel("organization", "required"))}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Delete: %v", resp.Diagnostics)
			}
			assertPluginsBeta(t, requests())
		})
	}
}

func TestPluginInstallationSettingResource_WriteIsResentWhenTheAPIAsks(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugins/plugin_01/installation_settings/organization": {
			apiError(http.StatusServiceUnavailable, "overloaded_error", "concurrent writes", "", map[string]string{"x-should-retry": "true"}),
			ok(installationSettingJSON("organization", "required")),
		},
	})
	if _, err := setPluginInstallationSetting(context.Background(), client, "plugin_01", "organization", "required"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if n := len(requests()); n != 2 {
		t.Errorf("requests = %d, want the 503 with x-should-retry: true resent once", n)
	}
}

// TestPluginInstallationSettingWrites_areSerializedPerPlugin checks that two
// settings of one plugin, applied in parallel by Terraform, never reach the
// API at the same time, as the API asks.
func TestPluginInstallationSettingWrites_areSerializedPerPlugin(t *testing.T) {
	t.Parallel()
	var inFlight, maxInFlight atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		_, _ = io.WriteString(w, installationSettingJSON("organization", "required"))
	}))
	defer srv.Close()
	client := admintest.NewClient(t, srv)

	// A plugin ID of its own, so no other test shares its lock.
	const pluginID = "plugin_serialized"
	var wg sync.WaitGroup
	for _, target := range []string{"organization", "rbac_group_a", "rbac_group_b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := setPluginInstallationSetting(context.Background(), client, pluginID, target, "required"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := removePluginInstallationSetting(context.Background(), client, pluginID, "rbac_group_c"); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()

	if maxInFlight.Load() != 1 {
		t.Errorf("max concurrent writes for one plugin = %d, want 1", maxInFlight.Load())
	}
}

func TestPluginInstallationSettingResource_ImportState(t *testing.T) {
	t.Parallel()
	r := &PluginInstallationSettingResource{}
	for id, wantErr := range map[string]bool{
		"plugin_01:organization":  false,
		"plugin_01:rbac_group_01": false,
		"plugin_01":               true,
		"plugin_01:user_01":       true,
		":organization":           true,
	} {
		resp := resource.ImportStateResponse{State: schematest.NullState(t, r)}
		r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
		if resp.Diagnostics.HasError() != wantErr {
			t.Errorf("import %q: diagnostics = %v, want error = %v", id, resp.Diagnostics, wantErr)
			continue
		}
		if wantErr {
			continue
		}
		var target types.String
		resp.State.GetAttribute(context.Background(), path.Root("target"), &target)
		if "plugin_01:"+target.ValueString() != id {
			t.Errorf("import %q: target = %s", id, target)
		}
	}
}
