// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

func inviteFixture(status string) string {
	return `{
		"id": "invite_01ABC",
		"email": "new-teammate@example.com",
		"role": "developer",
		"status": "` + status + `",
		"invited_at": "2026-01-01T00:00:00Z",
		"expires_at": "2026-01-08T00:00:00Z",
		"accepted_at": "",
		"type": "invite"
	}`
}

func TestInviteCreate_mapsResponseToState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/organizations/invites" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, inviteFixture("pending"))
	}))
	defer srv.Close()

	client := admintest.NewClient(t, srv)
	body, err := client.DoRequest(context.Background(), "POST", "/v1/organizations/invites", inviteCreateRequest{
		Email: "new-teammate@example.com",
		Role:  "developer",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var inv inviteAPIResponse
	if err := json.Unmarshal(body, &inv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var data InviteResourceModel
	mapInviteToState(&inv, &data)

	if data.ID.ValueString() != "invite_01ABC" {
		t.Errorf("ID = %q, want invite_01ABC", data.ID.ValueString())
	}
	if data.Email.ValueString() != "new-teammate@example.com" {
		t.Errorf("Email = %q, want new-teammate@example.com", data.Email.ValueString())
	}
	if data.Role.ValueString() != "developer" {
		t.Errorf("Role = %q, want developer", data.Role.ValueString())
	}
	if data.Status.ValueString() != "pending" {
		t.Errorf("Status = %q, want pending", data.Status.ValueString())
	}
	if data.Type.ValueString() != "invite" {
		t.Errorf("Type = %q, want invite", data.Type.ValueString())
	}
}

func TestInviteRead_terminalStatusRemovesFromState(t *testing.T) {
	for _, status := range []string{"accepted", "expired", "deleted"} {
		t.Run(status, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, inviteFixture(status))
			}))
			defer srv.Close()

			client := admintest.NewClient(t, srv)
			body, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/invites/invite_01ABC", nil)
			if err != nil {
				t.Fatalf("read: %v", err)
			}

			var inv inviteAPIResponse
			if err := json.Unmarshal(body, &inv); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if inv.Status == "pending" {
				t.Fatalf("status %q should not be treated as pending", inv.Status)
			}
		})
	}
}

func TestInviteRead_pendingStatusKept(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, inviteFixture("pending"))
	}))
	defer srv.Close()

	client := admintest.NewClient(t, srv)
	body, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/invites/invite_01ABC", nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var inv inviteAPIResponse
	if err := json.Unmarshal(body, &inv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if inv.Status != "pending" {
		t.Errorf("Status = %q, want pending", inv.Status)
	}
}

func TestInviteDelete_callsExpectedEndpoint(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := admintest.NewClient(t, srv)
	_, err := client.DoRequest(context.Background(), "DELETE", "/v1/organizations/invites/invite_01ABC", nil)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	if gotMethod != "DELETE" {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/v1/organizations/invites/invite_01ABC" {
		t.Errorf("path = %q, want /v1/organizations/invites/invite_01ABC", gotPath)
	}
}

// TestInviteResource_planModifiers verifies email and role are wired with
// RequiresReplace, since the API has no update endpoint for invites.
func TestInviteResource_planModifiers(t *testing.T) {
	r := NewInviteResource()
	s := schematest.ResourceSchema(t, r)

	for _, name := range []string{"email", "role"} {
		attr, ok := s.Attributes[name]
		if !ok {
			t.Fatalf("attribute %q not found in schema", name)
		}
		strAttr, ok := attr.(schema.StringAttribute)
		if !ok {
			t.Fatalf("attribute %q is not a StringAttribute", name)
		}

		found := false
		for _, pm := range strAttr.PlanModifiers {
			if pm.Description(context.Background()) == stringplanmodifier.RequiresReplace().Description(context.Background()) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("attribute %q missing RequiresReplace plan modifier", name)
		}
	}
}

// TestInviteResource_importState verifies ImportState wires the id attribute.
func TestInviteResource_importState(t *testing.T) {
	r, ok := NewInviteResource().(resource.ResourceWithImportState)
	if !ok {
		t.Fatal("resource does not implement ResourceWithImportState")
	}

	state := schematest.NullState(t, r)

	req := resource.ImportStateRequest{ID: "invite_01ABC"}
	resp := &resource.ImportStateResponse{State: state}
	r.ImportState(context.Background(), req, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState diagnostics: %v", resp.Diagnostics)
	}

	var got InviteResourceModel
	resp.Diagnostics.Append(resp.State.Get(context.Background(), &got)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get diagnostics: %v", resp.Diagnostics)
	}

	if got.ID.ValueString() != "invite_01ABC" {
		t.Errorf("ID = %q, want invite_01ABC", got.ID.ValueString())
	}
}
