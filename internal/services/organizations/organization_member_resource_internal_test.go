// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Live create/destroy CRUD acceptance tests for anthropic_organization_member
// are intentionally not written: removing an org member is irreversible and
// there is no dedicated test organization yet (Admin API resource-test
// blocker tracked in #58, see CLAUDE.md). Coverage here is httptest-based
// unit tests only.
package organizations

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

const organizationMemberResourceFixture = `{
	"id": "user_01WCz1FkmYMm4gnmykNKUu3Q",
	"email": "user@example.com",
	"name": "Example User",
	"role": "developer",
	"added_at": "2026-01-01T00:00:00Z",
	"type": "user"
}`

// --- Create always errors ---

func TestOrganizationMemberResource_CreateErrors(t *testing.T) {
	t.Parallel()

	r := &OrganizationMemberResource{}
	var resp resource.CreateResponse
	r.Create(context.Background(), resource.CreateRequest{}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected Create to return an error diagnostic")
	}
}

// --- Read ---

func TestOrganizationMemberResource_Read(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotMethod = req.Method
		gotPath = req.URL.Path
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, organizationMemberResourceFixture)
	}))
	defer srv.Close()

	r := &OrganizationMemberResource{client: admintest.NewClient(t, srv)}

	schema := schematest.ResourceSchema(t, r)
	objType := schematest.ResourceObjectType(t, r)
	vals := schematest.NullValues(t, r)
	vals["id"] = tftypes.NewValue(objType.AttributeTypes["id"], "user_01WCz1FkmYMm4gnmykNKUu3Q")

	state := tfsdk.State{
		Raw:    tftypes.NewValue(objType, vals),
		Schema: schema,
	}

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schema}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read returned errors: %v", resp.Diagnostics)
	}

	if gotMethod != http.MethodGet {
		t.Errorf("expected GET, got %s", gotMethod)
	}
	if gotPath != "/v1/organizations/users/user_01WCz1FkmYMm4gnmykNKUu3Q" {
		t.Errorf("expected users read path, got %s", gotPath)
	}

	var data OrganizationMemberResourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("failed to read state: %v", diags)
	}
	if data.Email.ValueString() != "user@example.com" {
		t.Errorf("expected email to be mapped from response, got %s", data.Email.ValueString())
	}
	if data.Role.ValueString() != "developer" {
		t.Errorf("expected role to be mapped from response, got %s", data.Role.ValueString())
	}
}

func TestOrganizationMemberResource_Read_notFoundRemovesFromState(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"user not found"}}`)
	}))
	defer srv.Close()

	r := &OrganizationMemberResource{client: admintest.NewClient(t, srv)}

	schema := schematest.ResourceSchema(t, r)
	objType := schematest.ResourceObjectType(t, r)
	vals := schematest.NullValues(t, r)
	vals["id"] = tftypes.NewValue(objType.AttributeTypes["id"], "user_01WCz1FkmYMm4gnmykNKUu3Q")

	state := tfsdk.State{
		Raw:    tftypes.NewValue(objType, vals),
		Schema: schema,
	}

	var resp resource.ReadResponse
	resp.State = state
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read returned errors: %v", resp.Diagnostics)
	}

	if !resp.State.Raw.IsNull() {
		t.Error("expected state to be removed (null) after a 404, but it was not")
	}
}

// --- Update ---

func TestOrganizationMemberResource_Update(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotMethod = req.Method
		gotPath = req.URL.Path
		_ = json.NewDecoder(req.Body).Decode(&gotBody)
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, organizationMemberResourceFixture)
	}))
	defer srv.Close()

	r := &OrganizationMemberResource{client: admintest.NewClient(t, srv)}

	schema := schematest.ResourceSchema(t, r)
	objType := schematest.ResourceObjectType(t, r)
	vals := schematest.NullValues(t, r)
	vals["id"] = tftypes.NewValue(objType.AttributeTypes["id"], "user_01WCz1FkmYMm4gnmykNKUu3Q")
	vals["role"] = tftypes.NewValue(objType.AttributeTypes["role"], "developer")

	plan := tfsdk.Plan{
		Raw:    tftypes.NewValue(objType, vals),
		Schema: schema,
	}

	var resp resource.UpdateResponse
	resp.State = tfsdk.State{Schema: schema}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update returned errors: %v", resp.Diagnostics)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/v1/organizations/users/user_01WCz1FkmYMm4gnmykNKUu3Q" {
		t.Errorf("expected users update path, got %s", gotPath)
	}
	if gotBody["role"] != "developer" {
		t.Errorf("expected role 'developer' in request body, got %v", gotBody["role"])
	}

	var data OrganizationMemberResourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("failed to read state: %v", diags)
	}
	if data.Email.ValueString() != "user@example.com" {
		t.Errorf("expected email to be mapped from response, got %s", data.Email.ValueString())
	}
	if data.Role.ValueString() != "developer" {
		t.Errorf("expected role to be mapped from response, got %s", data.Role.ValueString())
	}
}

// --- Delete ---

func TestOrganizationMemberResource_Delete(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotMethod = req.Method
		gotPath = req.URL.Path
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	r := &OrganizationMemberResource{client: admintest.NewClient(t, srv)}

	schema := schematest.ResourceSchema(t, r)
	objType := schematest.ResourceObjectType(t, r)
	vals := schematest.NullValues(t, r)
	vals["id"] = tftypes.NewValue(objType.AttributeTypes["id"], "user_01WCz1FkmYMm4gnmykNKUu3Q")

	state := tfsdk.State{
		Raw:    tftypes.NewValue(objType, vals),
		Schema: schema,
	}

	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete returned errors: %v", resp.Diagnostics)
	}

	if gotMethod != http.MethodDelete {
		t.Errorf("expected DELETE, got %s", gotMethod)
	}
	if gotPath != "/v1/organizations/users/user_01WCz1FkmYMm4gnmykNKUu3Q" {
		t.Errorf("expected users delete path, got %s", gotPath)
	}
}

// --- ImportState ---

func TestOrganizationMemberResource_ImportState(t *testing.T) {
	t.Parallel()

	r := &OrganizationMemberResource{}
	state := schematest.NullState(t, r)

	var resp resource.ImportStateResponse
	resp.State = state
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "user_01WCz1FkmYMm4gnmykNKUu3Q"}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState returned errors: %v", resp.Diagnostics)
	}

	var id string
	if diags := resp.State.GetAttribute(context.Background(), path.Root("id"), &id); diags.HasError() {
		t.Fatalf("failed to read imported id: %v", diags)
	}
	if id != "user_01WCz1FkmYMm4gnmykNKUu3Q" {
		t.Errorf("expected imported id to be 'user_01WCz1FkmYMm4gnmykNKUu3Q', got %s", id)
	}
}

// --- mapOrganizationMemberToResourceState ---

func TestMapOrganizationMemberToResourceState(t *testing.T) {
	t.Parallel()

	var member organizationMemberAPIResponse
	if err := json.Unmarshal([]byte(organizationMemberResourceFixture), &member); err != nil {
		t.Fatalf("failed to unmarshal fixture: %s", err)
	}

	data := mapOrganizationMemberToResourceState(member)

	if data.ID.ValueString() != "user_01WCz1FkmYMm4gnmykNKUu3Q" {
		t.Errorf("expected id to be mapped, got %s", data.ID.ValueString())
	}
	if data.Email.ValueString() != "user@example.com" {
		t.Errorf("expected email to be mapped, got %s", data.Email.ValueString())
	}
	if data.Name.ValueString() != "Example User" {
		t.Errorf("expected name to be mapped, got %s", data.Name.ValueString())
	}
	if data.Role.ValueString() != "developer" {
		t.Errorf("expected role to be mapped, got %s", data.Role.ValueString())
	}
	if data.AddedAt.ValueString() != "2026-01-01T00:00:00Z" {
		t.Errorf("expected added_at to be mapped, got %s", data.AddedAt.ValueString())
	}
	if data.Type.ValueString() != "user" {
		t.Errorf("expected type to be mapped, got %s", data.Type.ValueString())
	}
}
