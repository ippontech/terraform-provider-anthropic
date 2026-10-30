// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestPluginVersionsDataSource_Read(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"GET /v1/organizations/plugins/plugin_01/versions": {ok(`{"data": [` + pluginVersionJSON + `], "next_page": null}`)},
	})

	resp := readDataSource(t, &PluginVersionsDataSource{client: client}, map[string]tftypes.Value{"plugin_id": str("plugin_01")})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	reqs := requests()
	assertPluginsBeta(t, reqs)
	if reqs[0].Query.Get("limit") != "1000" {
		t.Errorf("limit = %s, want the versions list's maximum", reqs[0].Query.Get("limit"))
	}
	version := path.Root("versions").AtListIndex(0)
	if got := stateString(t, resp, version.AtName("release_notes")); got.ValueString() != "Adds the call-prep command." {
		t.Errorf("release_notes = %s", got)
	}
	if got := stateString(t, resp, version.AtName("content_scan").AtName("status")); got.ValueString() != "processing" {
		t.Errorf("content_scan.status = %s", got)
	}
	if got := stateString(t, resp, version.AtName("reach")); !got.IsNull() {
		t.Errorf("reach = %s, want null when unclassified", got)
	}
}
