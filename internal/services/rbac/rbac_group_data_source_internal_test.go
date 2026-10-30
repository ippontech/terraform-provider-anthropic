// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package rbac

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
)

func TestRBACGroupDataSource_Read(t *testing.T) {
	t.Parallel()

	// The 2026-10-21 shape (no deprecated roles) must map the same way.
	srv, rec := stub(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"type":"rbac_group","id":"` + rbacGroupID + `","name":"SCIM Engineers","source_type":"scim",` +
			`"role_ids":["rbac_role_a","rbac_role_b"],"created_at":"2026-03-18T10:01:42Z","updated_at":"2026-05-02T08:55:09Z"}`
	})
	d := &RBACGroupDataSource{client: admintest.NewClient(t, srv)}

	resp := runDataSourceRead(t, d, map[string]tftypes.Value{"id": tftypes.NewValue(tftypes.String, rbacGroupID)})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}

	req := rec.all()[0]
	if req.Method != http.MethodGet || req.Path != "/v1/organizations/rbac_groups/"+rbacGroupID {
		t.Errorf("request = %s %s", req.Method, req.Path)
	}
	assertBetaQuery(t, req)

	var got rbacGroupModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if got.Name.ValueString() != "SCIM Engineers" || got.SourceType.ValueString() != "scim" {
		t.Errorf("name/source_type = %v/%v", got.Name, got.SourceType)
	}
	if ids := roleIDsOf(t, got); len(ids) != 2 {
		t.Errorf("role_ids = %v, want 2", ids)
	}
	if got.UpdatedAt.ValueString() != "2026-05-02T08:55:09Z" {
		t.Errorf("updated_at = %v", got.UpdatedAt)
	}
}

func TestRBACGroupDataSource_Read_notFound(t *testing.T) {
	t.Parallel()

	srv, _ := stub(t, func(recordedRequest) (int, string) { return http.StatusNotFound, notFoundBody })
	d := &RBACGroupDataSource{client: admintest.NewClient(t, srv)}

	resp := runDataSourceRead(t, d, map[string]tftypes.Value{"id": tftypes.NewValue(tftypes.String, "rbac_group_missing")})
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected a diagnostic for a 404")
	}
	if got := resp.Diagnostics.Errors()[0].Summary(); got != "RBAC Group Not Found" {
		t.Errorf("summary = %q", got)
	}
}
