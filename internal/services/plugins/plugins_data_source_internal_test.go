// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestPluginsDataSource_FiltersAndPages(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"GET /v1/organizations/plugins": {
			ok(`{"data": [` + pluginJSON("pluginver_02", false) + `], "next_page": "page_2"}`),
			ok(`{"data": [` + memberPluginJSON + `], "next_page": null}`),
		},
	})

	resp := readDataSource(t, &PluginsDataSource{client: client}, map[string]tftypes.Value{
		"owner_type":     str("organization"),
		"owner_user_id":  str("user_01"),
		"marketplace_id": str("marketplace_01"),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}

	reqs := requests()
	assertPluginsBeta(t, reqs)
	q := reqs[0].Query
	if q.Get("owner_type") != "organization" || q.Get("owner_user_id") != "user_01" ||
		q.Get("marketplace_id") != "marketplace_01" || q.Get("limit") != "100" {
		t.Errorf("query = %v", q)
	}
	if len(reqs) != 2 || reqs[1].Query.Get("page") != "page_2" || reqs[1].Query.Get("owner_type") != "organization" {
		t.Errorf("pages = %d, second query = %v", len(reqs), reqs[len(reqs)-1].Query)
	}

	plugins := stateList(t, resp, "plugins")
	if len(plugins.Elements()) != 2 {
		t.Fatalf("plugins = %d, want 2", len(plugins.Elements()))
	}
	if got := stateString(t, resp, path.Root("plugins").AtListIndex(0).AtName("reach")); got.ValueString() != "remote" {
		t.Errorf("plugins[0].reach = %s", got)
	}
	if got := stateString(t, resp, path.Root("plugins").AtListIndex(1).AtName("owner_type")); got.ValueString() != "user" {
		t.Errorf("plugins[1].owner_type = %s", got)
	}
}

func TestPluginsDataSource_EmptyIsAnEmptyList(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"GET /v1/organizations/plugins": {ok(`{"data": [], "next_page": null}`)},
	})

	resp := readDataSource(t, &PluginsDataSource{client: client}, nil)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	if q := requests()[0].Query; q.Has("owner_type") || q.Has("owner_user_id") || q.Has("marketplace_id") {
		t.Errorf("query = %v, want no filter when none is configured", q)
	}
	if plugins := stateList(t, resp, "plugins"); plugins.IsNull() || len(plugins.Elements()) != 0 {
		t.Errorf("plugins = %v, want an empty list", plugins)
	}
}
