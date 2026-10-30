// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestPluginMarketplaceDataSource_Read(t *testing.T) {
	t.Parallel()
	manual := `{"type": "plugin_marketplace", "id": "marketplace_02", "name": "library", "owner": {"type": "organization"},
		"source": "manual", "sync_status": null, "last_sync_ended_at": null, "last_sync_read_sha": null,
		"default_installation_preference": "not_available", "created_at": "2026-06-12T08:45:00Z"}`
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"GET /v1/organizations/plugin_marketplaces/marketplace_02": {ok(manual)},
		"GET /v1/organizations/plugin_marketplaces/marketplace_09": {apiError(http.StatusNotFound, "not_found_error", "not found", "", nil)},
	})
	d := &PluginMarketplaceDataSource{client: client}

	resp := readDataSource(t, d, map[string]tftypes.Value{"id": str("marketplace_02")})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	assertPluginsBeta(t, requests())
	if got := stateString(t, resp, path.Root("source")); got.ValueString() != "manual" {
		t.Errorf("source = %s", got)
	}
	for _, name := range []string{"sync_status", "last_sync_ended_at", "last_sync_read_sha", "owner_user_id"} {
		if got := stateString(t, resp, path.Root(name)); !got.IsNull() {
			t.Errorf("%s = %s, want null for a manual organization marketplace", name, got)
		}
	}

	resp = readDataSource(t, d, map[string]tftypes.Value{"id": str("marketplace_09")})
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Plugin Marketplace Not Found" {
		t.Errorf("diagnostics = %v", resp.Diagnostics)
	}
}
