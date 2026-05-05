// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

// newTestAdminClient returns an admin.Client pointed at srv instead of the real API.
func newTestAdminClient(t *testing.T, srv *httptest.Server) *admin.Client {
	t.Helper()
	return &admin.Client{
		ApiKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	}
}

// --- parseAllowedInferenceGeos ---

func TestParseAllowedInferenceGeos(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{name: "empty raw", input: "", want: nil},
		{name: "json null", input: "null", want: nil},
		{name: "unrestricted string", input: `"unrestricted"`, want: []string{"unrestricted"}},
		{name: "specific geos array", input: `["us","eu"]`, want: []string{"us", "eu"}},
		{name: "single geo array", input: `["us"]`, want: []string{"us"}},
		{name: "empty array", input: `[]`, want: []string{}},
		{name: "invalid json", input: `{bad}`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAllowedInferenceGeos(json.RawMessage(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// --- buildAllowedInferenceGeos ---

func TestBuildAllowedInferenceGeos(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  string
	}{
		{name: "unrestricted sentinel", input: []string{"unrestricted"}, want: `"unrestricted"`},
		{name: "specific geos", input: []string{"us", "eu"}, want: `["us","eu"]`},
		{name: "single geo", input: []string{"us"}, want: `["us"]`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := string(buildAllowedInferenceGeos(tc.input))
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// --- Workspace API round-trip via mock server ---

func workspaceFixture() string {
	return `{
		"id": "wrkspc_01ABC",
		"name": "test-workspace",
		"archived_at": null,
		"created_at": "2026-01-01T00:00:00Z",
		"display_color": "#FF5733",
		"type": "workspace",
		"data_residency": {
			"allowed_inference_geos": "unrestricted",
			"default_inference_geo": "global",
			"workspace_geo": "us"
		}
	}`
}

func TestWorkspaceCreate_parsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/organizations/workspaces" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, workspaceFixture())
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	body, err := client.DoRequest(context.Background(), "POST", "/v1/organizations/workspaces",
		workspaceCreateRequest{Name: "test-workspace"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var ws workspaceAPIResponse
	if err := json.Unmarshal(body, &ws); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ws.ID != "wrkspc_01ABC" {
		t.Errorf("ID = %q, want %q", ws.ID, "wrkspc_01ABC")
	}
	if ws.ArchivedAt != nil {
		t.Errorf("ArchivedAt = %v, want nil", ws.ArchivedAt)
	}

	geos, err := parseAllowedInferenceGeos(ws.DataResidency.AllowedInferenceGeos)
	if err != nil {
		t.Fatalf("parseAllowedInferenceGeos: %v", err)
	}
	if len(geos) != 1 || geos[0] != "unrestricted" {
		t.Errorf("geos = %v, want [unrestricted]", geos)
	}
}

func TestWorkspaceRead_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"workspace not found"}}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	_, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/workspaces/wrkspc_missing", nil)
	if !admin.IsNotFound(err) {
		t.Fatalf("expected IsNotFound, got: %v", err)
	}
}

func TestWorkspaceRead_archivedDetection(t *testing.T) {
	archivedAt := "2026-04-25T10:00:00Z"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{
			"id": "wrkspc_01ABC",
			"name": "archived-workspace",
			"archived_at": "`+archivedAt+`",
			"created_at": "2026-01-01T00:00:00Z",
			"display_color": "#FF5733",
			"type": "workspace",
			"data_residency": {
				"allowed_inference_geos": "unrestricted",
				"default_inference_geo": "global",
				"workspace_geo": "us"
			}
		}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	body, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/workspaces/wrkspc_01ABC", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var ws workspaceAPIResponse
	if err := json.Unmarshal(body, &ws); err != nil {
		t.Fatalf("parse: %v", err)
	}
	// The resource's Read method removes archived workspaces from state.
	// Verify the detection condition matches what the code checks.
	if ws.ArchivedAt == nil || *ws.ArchivedAt == "" {
		t.Errorf("expected ArchivedAt to be set, got nil/empty")
	}
	if *ws.ArchivedAt != archivedAt {
		t.Errorf("ArchivedAt = %q, want %q", *ws.ArchivedAt, archivedAt)
	}
}

func TestWorkspaceArchive_sendsCorrectPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, workspaceFixture())
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	_, err := client.DoRequest(context.Background(), "POST", "/v1/organizations/workspaces/wrkspc_01ABC/archive", nil)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if gotPath != "/v1/organizations/workspaces/wrkspc_01ABC/archive" {
		t.Errorf("path = %q, want archive path", gotPath)
	}
}

// --- WorkspaceMember data source ---

func TestWorkspaceMemberRead_requestPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"workspace_role":"workspace_developer","type":"workspace_member"}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	body, err := client.DoRequest(context.Background(), "GET",
		"/v1/organizations/workspaces/wrkspc_01ABC/members/user_01XYZ", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantPath := "/v1/organizations/workspaces/wrkspc_01ABC/members/user_01XYZ"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}

	var m workspaceMemberAPIResponse
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.WorkspaceRole != "workspace_developer" {
		t.Errorf("WorkspaceRole = %q, want %q", m.WorkspaceRole, "workspace_developer")
	}
	if m.Type != "workspace_member" {
		t.Errorf("Type = %q, want %q", m.Type, "workspace_member")
	}
}

func TestWorkspaceMemberRead_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"workspace member not found"}}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	_, err := client.DoRequest(context.Background(), "GET",
		"/v1/organizations/workspaces/wrkspc_01ABC/members/user_missing", nil)
	if !admin.IsNotFound(err) {
		t.Fatalf("expected IsNotFound, got: %v", err)
	}
}

func TestWorkspaceUpdate_excludesWorkspaceGeo(t *testing.T) {
	var reqBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, workspaceFixture())
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	updateReq := workspaceUpdateRequest{
		Name: "updated-name",
		DataResidency: &workspaceUpdateDataResidency{
			DefaultInferenceGeo:  "global",
			AllowedInferenceGeos: json.RawMessage(`"unrestricted"`),
		},
	}
	_, err := client.DoRequest(context.Background(), "POST", "/v1/organizations/workspaces/wrkspc_01ABC", updateReq)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(reqBody, &parsed); err != nil {
		t.Fatalf("parse request body: %v", err)
	}
	if _, ok := parsed["workspace_geo"]; ok {
		t.Error("update request must not include workspace_geo (immutable field)")
	}
	if parsed["name"] != "updated-name" {
		t.Errorf("name = %v, want updated-name", parsed["name"])
	}
}
