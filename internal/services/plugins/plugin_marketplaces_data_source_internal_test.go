// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestPluginMarketplacesDataSource_FiltersAndPages(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"GET /v1/organizations/plugin_marketplaces": {
			ok(`{"data": [], "next_page": "page_2"}`),
			ok(`{"data": [` + marketplaceJSON("available") + `], "next_page": null}`),
		},
	})

	resp := readDataSource(t, &PluginMarketplacesDataSource{client: client}, map[string]tftypes.Value{
		"owner_type": str("organization"),
		"source":     str("github"),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	reqs := requests()
	assertPluginsBeta(t, reqs)
	if q := reqs[0].Query; q.Get("owner_type") != "organization" || q.Get("source") != "github" || q.Get("limit") != "1000" {
		t.Errorf("query = %v", q)
	}
	if len(reqs) != 2 || reqs[1].Query.Get("page") != "page_2" {
		t.Errorf("requests = %d, want the empty first page followed", len(reqs))
	}
	marketplace := path.Root("marketplaces").AtListIndex(0)
	if got := stateString(t, resp, marketplace.AtName("last_sync_read_sha")); got.ValueString() != "9fceb02d0ae598e95dc970b74767f19372d61af8" {
		t.Errorf("last_sync_read_sha = %s", got)
	}
	if got := stateString(t, resp, marketplace.AtName("default_installation_preference")); got.ValueString() != "available" {
		t.Errorf("default_installation_preference = %s", got)
	}
}
