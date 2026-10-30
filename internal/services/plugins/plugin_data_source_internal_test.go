// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestPluginDataSource_Read(t *testing.T) {
	t.Parallel()
	client, requests := fakePluginsAPI(t, map[string][]fakeResponse{
		"GET /v1/organizations/plugins/plugin_02": {ok(memberPluginJSON)},
	})

	resp := readDataSource(t, &PluginDataSource{client: client}, map[string]tftypes.Value{"id": str("plugin_02")})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	assertPluginsBeta(t, requests())

	if got := stateString(t, resp, path.Root("owner_type")); got.ValueString() != "user" {
		t.Errorf("owner_type = %s", got)
	}
	if got := stateString(t, resp, path.Root("owner_user_id")); got.ValueString() != "user_01" {
		t.Errorf("owner_user_id = %s", got)
	}
	for _, name := range []string{"organization_installation_preference", "display_name", "reach", "manifest_version"} {
		if got := stateString(t, resp, path.Root(name)); !got.IsNull() {
			t.Errorf("%s = %s, want null", name, got)
		}
	}
	var inherited types.Bool
	resp.State.GetAttribute(context.Background(), path.Root("organization_installation_preference_inherited"), &inherited)
	if !inherited.IsNull() {
		t.Errorf("organization_installation_preference_inherited = %s, want null for a member-owned plugin", inherited)
	}
	if got := stateString(t, resp, path.Root("created_by").AtName("email_address")); got.ValueString() != "member@example.com" {
		t.Errorf("created_by.email_address = %s", got)
	}
	if !stateList(t, resp, "components").IsNull() {
		t.Error("components must be null when not recorded")
	}
}

func TestPluginDataSource_NotFound(t *testing.T) {
	t.Parallel()
	client, _ := fakePluginsAPI(t, map[string][]fakeResponse{
		"GET /v1/organizations/plugins/plugin_09": {apiError(http.StatusNotFound, "not_found_error", "not found", "", nil)},
	})

	resp := readDataSource(t, &PluginDataSource{client: client}, map[string]tftypes.Value{"id": str("plugin_09")})
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Plugin Not Found" {
		t.Errorf("diagnostics = %v", resp.Diagnostics)
	}
}
