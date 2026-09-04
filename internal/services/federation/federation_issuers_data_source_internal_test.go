// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// newTestFederationClient builds an SDK client pointed at srv, wrapped as the
// OAuth client this data source requires. Unlike admin.Client (retried by
// default), the SDK client's own MaxRetries default is small but nonzero;
// these tests never return a transient status, so that default is harmless.
func newTestFederationClient(srv *httptest.Server) *anthropic.Client {
	c := anthropic.NewClient(
		option.WithBaseURL(srv.URL),
		option.WithAuthToken("test"),
	)
	return &c
}

const federationIssuerJSONTemplate = `{
	"id": %q,
	"type": "federation_issuer",
	"issuer_url": "https://issuer.example.com",
	"name": "example-issuer",
	"check_jti": true,
	"max_jwt_lifetime_seconds": 3600,
	"jwks": {"type": "discovery"},
	"jwks_polling_disabled_at": null,
	"poll_status": {"consecutive_failures": 0, "last_fetched_at": null, "next_poll_at": null},
	"created_at": "2024-01-15T10:00:00Z",
	"created_by_actor_id": "user_01ABC",
	"updated_at": "2024-01-15T10:00:00Z",
	"updated_by_actor_id": "user_01ABC",
	"archived_at": null,
	"archived_by_actor_id": null
}`

func TestFederationIssuersDataSource_ListAllSinglePage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/federation_issuers" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"data":[`+sprintfIssuer("fdis_01AAA")+`],"next_page":null}`)
	}))
	defer srv.Close()

	client := newTestFederationClient(srv)
	pager := client.Beta.Organization.Federation.Issuers.ListAutoPaging(context.Background(), anthropic.BetaOrganizationFederationIssuerListParams{})

	var got []anthropic.BetaFederationIssuer
	for pager.Next() {
		got = append(got, pager.Current())
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pager error: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].ID != "fdis_01AAA" {
		t.Errorf("ID = %q, want fdis_01AAA", got[0].ID)
	}
}

func TestFederationIssuersDataSource_ListAllMultiplePages(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.URL.Query().Get("page") == "" {
			_, _ = io.WriteString(w, `{"data":[`+sprintfIssuer("fdis_01AAA")+`],"next_page":"cursor-2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[`+sprintfIssuer("fdis_01BBB")+`],"next_page":null}`)
	}))
	defer srv.Close()

	client := newTestFederationClient(srv)
	pager := client.Beta.Organization.Federation.Issuers.ListAutoPaging(context.Background(), anthropic.BetaOrganizationFederationIssuerListParams{})

	var got []anthropic.BetaFederationIssuer
	for pager.Next() {
		got = append(got, pager.Current())
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pager error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
	if got[0].ID != "fdis_01AAA" || got[1].ID != "fdis_01BBB" {
		t.Errorf("unexpected IDs: %q, %q", got[0].ID, got[1].ID)
	}
}

func TestFederationIssuersDataSource_EmptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"data":[],"next_page":null}`)
	}))
	defer srv.Close()

	client := newTestFederationClient(srv)
	pager := client.Beta.Organization.Federation.Issuers.ListAutoPaging(context.Background(), anthropic.BetaOrganizationFederationIssuerListParams{})

	var got []anthropic.BetaFederationIssuer
	for pager.Next() {
		got = append(got, pager.Current())
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pager error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0", len(got))
	}
}

func TestFederationIssuersDataSource_IncludeArchivedQueryParam(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("include_archived")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"data":[],"next_page":null}`)
	}))
	defer srv.Close()

	client := newTestFederationClient(srv)
	pager := client.Beta.Organization.Federation.Issuers.ListAutoPaging(context.Background(), anthropic.BetaOrganizationFederationIssuerListParams{
		IncludeArchived: param.NewOpt(true),
	})
	for pager.Next() {
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pager error: %v", err)
	}

	if gotQuery != "true" {
		t.Errorf("include_archived query param = %q, want true", gotQuery)
	}
}

func Test404NotFoundSurfacesAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"not found"}}`)
	}))
	defer srv.Close()

	client := newTestFederationClient(srv)
	pager := client.Beta.Organization.Federation.Issuers.ListAutoPaging(context.Background(), anthropic.BetaOrganizationFederationIssuerListParams{})
	for pager.Next() {
	}
	if err := pager.Err(); err == nil {
		t.Fatal("expected an error from a 404 response, got nil")
	}
}

// sprintfIssuer renders a minimal-but-complete federation issuer JSON object
// with the given ID, matching the shape mapFederationIssuersListEntry expects.
func sprintfIssuer(id string) string {
	return fmt.Sprintf(federationIssuerJSONTemplate, id)
}

func unmarshalIssuer(t *testing.T, raw string) *anthropic.BetaFederationIssuer {
	t.Helper()
	var issuer anthropic.BetaFederationIssuer
	if err := json.Unmarshal([]byte(raw), &issuer); err != nil {
		t.Fatalf("unmarshal issuer: %v", err)
	}
	return &issuer
}

func TestMapFederationIssuersListEntry_DiscoveryNotArchived(t *testing.T) {
	issuer := unmarshalIssuer(t, sprintfIssuer("fdis_01AAA"))

	objVal, diags := mapFederationIssuersListEntry(issuer)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	attrs := objVal.(types.Object).Attributes()
	if got := attrs["id"].(interface{ ValueString() string }).ValueString(); got != "fdis_01AAA" {
		t.Errorf("id = %q, want fdis_01AAA", got)
	}
	if got := attrs["name"].(interface{ ValueString() string }).ValueString(); got != "example-issuer" {
		t.Errorf("name = %q, want example-issuer", got)
	}
	if !attrs["archived_at"].(interface{ IsNull() bool }).IsNull() {
		t.Error("expected archived_at to be null for a non-archived issuer")
	}
	if !attrs["archived_by_actor_id"].(interface{ IsNull() bool }).IsNull() {
		t.Error("expected archived_by_actor_id to be null for a non-archived issuer")
	}
	if !attrs["jwks_polling_disabled_at"].(interface{ IsNull() bool }).IsNull() {
		t.Error("expected jwks_polling_disabled_at to be null when polling is active")
	}
}

func TestMapFederationIssuersJWKS_Discovery(t *testing.T) {
	var jwks anthropic.BetaFederationIssuerJWKSUnion
	if err := json.Unmarshal([]byte(`{"type":"discovery","discovery_base":"https://alt.example.com"}`), &jwks); err != nil {
		t.Fatalf("unmarshal jwks: %v", err)
	}

	obj, diags := mapFederationIssuersJWKS(jwks)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	attrs := obj.Attributes()
	if got := attrs["type"].(interface{ ValueString() string }).ValueString(); got != "discovery" {
		t.Errorf("type = %q, want discovery", got)
	}
	if got := attrs["discovery_base"].(interface{ ValueString() string }).ValueString(); got != "https://alt.example.com" {
		t.Errorf("discovery_base = %q, want https://alt.example.com", got)
	}
	if !attrs["url"].(interface{ IsNull() bool }).IsNull() {
		t.Error("expected url to be null for discovery mode")
	}
	if !attrs["keys"].(interface{ IsNull() bool }).IsNull() {
		t.Error("expected keys to be null for discovery mode")
	}
}

func TestMapFederationIssuersJWKS_InlineKeys(t *testing.T) {
	var jwks anthropic.BetaFederationIssuerJWKSUnion
	raw := `{"type":"inline","keys":[{"kty":"RSA","kid":"key-1","n":"abc","e":"AQAB"}]}`
	if err := json.Unmarshal([]byte(raw), &jwks); err != nil {
		t.Fatalf("unmarshal jwks: %v", err)
	}

	obj, diags := mapFederationIssuersJWKS(jwks)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	attrs := obj.Attributes()
	if attrs["keys"].(interface{ IsNull() bool }).IsNull() {
		t.Fatal("expected keys to be non-null for inline mode")
	}
	keysVal := attrs["keys"].(interface{ ValueString() string }).ValueString()
	var roundTripped []map[string]any
	if err := json.Unmarshal([]byte(keysVal), &roundTripped); err != nil {
		t.Fatalf("keys is not valid JSON: %v (%s)", err, keysVal)
	}
	if len(roundTripped) != 1 || roundTripped[0]["kid"] != "key-1" {
		t.Errorf("unexpected keys content: %s", keysVal)
	}
}

func TestMapFederationIssuersPollStatus_Zero(t *testing.T) {
	var status anthropic.BetaFederationIssuerPollStatus
	if err := json.Unmarshal([]byte(`{"consecutive_failures":0,"last_fetched_at":null,"next_poll_at":null}`), &status); err != nil {
		t.Fatalf("unmarshal poll status: %v", err)
	}

	obj, diags := mapFederationIssuersPollStatus(status)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	attrs := obj.Attributes()
	if !attrs["last_fetched_at"].(interface{ IsNull() bool }).IsNull() {
		t.Error("expected last_fetched_at to be null when never fetched")
	}
	if !attrs["next_poll_at"].(interface{ IsNull() bool }).IsNull() {
		t.Error("expected next_poll_at to be null when paused")
	}
}

func TestMapFederationIssuersPollStatus_Populated(t *testing.T) {
	var status anthropic.BetaFederationIssuerPollStatus
	raw := `{"consecutive_failures":3,"last_fetched_at":"2024-06-01T12:00:00Z","next_poll_at":"2024-06-01T13:00:00Z"}`
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		t.Fatalf("unmarshal poll status: %v", err)
	}

	obj, diags := mapFederationIssuersPollStatus(status)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	attrs := obj.Attributes()
	if got := attrs["last_fetched_at"].(interface{ ValueString() string }).ValueString(); got != "2024-06-01T12:00:00Z" {
		t.Errorf("last_fetched_at = %q, want 2024-06-01T12:00:00Z", got)
	}
	if got := attrs["next_poll_at"].(interface{ ValueString() string }).ValueString(); got != "2024-06-01T13:00:00Z" {
		t.Errorf("next_poll_at = %q, want 2024-06-01T13:00:00Z", got)
	}
}

func TestMapFederationIssuersListEntry_Archived(t *testing.T) {
	raw := `{
		"id": "fdis_01ARCHIVED",
		"type": "federation_issuer",
		"issuer_url": "https://issuer.example.com",
		"name": "archived-issuer",
		"check_jti": true,
		"max_jwt_lifetime_seconds": 3600,
		"jwks": {"type": "explicit_url", "url": "https://issuer.example.com/jwks.json"},
		"jwks_polling_disabled_at": "2024-05-01T00:00:00Z",
		"poll_status": {"consecutive_failures": 5, "last_fetched_at": "2024-04-01T00:00:00Z", "next_poll_at": null},
		"created_at": "2024-01-15T10:00:00Z",
		"created_by_actor_id": "user_01ABC",
		"updated_at": "2024-01-15T10:00:00Z",
		"updated_by_actor_id": "user_01ABC",
		"archived_at": "2024-06-01T00:00:00Z",
		"archived_by_actor_id": "user_01DEF"
	}`
	issuer := unmarshalIssuer(t, raw)

	objVal, diags := mapFederationIssuersListEntry(issuer)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	attrs := objVal.(types.Object).Attributes()
	if got := attrs["archived_at"].(interface{ ValueString() string }).ValueString(); got != "2024-06-01T00:00:00Z" {
		t.Errorf("archived_at = %q, want 2024-06-01T00:00:00Z", got)
	}
	if got := attrs["archived_by_actor_id"].(interface{ ValueString() string }).ValueString(); got != "user_01DEF" {
		t.Errorf("archived_by_actor_id = %q, want user_01DEF", got)
	}
	if got := attrs["jwks_polling_disabled_at"].(interface{ ValueString() string }).ValueString(); got != "2024-05-01T00:00:00Z" {
		t.Errorf("jwks_polling_disabled_at = %q, want 2024-05-01T00:00:00Z", got)
	}
}
