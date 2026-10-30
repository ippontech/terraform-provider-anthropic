// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// No live acceptance test is written for anthropic_compliance_settings: Admin
// API resource tests wait for a dedicated test organization (#58, see
// CLAUDE.md), and toggling the setting on a shared organization would stop or
// start its activity recording. Coverage here is httptest-based.
package compliance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

const testOrgID = "12345678-1234-5678-1234-567812345678"

type recordedRequest struct {
	method, path, query string
	body                map[string]any
}

// newSettingsServer serves GET /v1/organizations/me and the compliance
// settings endpoint, answering every settings call with settingsBody, and
// records each request.
func newSettingsServer(t *testing.T, settingsBody string) (*httptest.Server, *[]recordedRequest) {
	t.Helper()
	return newOrgSettingsServer(t, testOrgID, settingsBody)
}

// newOrgSettingsServer is newSettingsServer for an admin key of organization
// orgID.
func newOrgSettingsServer(t *testing.T, orgID, settingsBody string) (*httptest.Server, *[]recordedRequest) {
	t.Helper()
	var mu sync.Mutex
	var reqs []recordedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		reqs = append(reqs, recordedRequest{r.Method, r.URL.Path, r.URL.RawQuery, body})
		mu.Unlock()
		w.Header().Set("content-type", "application/json")
		switch r.URL.Path {
		case "/v1/organizations/me":
			_, _ = io.WriteString(w, `{"id":"`+orgID+`","name":"Test Organization","type":"organization"}`)
		case "/v1/organizations/compliance_settings":
			_, _ = io.WriteString(w, settingsBody)
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

func settingsValue(t *testing.T, id, state any) tftypes.Value {
	t.Helper()
	objType := schematest.ResourceObjectType(t, &ComplianceSettingsResource{})
	return tftypes.NewValue(objType, map[string]tftypes.Value{
		"id":    tftypes.NewValue(tftypes.String, id),
		"state": tftypes.NewValue(tftypes.String, state),
	})
}

// --- Create ---

func TestComplianceSettingsResource_Create(t *testing.T) {
	srv, reqs := newSettingsServer(t, `{"type":"compliance_settings","state":{"type":"enabled"}}`)
	r := &ComplianceSettingsResource{client: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)

	resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: tfsdk.Plan{Schema: s, Raw: settingsValue(t, tftypes.UnknownValue, "enabled")},
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	if len(*reqs) != 2 {
		t.Fatalf("requests = %+v, want GET /me then POST", *reqs)
	}
	if got := (*reqs)[0]; got.method != http.MethodGet || got.path != "/v1/organizations/me" {
		t.Errorf("first request = %s %s", got.method, got.path)
	}
	post := (*reqs)[1]
	if post.method != http.MethodPost || post.path != "/v1/organizations/compliance_settings" || post.query != "" {
		t.Errorf("second request = %s %s?%s", post.method, post.path, post.query)
	}
	if b, _ := json.Marshal(post.body); string(b) != `{"state":{"type":"enabled"}}` {
		t.Errorf("body = %s, want the canonical object form", b)
	}

	var data ComplianceSettingsResourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if data.ID.ValueString() != testOrgID || data.State.ValueString() != "enabled" {
		t.Errorf("state = %+v", data)
	}
}

func TestComplianceSettingsResource_Create_apiError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		if r.URL.Path == "/v1/organizations/me" {
			_, _ = io.WriteString(w, `{"id":"`+testOrgID+`"}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"type":"permission_error","message":"not eligible"}}`)
	}))
	defer srv.Close()

	r := &ComplianceSettingsResource{client: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)
	resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: tfsdk.Plan{Schema: s, Raw: settingsValue(t, tftypes.UnknownValue, "enabled")},
	}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic for a 403")
	}
	if !resp.State.Raw.IsNull() {
		t.Error("state must stay empty when the update fails")
	}
}

// --- Read ---

func TestComplianceSettingsResource_Read(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"object form", `{"type":"compliance_settings","state":{"type":"enabled"}}`, "enabled"},
		{"string shorthand", `{"type":"compliance_settings","state":"disabled"}`, "disabled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, reqs := newSettingsServer(t, tc.body)
			r := &ComplianceSettingsResource{client: admintest.NewClient(t, srv)}
			s := schematest.ResourceSchema(t, r)
			// The prior state differs from the API: Read must report the API value.
			state := tfsdk.State{Schema: s, Raw: settingsValue(t, testOrgID, "other")}

			resp := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Read: %v", resp.Diagnostics)
			}
			if len(*reqs) != 2 || (*reqs)[0].path != "/v1/organizations/me" ||
				(*reqs)[1].method != http.MethodGet || (*reqs)[1].path != "/v1/organizations/compliance_settings" {
				t.Errorf("requests = %+v, want GET /me then a GET of the settings", *reqs)
			}

			var data ComplianceSettingsResourceModel
			if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
				t.Fatalf("state: %v", diags)
			}
			if data.ID.ValueString() != testOrgID || data.State.ValueString() != tc.want {
				t.Errorf("state = %+v, want id kept and state %q", data, tc.want)
			}
		})
	}
}

func TestComplianceSettingsResource_Read_notFoundIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"not found"}}`)
	}))
	defer srv.Close()

	r := &ComplianceSettingsResource{client: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)
	state := tfsdk.State{Schema: s, Raw: settingsValue(t, testOrgID, "enabled")}

	resp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error: the singleton always exists, so a 404 must not plan a re-creation")
	}
	if resp.State.Raw.IsNull() {
		t.Error("the resource must not be removed from state")
	}
}

func TestParseState(t *testing.T) {
	for raw, want := range map[string]string{
		`{"type":"enabled"}`:  "enabled",
		`{"type":"disabled"}`: "disabled",
		`"enabled"`:           "enabled",
	} {
		got, err := parseState(json.RawMessage(raw))
		if err != nil || got != want {
			t.Errorf("parseState(%s) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{`{}`, `null`, `42`, ``} {
		if got, err := parseState(json.RawMessage(raw)); err == nil {
			t.Errorf("parseState(%s) = %q, want an error", raw, got)
		}
	}
}

// --- Update ---

func TestComplianceSettingsResource_Update(t *testing.T) {
	srv, reqs := newSettingsServer(t, `{"type":"compliance_settings","state":{"type":"disabled"}}`)
	r := &ComplianceSettingsResource{client: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)
	state := tfsdk.State{Schema: s, Raw: settingsValue(t, testOrgID, "enabled")}
	plan := tfsdk.Plan{Schema: s, Raw: settingsValue(t, testOrgID, "disabled")}

	resp := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}
	if len(*reqs) != 2 || (*reqs)[0].path != "/v1/organizations/me" || (*reqs)[1].method != http.MethodPost {
		t.Fatalf("requests = %+v, want GET /me then a POST", *reqs)
	}
	if b, _ := json.Marshal((*reqs)[1].body); string(b) != `{"state":{"type":"disabled"}}` {
		t.Errorf("body = %s", b)
	}

	var data ComplianceSettingsResourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if data.ID.ValueString() != testOrgID || data.State.ValueString() != "disabled" {
		t.Errorf("state = %+v", data)
	}
}

// --- Delete ---

func TestComplianceSettingsResource_Delete_leavesSettingsAlone(t *testing.T) {
	srv, reqs := newSettingsServer(t, `{}`)
	r := &ComplianceSettingsResource{client: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)
	state := tfsdk.State{Schema: s, Raw: settingsValue(t, testOrgID, "enabled")}

	var resp resource.DeleteResponse
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete: %v", resp.Diagnostics)
	}
	if len(*reqs) != 0 {
		t.Errorf("Delete must not call the API, got %+v", *reqs)
	}
	if resp.Diagnostics.WarningsCount() != 1 {
		t.Errorf("diagnostics = %v, want one warning saying the settings are left as they are", resp.Diagnostics)
	}
}

// --- ImportState ---

func TestComplianceSettingsResource_ImportState(t *testing.T) {
	for importID, warnings := range map[string]int{testOrgID: 0, "compliance_settings": 1} {
		t.Run(importID, func(t *testing.T) {
			srv, _ := newSettingsServer(t, `{}`)
			r := &ComplianceSettingsResource{client: admintest.NewClient(t, srv)}

			resp := resource.ImportStateResponse{State: schematest.NullState(t, r)}
			r.ImportState(context.Background(), resource.ImportStateRequest{ID: importID}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("ImportState: %v", resp.Diagnostics)
			}
			var id string
			if diags := resp.State.GetAttribute(context.Background(), path.Root("id"), &id); diags.HasError() || id != testOrgID {
				t.Errorf("imported id = %q, want the organization ID %q (%v)", id, testOrgID, diags)
			}
			if got := resp.Diagnostics.WarningsCount(); got != warnings {
				t.Errorf("warnings = %d, want %d (an ID other than the organization's names the one imported): %v", got, warnings, resp.Diagnostics)
			}
		})
	}
}

func TestComplianceSettingsResource_ImportState_organizationError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"type":"authentication_error","message":"invalid admin key"}}`)
	}))
	defer srv.Close()

	r := &ComplianceSettingsResource{client: admintest.NewClient(t, srv)}
	resp := resource.ImportStateResponse{State: schematest.NullState(t, r)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: testOrgID}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error when the organization cannot be read")
	}
}

// --- Admin key of another organization ---

const otherOrgID = "87654321-4321-8765-4321-876543218765"

// Read fails, without reading the settings, when the admin key belongs to
// another organization than the stored id: the singleton is addressed through
// the key alone, so nothing else would notice.
func TestComplianceSettingsResource_Read_otherOrganizationFails(t *testing.T) {
	srv, reqs := newOrgSettingsServer(t, otherOrgID, `{"type":"compliance_settings","state":{"type":"enabled"}}`)
	r := &ComplianceSettingsResource{client: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)
	state := tfsdk.State{Schema: s, Raw: settingsValue(t, testOrgID, "disabled")}

	resp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error for an admin key of another organization")
	}
	for _, req := range *reqs {
		if req.path == "/v1/organizations/compliance_settings" {
			t.Errorf("requests = %+v, want no settings call", *reqs)
		}
	}
}

// Update checks the organization before the POST too, for an apply with
// -refresh=false, and sends nothing on a mismatch.
func TestComplianceSettingsResource_Update_otherOrganizationFails(t *testing.T) {
	srv, reqs := newOrgSettingsServer(t, otherOrgID, `{"type":"compliance_settings","state":{"type":"disabled"}}`)
	r := &ComplianceSettingsResource{client: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)
	state := tfsdk.State{Schema: s, Raw: settingsValue(t, testOrgID, "enabled")}
	plan := tfsdk.Plan{Schema: s, Raw: settingsValue(t, testOrgID, "disabled")}

	resp := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error for an admin key of another organization")
	}
	for _, req := range *reqs {
		if req.method == http.MethodPost {
			t.Errorf("requests = %+v, want no POST", *reqs)
		}
	}
}

// --- POST answering another state ---

// Create reports a POST that answers another state than the planned one
// with its own error, and stores nothing, as for an API error.
func TestComplianceSettingsResource_Create_stateNotApplied(t *testing.T) {
	srv, _ := newSettingsServer(t, `{"type":"compliance_settings","state":{"type":"enabled"}}`)
	r := &ComplianceSettingsResource{client: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)

	resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{
		Plan: tfsdk.Plan{Schema: s, Raw: settingsValue(t, tftypes.UnknownValue, "disabled")},
	}, &resp)
	if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != "Compliance Settings Not Applied" {
		t.Fatalf("diagnostics = %v, want a Compliance Settings Not Applied error", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("state must stay empty when the state was not applied")
	}
}

// Update reports the mismatch with its own error and keeps the planned value,
// so Terraform does not add its inconsistent-result error on top.
func TestComplianceSettingsResource_Update_stateNotApplied(t *testing.T) {
	srv, _ := newSettingsServer(t, `{"type":"compliance_settings","state":{"type":"enabled"}}`)
	r := &ComplianceSettingsResource{client: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)
	state := tfsdk.State{Schema: s, Raw: settingsValue(t, testOrgID, "enabled")}
	plan := tfsdk.Plan{Schema: s, Raw: settingsValue(t, testOrgID, "disabled")}

	resp := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &resp)
	if !resp.Diagnostics.HasError() || resp.Diagnostics[0].Summary() != "Compliance Settings Not Applied" {
		t.Fatalf("diagnostics = %v, want a Compliance Settings Not Applied error", resp.Diagnostics)
	}
	var data ComplianceSettingsResourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if data.State.ValueString() != "disabled" {
		t.Errorf("state = %q, want the planned value", data.State.ValueString())
	}
}
