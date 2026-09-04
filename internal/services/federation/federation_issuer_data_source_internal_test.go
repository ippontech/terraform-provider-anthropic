// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestMapFederationIssuerDataSourceToState_Discovery(t *testing.T) {
	createdAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	issuer := &anthropic.BetaFederationIssuer{
		ID:                    "fdis_01ABC",
		Name:                  "my-issuer",
		IssuerURL:             "https://issuer.example.com",
		CheckJTI:              true,
		MaxJWTLifetimeSeconds: 3600,
		CreatedAt:             createdAt,
		CreatedByActorID:      "user_01",
		UpdatedAt:             updatedAt,
		UpdatedByActorID:      "user_02",
		// ArchivedAt / ArchivedByActorID / JWKSPollingDisabledAt left zero.
		JWKS: anthropic.BetaFederationIssuerJWKSUnion{
			Type: "discovery",
		},
		PollStatus: anthropic.BetaFederationIssuerPollStatus{
			ConsecutiveFailures: 0,
			// LastFetchedAt / NextPollAt left zero.
		},
	}

	var data FederationIssuerDataSourceModel
	diags := mapFederationIssuerDataSourceToState(issuer, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if got := data.ID.ValueString(); got != "fdis_01ABC" {
		t.Errorf("ID = %q, want fdis_01ABC", got)
	}
	if got := data.Name.ValueString(); got != "my-issuer" {
		t.Errorf("Name = %q, want my-issuer", got)
	}
	if got := data.IssuerURL.ValueString(); got != "https://issuer.example.com" {
		t.Errorf("IssuerURL = %q, want https://issuer.example.com", got)
	}
	if !data.CheckJTI.ValueBool() {
		t.Error("CheckJTI = false, want true")
	}
	if got := data.MaxJWTLifetimeSeconds.ValueInt64(); got != 3600 {
		t.Errorf("MaxJWTLifetimeSeconds = %d, want 3600", got)
	}
	if got := data.CreatedAt.ValueString(); got != "2024-01-15T10:00:00Z" {
		t.Errorf("CreatedAt = %q, want 2024-01-15T10:00:00Z", got)
	}
	if got := data.UpdatedAt.ValueString(); got != "2024-01-15T11:00:00Z" {
		t.Errorf("UpdatedAt = %q, want 2024-01-15T11:00:00Z", got)
	}
	if got := data.CreatedByActorID.ValueString(); got != "user_01" {
		t.Errorf("CreatedByActorID = %q, want user_01", got)
	}
	if got := data.UpdatedByActorID.ValueString(); got != "user_02" {
		t.Errorf("UpdatedByActorID = %q, want user_02", got)
	}
	if !data.ArchivedAt.IsNull() {
		t.Errorf("ArchivedAt = %q, want null", data.ArchivedAt.ValueString())
	}
	if !data.ArchivedByActorID.IsNull() {
		t.Errorf("ArchivedByActorID = %q, want null", data.ArchivedByActorID.ValueString())
	}
	if !data.JWKSPollingDisabledAt.IsNull() {
		t.Errorf("JWKSPollingDisabledAt = %q, want null", data.JWKSPollingDisabledAt.ValueString())
	}

	jwksAttrs := data.JWKS.Attributes()
	if got := jwksAttrs["type"].(types.String).ValueString(); got != "discovery" {
		t.Errorf("jwks.type = %q, want discovery", got)
	}
	if !jwksAttrs["url"].(types.String).IsNull() {
		t.Error("jwks.url should be null for discovery mode")
	}
	if !jwksAttrs["keys"].(jsontypes.Normalized).IsNull() {
		t.Error("jwks.keys should be null for discovery mode")
	}

	pollAttrs := data.PollStatus.Attributes()
	if !pollAttrs["last_fetched_at"].(types.String).IsNull() {
		t.Error("poll_status.last_fetched_at should be null when never fetched")
	}
	if !pollAttrs["next_poll_at"].(types.String).IsNull() {
		t.Error("poll_status.next_poll_at should be null when paused")
	}
}

func TestMapFederationIssuerDataSourceToState_Archived(t *testing.T) {
	archivedAt := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	pollingDisabledAt := time.Date(2024, 6, 2, 0, 0, 0, 0, time.UTC)

	issuer := &anthropic.BetaFederationIssuer{
		ID:                    "fdis_02DEF",
		Name:                  "archived-issuer",
		IssuerURL:             "https://issuer.example.com",
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
		ArchivedAt:            archivedAt,
		ArchivedByActorID:     "user_03",
		JWKSPollingDisabledAt: pollingDisabledAt,
		JWKS:                  anthropic.BetaFederationIssuerJWKSUnion{Type: "explicit_url", URL: "https://issuer.example.com/jwks.json"},
	}

	var data FederationIssuerDataSourceModel
	diags := mapFederationIssuerDataSourceToState(issuer, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if data.ArchivedAt.IsNull() {
		t.Error("expected ArchivedAt to be non-null for an archived issuer")
	}
	if got := data.ArchivedAt.ValueString(); got != "2024-06-01T12:00:00Z" {
		t.Errorf("ArchivedAt = %q, want 2024-06-01T12:00:00Z", got)
	}
	if got := data.ArchivedByActorID.ValueString(); got != "user_03" {
		t.Errorf("ArchivedByActorID = %q, want user_03", got)
	}
	if got := data.JWKSPollingDisabledAt.ValueString(); got != "2024-06-02T00:00:00Z" {
		t.Errorf("JWKSPollingDisabledAt = %q, want 2024-06-02T00:00:00Z", got)
	}

	jwksAttrs := data.JWKS.Attributes()
	if got := jwksAttrs["type"].(types.String).ValueString(); got != "explicit_url" {
		t.Errorf("jwks.type = %q, want explicit_url", got)
	}
	if got := jwksAttrs["url"].(types.String).ValueString(); got != "https://issuer.example.com/jwks.json" {
		t.Errorf("jwks.url = %q, want https://issuer.example.com/jwks.json", got)
	}
}

func TestMapFederationIssuerDataSourceToState_InlineKeys(t *testing.T) {
	// Simulate what the SDK decodes off the wire for an inline JWKS: parse a
	// full BetaFederationIssuer JSON payload so JWKS.JSON.Keys.Raw() is
	// populated exactly as it would be from a live response.
	const body = `{
		"id": "fdis_03GHI",
		"name": "inline-issuer",
		"issuer_url": "https://issuer.example.com",
		"check_jti": true,
		"max_jwt_lifetime_seconds": 3600,
		"created_at": "2024-01-15T10:00:00Z",
		"created_by_actor_id": "user_01",
		"updated_at": "2024-01-15T11:00:00Z",
		"updated_by_actor_id": "user_01",
		"archived_at": null,
		"archived_by_actor_id": "",
		"jwks_polling_disabled_at": null,
		"jwks": {
			"type": "inline",
			"keys": [{"kty": "RSA", "kid": "key-1", "n": "abc", "e": "AQAB"}]
		},
		"poll_status": {
			"consecutive_failures": 0,
			"last_fetched_at": null,
			"next_poll_at": null
		}
	}`

	var issuer anthropic.BetaFederationIssuer
	if err := issuer.UnmarshalJSON([]byte(body)); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	var data FederationIssuerDataSourceModel
	diags := mapFederationIssuerDataSourceToState(&issuer, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	jwksAttrs := data.JWKS.Attributes()
	keysVal, ok := jwksAttrs["keys"].(jsontypes.Normalized)
	if !ok {
		t.Fatalf("keys attribute has unexpected type %T", jwksAttrs["keys"])
	}
	if keysVal.IsNull() {
		t.Fatal("expected jwks.keys to be non-null for inline mode")
	}
	if got := keysVal.ValueString(); got == "" {
		t.Error("expected jwks.keys to carry the raw JSON keys array")
	}
}

func TestFederationIssuerDataSource_Get404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/organizations/federation_issuers/fdis_missing" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"federation issuer not found"}}`)
	}))
	defer srv.Close()

	c := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAuthToken("test"))

	_, err := c.Beta.Organization.Federation.Issuers.Get(context.Background(), "fdis_missing", anthropic.BetaOrganizationFederationIssuerGetParams{})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected *anthropic.Error with status 404, got: %v", err)
	}
}
