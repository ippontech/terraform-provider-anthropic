// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package rbac

import (
	"context"
	"net/http"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
)

func TestRBACGroupsDataSource_Read_paginates(t *testing.T) {
	t.Parallel()

	srv, rec := stub(t, func(req recordedRequest) (int, string) {
		if req.Query["page"] == nil {
			return http.StatusOK, `{"data":[` + rbacGroupFixture + `],"has_more":true,"next_page":"cur_2"}`
		}
		return http.StatusOK, `{"data":[{"id":"rbac_group_02","name":"Sales","source_type":"scim","role_ids":null,` +
			`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}],"has_more":false,"next_page":null}`
	})
	d := &RBACGroupsDataSource{client: admintest.NewClient(t, srv)}

	resp := runDataSourceRead(t, d, nil)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}

	reqs := rec.all()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	for _, req := range reqs {
		if req.Path != "/v1/organizations/rbac_groups" {
			t.Errorf("path = %s", req.Path)
		}
		assertBetaQuery(t, req)
	}

	var got RBACGroupsDataSourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	var groups []rbacGroupModel
	if diags := got.RBACGroups.ElementsAs(context.Background(), &groups, false); diags.HasError() {
		t.Fatalf("groups: %v", diags)
	}
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if groups[0].ID.ValueString() != rbacGroupID || len(roleIDsOf(t, groups[0])) != 1 {
		t.Errorf("first group = %+v", groups[0])
	}
	if groups[1].SourceType.ValueString() != "scim" || !groups[1].RoleIDs.IsNull() {
		t.Errorf("second group = %+v, want scim with null role_ids", groups[1])
	}
}

func TestRBACGroupsDataSource_Read_emptyIsNotNull(t *testing.T) {
	t.Parallel()

	srv, _ := stub(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"data":[],"has_more":false,"next_page":null}`
	})
	d := &RBACGroupsDataSource{client: admintest.NewClient(t, srv)}

	resp := runDataSourceRead(t, d, nil)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	var got RBACGroupsDataSourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if got.RBACGroups.IsNull() || len(got.RBACGroups.Elements()) != 0 {
		t.Errorf("rbac_groups = %v, want an empty list", got.RBACGroups)
	}
}

func TestRBACGroupsDataSource_Read_consoleOrganizationError(t *testing.T) {
	t.Parallel()

	srv, _ := stub(t, func(recordedRequest) (int, string) {
		return http.StatusBadRequest, `{"type":"error","error":{"type":"invalid_request_error","message":"The RBAC Groups API is available to Claude Enterprise organizations only."}}`
	})
	d := &RBACGroupsDataSource{client: admintest.NewClient(t, srv)}

	resp := runDataSourceRead(t, d, nil)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected a diagnostic")
	}
}
