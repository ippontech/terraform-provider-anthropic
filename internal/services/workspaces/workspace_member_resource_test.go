// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/ippontech/terraform-provider-anthropic/internal/acctest"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

// ============================================================================
// Unit tests (no TF_ACC needed)
// ============================================================================

func newTestAdminClientForMember(t *testing.T, srv *httptest.Server) *admin.Client {
	t.Helper()
	return &admin.Client{
		ApiKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	}
}

func workspaceMemberFixture() string {
	return `{
		"user_id": "user_01xyz789",
		"workspace_role": "workspace_user",
		"type": "workspace_member"
	}`
}

func TestWorkspaceMemberCreate_parsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/organizations/workspaces/ws_01abc123/members" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, workspaceMemberFixture())
	}))
	defer srv.Close()

	client := newTestAdminClientForMember(t, srv)
	body, err := client.DoRequest(context.Background(), "POST", "/v1/organizations/workspaces/ws_01abc123/members",
		map[string]string{"user_id": "user_01xyz789", "workspace_role": "workspace_user"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var resp struct {
		UserID        string `json:"user_id"`
		WorkspaceRole string `json:"workspace_role"`
		Type          string `json:"type"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.UserID != "user_01xyz789" {
		t.Errorf("UserID = %q, want %q", resp.UserID, "user_01xyz789")
	}
	if resp.WorkspaceRole != "workspace_user" {
		t.Errorf("WorkspaceRole = %q, want %q", resp.WorkspaceRole, "workspace_user")
	}
	if resp.Type != "workspace_member" {
		t.Errorf("Type = %q, want %q", resp.Type, "workspace_member")
	}
}

func TestWorkspaceMemberRead_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"workspace member not found"}}`)
	}))
	defer srv.Close()

	client := newTestAdminClientForMember(t, srv)
	_, err := client.DoRequest(context.Background(), "GET",
		"/v1/organizations/workspaces/ws_01abc123/members/user_missing", nil)
	if !admin.IsNotFound(err) {
		t.Fatalf("expected IsNotFound, got: %v", err)
	}
}

// ============================================================================
// Acceptance tests (require TF_ACC=1 and real API credentials)
// ============================================================================

func TestAccWorkspaceMemberResource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccWorkspaceMemberConfig("workspace_user"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_workspace_member.test", "workspace_role", "workspace_user"),
					resource.TestCheckResourceAttr("anthropic_workspace_member.test", "type", "workspace_member"),
					resource.TestCheckResourceAttrSet("anthropic_workspace_member.test", "id"),
				),
			},
			{
				ResourceName:      "anthropic_workspace_member.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccWorkspaceMemberResource_updateRole(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccWorkspaceMemberConfig("workspace_user"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_workspace_member.test", "workspace_role", "workspace_user"),
				),
			},
			{
				Config: testAccWorkspaceMemberConfig("workspace_admin"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_workspace_member.test", "workspace_role", "workspace_admin"),
				),
			},
		},
	})
}

func TestAccWorkspaceMemberResource_rejectsBillingOnCreate(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      testAccWorkspaceMemberConfig("workspace_billing"),
				ExpectError: regexp.MustCompile(`workspace_billing`),
			},
		},
	})
}

func TestAccWorkspaceMemberResource_rejectsBillingOnUpdate(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccWorkspaceMemberConfig("workspace_user"),
			},
			{
				Config:      testAccWorkspaceMemberConfig("workspace_billing"),
				ExpectError: regexp.MustCompile(`workspace_billing`),
			},
		},
	})
}

func testAccWorkspaceMemberConfig(role string) string {
	return `
resource "anthropic_workspace" "test" {
  name = "tf-acc-workspace-member-test"
}

resource "anthropic_workspace_member" "test" {
  workspace_id   = anthropic_workspace.test.id
  user_id        = "user_01test"
  workspace_role = "` + role + `"
}
`
}
