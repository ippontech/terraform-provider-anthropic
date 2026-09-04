// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

func newTestFederationOAuthClient(t *testing.T, srv *httptest.Server) *providerdata.OAuthClient {
	t.Helper()

	c := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAuthToken("test"))
	return &providerdata.OAuthClient{Client: &c}
}

func TestMapFederationRuleDataSourceToState_FullyPopulated(t *testing.T) {
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)
	archivedAt := createdAt.Add(2 * time.Hour)

	rule := &anthropic.BetaFederationRule{
		ID:                     "fdrl_01ABC",
		Name:                   "ci-rule",
		Description:            "CI pipeline rule",
		IssuerID:               "fdis_01ABC",
		IssuerName:             "GitHub Actions",
		OAuthScope:             "workspace:developer",
		WorkspaceID:            "wrkspc_01ABC",
		AppliesToAllWorkspaces: false,
		TokenLifetimeSeconds:   3600,
		CreatedAt:              createdAt,
		CreatedByActorID:       "user_01ABC",
		UpdatedAt:              updatedAt,
		UpdatedByActorID:       "user_01DEF",
		ArchivedAt:             archivedAt,
		ArchivedByActorID:      "user_01GHI",
		Attributes: map[string]string{
			"team": "platform",
		},
		WorkspaceIDs: []string{"wrkspc_01ABC", "wrkspc_01DEF"},
		Match: anthropic.BetaFederationRuleMatch{
			SubjectPrefix: "repo:my-org/my-repo:*",
			Audience:      "https://anthropic.com",
			Condition:     "claims.repository_owner == 'my-org'",
			Claims: map[string]string{
				"repository_owner": "my-org",
			},
		},
		Target: anthropic.BetaServiceAccountTarget{
			ServiceAccountID:   "svac_01ABC",
			ServiceAccountName: "ci-bot",
		},
	}

	data, diags := mapFederationRuleDataSourceToState(rule)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if got := data.ID.ValueString(); got != "fdrl_01ABC" {
		t.Errorf("ID = %q, want %q", got, "fdrl_01ABC")
	}
	if got := data.Description.ValueString(); got != "CI pipeline rule" {
		t.Errorf("Description = %q, want %q", got, "CI pipeline rule")
	}
	if got := data.WorkspaceID.ValueString(); got != "wrkspc_01ABC" {
		t.Errorf("WorkspaceID = %q, want %q", got, "wrkspc_01ABC")
	}
	if data.ArchivedAt.IsNull() {
		t.Error("expected ArchivedAt to be non-null for an archived rule")
	}
	if got := data.ArchivedByActorID.ValueString(); got != "user_01GHI" {
		t.Errorf("ArchivedByActorID = %q, want %q", got, "user_01GHI")
	}
	if got := data.TokenLifetimeSeconds.ValueInt64(); got != 3600 {
		t.Errorf("TokenLifetimeSeconds = %d, want 3600", got)
	}

	attrsMap := data.Attributes.Elements()
	if len(attrsMap) != 1 {
		t.Fatalf("expected 1 attribute, got %d", len(attrsMap))
	}

	workspaceIDs := data.WorkspaceIDs.Elements()
	if len(workspaceIDs) != 2 {
		t.Fatalf("expected 2 workspace_ids, got %d", len(workspaceIDs))
	}

	matchAttrs := data.Match.Attributes()
	if got := matchAttrs["subject_prefix"]; got.String() != `"repo:my-org/my-repo:*"` {
		t.Errorf("match.subject_prefix = %s, want %q", got.String(), "repo:my-org/my-repo:*")
	}
	claims, ok := matchAttrs["claims"]
	if !ok {
		t.Fatal("expected match.claims to be present")
	}
	if claims.IsNull() {
		t.Error("expected match.claims to be non-null when populated")
	}

	targetAttrs := data.Target.Attributes()
	if got := targetAttrs["service_account_id"]; got.String() != `"svac_01ABC"` {
		t.Errorf("target.service_account_id = %s, want %q", got.String(), "svac_01ABC")
	}
}

func TestMapFederationRuleDataSourceToState_NullableFieldsEmpty(t *testing.T) {
	rule := &anthropic.BetaFederationRule{
		ID:                     "fdrl_02DEF",
		Name:                   "minimal-rule",
		IssuerID:               "fdis_01ABC",
		IssuerName:             "GitHub Actions",
		OAuthScope:             "workspace:developer",
		AppliesToAllWorkspaces: true,
		TokenLifetimeSeconds:   3600,
		CreatedAt:              time.Now(),
		CreatedByActorID:       "user_01ABC",
		UpdatedAt:              time.Now(),
		UpdatedByActorID:       "user_01ABC",
		// Description, WorkspaceID, ArchivedAt, ArchivedByActorID, Attributes, WorkspaceIDs all unset.
		Match: anthropic.BetaFederationRuleMatch{
			SubjectPrefix: "repo:my-org/*",
			// Audience, Condition, Claims unset.
		},
		Target: anthropic.BetaServiceAccountTarget{
			ServiceAccountID: "svac_01ABC",
			// ServiceAccountName unset.
		},
	}

	data, diags := mapFederationRuleDataSourceToState(rule)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if !data.Description.IsNull() {
		t.Errorf("expected Description to be null, got %q", data.Description.ValueString())
	}
	if !data.WorkspaceID.IsNull() {
		t.Errorf("expected WorkspaceID to be null, got %q", data.WorkspaceID.ValueString())
	}
	if !data.ArchivedAt.IsNull() {
		t.Errorf("expected ArchivedAt to be null, got %q", data.ArchivedAt.ValueString())
	}
	if !data.ArchivedByActorID.IsNull() {
		t.Errorf("expected ArchivedByActorID to be null, got %q", data.ArchivedByActorID.ValueString())
	}
	if !data.Attributes.IsNull() {
		t.Errorf("expected Attributes to be null when empty, got %v", data.Attributes)
	}
	if !data.WorkspaceIDs.IsNull() {
		t.Errorf("expected WorkspaceIDs to be null when empty, got %v", data.WorkspaceIDs)
	}

	matchAttrs := data.Match.Attributes()
	if audience, ok := matchAttrs["audience"]; !ok || !audience.IsNull() {
		t.Errorf("expected match.audience to be null, got %v", audience)
	}
	if condition, ok := matchAttrs["condition"]; !ok || !condition.IsNull() {
		t.Errorf("expected match.condition to be null, got %v", condition)
	}
	if claims, ok := matchAttrs["claims"]; !ok || !claims.IsNull() {
		t.Errorf("expected match.claims to be null when empty, got %v", claims)
	}

	targetAttrs := data.Target.Attributes()
	if name, ok := targetAttrs["service_account_name"]; !ok || !name.IsNull() {
		t.Errorf("expected target.service_account_name to be null, got %v", name)
	}
}

func TestFederationRuleDataSource_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"federation rule not found"}}`)
	}))
	defer srv.Close()

	client := newTestFederationOAuthClient(t, srv)

	_, err := client.Beta.Organization.Federation.Rules.Get(context.Background(), "fdrl_missing", anthropic.BetaOrganizationFederationRuleGetParams{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFederationRuleDataSource_get(t *testing.T) {
	fixture := `{
		"id": "fdrl_01ABC",
		"applies_to_all_workspaces": false,
		"archived_at": null,
		"archived_by_actor_id": "",
		"attributes": {},
		"created_at": "2026-01-01T00:00:00Z",
		"created_by_actor_id": "user_01ABC",
		"description": "",
		"issuer_id": "fdis_01ABC",
		"issuer_name": "GitHub Actions",
		"match": {
			"subject_prefix": "repo:my-org/my-repo:*",
			"audience": null,
			"claims": null,
			"condition": null
		},
		"name": "ci-rule",
		"oauth_scope": "workspace:developer",
		"target": {
			"service_account_id": "svac_01ABC",
			"service_account_name": "ci-bot",
			"type": "service_account"
		},
		"token_lifetime_seconds": 3600,
		"type": "federation_rule",
		"updated_at": "2026-01-01T01:00:00Z",
		"updated_by_actor_id": "user_01ABC",
		"workspace_id": "",
		"workspace_ids": []
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/federation_rules/fdrl_01ABC" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, fixture)
	}))
	defer srv.Close()

	client := newTestFederationOAuthClient(t, srv)

	rule, err := client.Beta.Organization.Federation.Rules.Get(context.Background(), "fdrl_01ABC", anthropic.BetaOrganizationFederationRuleGetParams{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	data, diags := mapFederationRuleDataSourceToState(rule)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if got := data.ID.ValueString(); got != "fdrl_01ABC" {
		t.Errorf("ID = %q, want %q", got, "fdrl_01ABC")
	}
	if !data.Description.IsNull() {
		t.Errorf("expected Description to be null for empty string, got %q", data.Description.ValueString())
	}
	if !data.WorkspaceID.IsNull() {
		t.Errorf("expected WorkspaceID to be null for empty string, got %q", data.WorkspaceID.ValueString())
	}
	if !data.WorkspaceIDs.IsNull() {
		t.Errorf("expected WorkspaceIDs to be null for empty list, got %v", data.WorkspaceIDs)
	}
	if !data.Attributes.IsNull() {
		t.Errorf("expected Attributes to be null for empty map, got %v", data.Attributes)
	}
}
