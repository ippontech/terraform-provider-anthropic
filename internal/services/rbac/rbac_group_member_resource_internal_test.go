// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package rbac

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

const rbacUserID = "user_01AbCdEfGhIjKlMnOpQrSt"

// memberJSON is the 2023-06-01 member shape: rbac_group_id and group_id.
func memberJSON(userID, email string) string {
	return `{"type":"rbac_group_member","rbac_group_id":"` + rbacGroupID + `","group_id":"` + rbacGroupID +
		`","user_id":"` + userID + `","email":"` + email + `","created_at":"2026-04-07T12:30:00Z"}`
}

// memberState is the state of a tracked membership.
func memberState() map[string]any {
	return map[string]any{"id": rbacGroupID + ":" + rbacUserID, "rbac_group_id": rbacGroupID, "user_id": rbacUserID}
}

func TestRBACGroupMemberResource_Create(t *testing.T) {
	t.Parallel()

	srv, rec := stub(t, func(recordedRequest) (int, string) {
		return http.StatusOK, memberJSON(rbacUserID, "jane@example.com")
	})
	r := &RBACGroupMemberResource{client: admintest.NewClient(t, srv)}

	plan := resourcePlan(t, r, map[string]any{
		"id":            tftypes.UnknownValue,
		"rbac_group_id": rbacGroupID,
		"user_id":       rbacUserID,
		"email":         tftypes.UnknownValue,
		"created_at":    tftypes.UnknownValue,
	})
	resp := resource.CreateResponse{State: emptyResourceState(t, r)}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	req := rec.all()[0]
	if req.Method != http.MethodPost || req.Path != "/v1/organizations/rbac_groups/"+rbacGroupID+"/members" {
		t.Errorf("request = %s %s", req.Method, req.Path)
	}
	assertBetaQuery(t, req)
	if len(req.Body) != 1 || req.Body["user_id"] != rbacUserID {
		t.Errorf("body = %v, want only user_id", req.Body)
	}

	var got RBACGroupMemberResourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if got.ID.ValueString() != rbacGroupID+":"+rbacUserID {
		t.Errorf("id = %v", got.ID)
	}
	if got.Email.ValueString() != "jane@example.com" || got.CreatedAt.ValueString() != "2026-04-07T12:30:00Z" {
		t.Errorf("email/created_at = %v/%v", got.Email, got.CreatedAt)
	}
}

func TestRBACGroupMemberResource_Read_findsMemberOnALaterPage(t *testing.T) {
	t.Parallel()

	srv, rec := stub(t, func(req recordedRequest) (int, string) {
		switch {
		case req.Query["page"] == nil:
			// A short page with has_more=true: the list filters out users who left.
			return http.StatusOK, `{"data":[` + memberJSON("user_other", "other@example.com") + `],"has_more":true,"next_page":"cur_2"}`
		case req.Query["page"][0] == "cur_2":
			return http.StatusOK, `{"data":[` + memberJSON(rbacUserID, "jane@example.com") + `],"has_more":true,"next_page":"cur_3"}`
		default:
			t.Errorf("Read kept paging after finding the member: page=%v", req.Query["page"])
			return http.StatusOK, `{"data":[],"has_more":false,"next_page":null}`
		}
	})
	r := &RBACGroupMemberResource{client: admintest.NewClient(t, srv)}

	state := resourceState(t, r, memberState())
	resp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}

	reqs := rec.all()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	for _, req := range reqs {
		if req.Method != http.MethodGet || req.Path != "/v1/organizations/rbac_groups/"+rbacGroupID+"/members" {
			t.Errorf("request = %s %s", req.Method, req.Path)
		}
		assertBetaQuery(t, req)
	}

	var got RBACGroupMemberResourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if got.Email.ValueString() != "jane@example.com" {
		t.Errorf("email = %v", got.Email)
	}
}

func TestRBACGroupMemberResource_Read_goneRemovesFromState(t *testing.T) {
	t.Parallel()

	cases := map[string]func(recordedRequest) (int, string){
		"user not in the list": func(recordedRequest) (int, string) {
			return http.StatusOK, `{"data":[` + memberJSON("user_other", "other@example.com") + `],"has_more":false,"next_page":null}`
		},
		"group deleted": func(recordedRequest) (int, string) {
			return http.StatusNotFound, notFoundBody
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv, _ := stub(t, handler)
			r := &RBACGroupMemberResource{client: admintest.NewClient(t, srv)}

			state := resourceState(t, r, memberState())
			resp := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Read: %v", resp.Diagnostics)
			}
			if !resp.State.Raw.IsNull() {
				t.Error("expected the resource to be removed from state")
			}
		})
	}
}

func TestRBACGroupMemberResource_Delete(t *testing.T) {
	t.Parallel()

	for name, status := range map[string]int{"removed": http.StatusOK, "already gone": http.StatusNotFound} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv, rec := stub(t, func(recordedRequest) (int, string) {
				if status == http.StatusNotFound {
					return status, notFoundBody
				}
				return status, `{"type":"rbac_group_member_deleted","rbac_group_id":"` + rbacGroupID + `","user_id":"` + rbacUserID + `"}`
			})
			r := &RBACGroupMemberResource{client: admintest.NewClient(t, srv)}

			state := resourceState(t, r, memberState())
			resp := resource.DeleteResponse{State: state}
			r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Delete: %v", resp.Diagnostics)
			}
			req := rec.all()[0]
			if req.Method != http.MethodDelete || req.Path != "/v1/organizations/rbac_groups/"+rbacGroupID+"/members/"+rbacUserID {
				t.Errorf("request = %s %s", req.Method, req.Path)
			}
			assertBetaQuery(t, req)
		})
	}
}

func TestRBACGroupMemberResource_Delete_reportsOtherErrors(t *testing.T) {
	t.Parallel()

	srv, _ := stub(t, func(recordedRequest) (int, string) {
		return http.StatusBadRequest, `{"type":"error","error":{"type":"invalid_request_error","message":"group is provisioned by SCIM"}}`
	})
	r := &RBACGroupMemberResource{client: admintest.NewClient(t, srv)}

	state := resourceState(t, r, memberState())
	resp := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected a diagnostic for a 400")
	}
}

func TestRBACGroupMemberResource_ImportState(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		r := &RBACGroupMemberResource{}
		resp := resource.ImportStateResponse{State: schematest.NullState(t, r)}
		r.ImportState(context.Background(), resource.ImportStateRequest{ID: rbacGroupID + ":" + rbacUserID}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("ImportState: %v", resp.Diagnostics)
		}
		var got RBACGroupMemberResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("state: %v", diags)
		}
		if got.RBACGroupID.ValueString() != rbacGroupID || got.UserID.ValueString() != rbacUserID || got.ID.ValueString() != rbacGroupID+":"+rbacUserID {
			t.Errorf("imported = %+v", got)
		}
	})

	for _, id := range []string{"", rbacGroupID, rbacGroupID + ":", ":" + rbacUserID} {
		t.Run("invalid "+id, func(t *testing.T) {
			t.Parallel()
			r := &RBACGroupMemberResource{}
			resp := resource.ImportStateResponse{State: schematest.NullState(t, r)}
			r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Errorf("ImportState(%q) should fail", id)
			}
		})
	}
}
