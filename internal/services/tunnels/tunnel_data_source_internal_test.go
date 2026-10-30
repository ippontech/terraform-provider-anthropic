// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

func TestTunnelDataSource_Read(t *testing.T) {
	srv, log := fakeTunnelsAPI(t, map[string]func(http.ResponseWriter){
		"GET /v1/tunnels/tnl_01ABC": respond(http.StatusOK, tunnelArchivedJSON),
	})

	resp := readDataSource(t, &TunnelDataSource{client: oauthtest.NewClient(t, srv)}, map[string]tftypes.Value{
		"id": tftypes.NewValue(tftypes.String, "tnl_01ABC"),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read returned errors: %v", resp.Diagnostics)
	}

	reqs := log.all()
	if len(reqs) != 1 || !strings.Contains(reqs[0].Beta, "mcp-tunnels-2026-06-22") || reqs[0].Auth != "Bearer "+sdktest.TestAuthToken {
		t.Errorf("requests = %+v, want one GET with the tunnels beta header and the bearer token", reqs)
	}

	var data tunnelDSModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if data.DisplayName.ValueString() != "corp-mcp" || data.Domain.ValueString() != "abc123.tunnel.anthropic.com" {
		t.Errorf("display_name/domain = %v/%v", data.DisplayName, data.Domain)
	}
	// Unlike the resource, the data source returns archived tunnels as is.
	if data.ArchivedAt.ValueString() != "2026-07-02T10:00:00Z" {
		t.Errorf("archived_at = %v, want 2026-07-02T10:00:00Z", data.ArchivedAt)
	}
	if n := log.count(http.MethodPost, "/v1/tunnels/tnl_01ABC/reveal_token"); n != 0 {
		t.Errorf("reveal_token called %d times, want 0: the data source never exposes the token", n)
	}
}

func TestTunnelDataSource_Read_404(t *testing.T) {
	srv, _ := fakeTunnelsAPI(t, nil)

	resp := readDataSource(t, &TunnelDataSource{client: oauthtest.NewClient(t, srv)}, map[string]tftypes.Value{
		"id": tftypes.NewValue(tftypes.String, "tnl_missing"),
	})
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostics for a 404 response")
	}
}
