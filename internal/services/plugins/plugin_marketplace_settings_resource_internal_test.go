// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

func marketplaceJSON(defaultPreference string) string {
	return fmt.Sprintf(`{
		"type": "plugin_marketplace",
		"id": "marketplace_01",
		"name": "engineering-tools",
		"owner": {"type": "organization"},
		"source": "github",
		"sync_status": "success",
		"last_sync_ended_at": "2026-09-10T22:15:03Z",
		"last_sync_read_sha": "9fceb02d0ae598e95dc970b74767f19372d61af8",
		"default_installation_preference": %q,
		"created_at": "2026-06-12T08:45:00Z"
	}`, defaultPreference)
}

func marketplaceSettingsModel(preference string) PluginMarketplaceSettingsResourceModel {
	return PluginMarketplaceSettingsResourceModel{
		MarketplaceID:                 types.StringValue("marketplace_01"),
		DefaultInstallationPreference: types.StringValue(preference),
	}
}

func TestPluginMarketplaceSettingsResource_CreateAdoptsTheMarketplace(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugin_marketplaces/marketplace_01": {ok(marketplaceJSON("available"))},
	})
	r := &PluginMarketplaceSettingsResource{client: client}

	resp := resource.CreateResponse{State: emptyState(t, r)}
	r.Create(context.Background(), resource.CreateRequest{Plan: planOf(t, r, marketplaceSettingsModel("available"))}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}
	reqs := requests()
	assertPluginsBeta(t, reqs)
	if len(reqs) != 1 || string(reqs[0].Body) != `{"default_installation_preference":"available"}` {
		t.Errorf("requests = %+v, want the one default write", reqs)
	}

	var got PluginMarketplaceSettingsResourceModel
	resp.State.Get(context.Background(), &got)
	if got.ID.ValueString() != "marketplace_01" || got.Name.ValueString() != "engineering-tools" || got.Source.ValueString() != "github" {
		t.Errorf("state = %+v", got)
	}
}

func TestPluginMarketplaceSettingsResource_ReadShowsDrift(t *testing.T) {
	t.Parallel()
	client, _ := fakePluginsAPI(t, map[string][]fakeResponse{
		"GET /v1/organizations/plugin_marketplaces/marketplace_01": {ok(marketplaceJSON("not_available"))},
	})
	r := &PluginMarketplaceSettingsResource{client: client}
	prior := marketplaceSettingsModel("available")

	resp := resource.ReadResponse{State: stateOf(t, r, prior)}
	r.Read(context.Background(), resource.ReadRequest{State: stateOf(t, r, prior)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	var got PluginMarketplaceSettingsResourceModel
	resp.State.Get(context.Background(), &got)
	if got.DefaultInstallationPreference.ValueString() != "not_available" {
		t.Errorf("default = %s, want the value changed in claude.ai", got.DefaultInstallationPreference)
	}
}

func TestPluginMarketplaceSettingsResource_ReadRemovesADeletedMarketplace(t *testing.T) {
	t.Parallel()
	client, _ := fakePluginsAPI(t, map[string][]fakeResponse{
		"GET /v1/organizations/plugin_marketplaces/marketplace_01": {apiError(http.StatusNotFound, "not_found_error", "not found", "", nil)},
	})
	r := &PluginMarketplaceSettingsResource{client: client}
	prior := marketplaceSettingsModel("available")

	resp := resource.ReadResponse{State: stateOf(t, r, prior)}
	r.Read(context.Background(), resource.ReadRequest{State: stateOf(t, r, prior)}, &resp)
	if resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
		t.Errorf("diagnostics = %v, removed = %v", resp.Diagnostics, resp.State.Raw.IsNull())
	}
}

func TestPluginMarketplaceSettingsResource_Update(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"POST /v1/organizations/plugin_marketplaces/marketplace_01": {ok(marketplaceJSON("required"))},
	})
	r := &PluginMarketplaceSettingsResource{client: client}
	prior := marketplaceSettingsModel("available")

	resp := resource.UpdateResponse{State: stateOf(t, r, prior)}
	r.Update(context.Background(), resource.UpdateRequest{Plan: planOf(t, r, marketplaceSettingsModel("required")), State: stateOf(t, r, prior)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}
	if body := string(requests()[0].Body); body != `{"default_installation_preference":"required"}` {
		t.Errorf("body = %s", body)
	}
}

func TestPluginMarketplaceSettingsResource_DeleteOnlyForgets(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, nil)
	r := &PluginMarketplaceSettingsResource{client: client}

	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: stateOf(t, r, marketplaceSettingsModel("available"))}, &resp)
	if resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 1 {
		t.Errorf("diagnostics = %v, want one warning", resp.Diagnostics)
	}
	if n := len(requests()); n != 0 {
		t.Errorf("requests = %d, want none", n)
	}
}

func TestPluginMarketplaceSettingsResource_ImportState(t *testing.T) {
	t.Parallel()
	r := &PluginMarketplaceSettingsResource{}
	resp := resource.ImportStateResponse{State: schematest.NullState(t, r)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "marketplace_01"}, &resp)
	var id types.String
	resp.State.GetAttribute(context.Background(), path.Root("marketplace_id"), &id)
	if resp.Diagnostics.HasError() || id.ValueString() != "marketplace_01" {
		t.Errorf("diagnostics = %v, marketplace_id = %s", resp.Diagnostics, id)
	}
}
