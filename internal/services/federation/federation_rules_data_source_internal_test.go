// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestMapFederationRulesListEntry_basicFields(t *testing.T) {
	createdAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	archivedAt := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

	rule := anthropic.BetaFederationRule{
		ID:                     "fdrl_01ABC",
		AppliesToAllWorkspaces: true,
		ArchivedAt:             archivedAt,
		ArchivedByActorID:      "user_01ARCHIVER",
		Attributes:             map[string]string{"team": "claims.team"},
		CreatedAt:              createdAt,
		CreatedByActorID:       "user_01CREATOR",
		Description:            "used by the CI pipeline",
		IssuerID:               "fdis_01ISSUER",
		IssuerName:             "GitHub Actions",
		Match: anthropic.BetaFederationRuleMatch{
			Audience:      "https://anthropic.com",
			Claims:        map[string]string{"repository_owner": "ippontech"},
			Condition:     "claims.repository_owner == 'ippontech'",
			SubjectPrefix: "repo:ippontech/terraform-provider-anthropic:*",
		},
		Name:       "ci-runner-rule",
		OAuthScope: "workspace:developer",
		Target: anthropic.BetaServiceAccountTarget{
			ServiceAccountID:   "svac_01ABC",
			Type:               "service_account",
			ServiceAccountName: "ci-runner",
		},
		TokenLifetimeSeconds: 3600,
		Type:                 "federation_rule",
		UpdatedAt:            updatedAt,
		UpdatedByActorID:     "user_01UPDATER",
		WorkspaceID:          "wrkspc_01ABC",
		WorkspaceIDs:         []string{"wrkspc_01ABC", "wrkspc_02DEF"},
	}

	value, diags := mapFederationRulesListEntry(context.Background(), rule)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	obj, ok := value.(types.Object)
	if !ok {
		t.Fatalf("expected types.Object, got %T", value)
	}
	attrs := obj.Attributes()

	if got := attrs["id"].(types.String).ValueString(); got != "fdrl_01ABC" {
		t.Errorf("id = %q, want fdrl_01ABC", got)
	}
	if got := attrs["applies_to_all_workspaces"].(types.Bool).ValueBool(); !got {
		t.Errorf("applies_to_all_workspaces = %v, want true", got)
	}
	if got := attrs["archived_at"].(types.String).ValueString(); got != "2024-06-01T12:00:00Z" {
		t.Errorf("archived_at = %q, want 2024-06-01T12:00:00Z", got)
	}
	if got := attrs["archived_by_actor_id"].(types.String).ValueString(); got != "user_01ARCHIVER" {
		t.Errorf("archived_by_actor_id = %q, want user_01ARCHIVER", got)
	}
	if attrs["attributes"].(types.Map).IsNull() {
		t.Error("attributes should not be null when the API returns entries")
	}
	if got := attrs["created_at"].(types.String).ValueString(); got != "2024-01-15T10:00:00Z" {
		t.Errorf("created_at = %q, want 2024-01-15T10:00:00Z", got)
	}
	if got := attrs["description"].(types.String).ValueString(); got != "used by the CI pipeline" {
		t.Errorf("description = %q, want %q", got, "used by the CI pipeline")
	}
	if got := attrs["issuer_id"].(types.String).ValueString(); got != "fdis_01ISSUER" {
		t.Errorf("issuer_id = %q, want fdis_01ISSUER", got)
	}
	if got := attrs["name"].(types.String).ValueString(); got != "ci-runner-rule" {
		t.Errorf("name = %q, want ci-runner-rule", got)
	}
	if got := attrs["token_lifetime_seconds"].(types.Int64).ValueInt64(); got != 3600 {
		t.Errorf("token_lifetime_seconds = %d, want 3600", got)
	}
	if got := attrs["workspace_id"].(types.String).ValueString(); got != "wrkspc_01ABC" {
		t.Errorf("workspace_id = %q, want wrkspc_01ABC", got)
	}

	matchObj := attrs["match"].(types.Object).Attributes()
	if got := matchObj["audience"].(types.String).ValueString(); got != "https://anthropic.com" {
		t.Errorf("match.audience = %q, want https://anthropic.com", got)
	}
	if got := matchObj["subject_prefix"].(types.String).ValueString(); got != "repo:ippontech/terraform-provider-anthropic:*" {
		t.Errorf("match.subject_prefix = %q, want the configured prefix", got)
	}
	if matchObj["claims"].(types.Map).IsNull() {
		t.Error("match.claims should not be null when the API returns entries")
	}

	targetObj := attrs["target"].(types.Object).Attributes()
	if got := targetObj["service_account_id"].(types.String).ValueString(); got != "svac_01ABC" {
		t.Errorf("target.service_account_id = %q, want svac_01ABC", got)
	}
	if got := targetObj["service_account_name"].(types.String).ValueString(); got != "ci-runner" {
		t.Errorf("target.service_account_name = %q, want ci-runner", got)
	}
}

func TestMapFederationRulesListEntry_emptyOptionalFieldsMapToNull(t *testing.T) {
	rule := anthropic.BetaFederationRule{
		ID:               "fdrl_02DEF",
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
		CreatedByActorID: "user_01ABC",
		UpdatedByActorID: "user_01ABC",
		IssuerID:         "fdis_01ISSUER",
		Name:             "no-frills-rule",
		OAuthScope:       "workspace:developer",
		Target: anthropic.BetaServiceAccountTarget{
			ServiceAccountID: "svac_01ABC",
			Type:             "service_account",
		},
		// ArchivedAt, ArchivedByActorID, Attributes, Description, WorkspaceID all zero/empty.
	}

	value, diags := mapFederationRulesListEntry(context.Background(), rule)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	attrs := value.(types.Object).Attributes()

	if !attrs["archived_at"].(types.String).IsNull() {
		t.Errorf("archived_at should be null for a live rule, got %q", attrs["archived_at"].(types.String).ValueString())
	}
	if !attrs["archived_by_actor_id"].(types.String).IsNull() {
		t.Error("archived_by_actor_id should be null for a live rule")
	}
	if !attrs["description"].(types.String).IsNull() {
		t.Error("description should be null when the API returns an empty string")
	}
	if !attrs["workspace_id"].(types.String).IsNull() {
		t.Error("workspace_id should be null when the API returns an empty string")
	}
	if !attrs["attributes"].(types.Map).IsNull() {
		t.Error("attributes should be null when the API returns no entries")
	}

	matchObj := attrs["match"].(types.Object).Attributes()
	if !matchObj["audience"].(types.String).IsNull() {
		t.Error("match.audience should be null when the API returns an empty string")
	}
	if !matchObj["claims"].(types.Map).IsNull() {
		t.Error("match.claims should be null when the API returns no entries")
	}

	targetObj := attrs["target"].(types.Object).Attributes()
	if !targetObj["service_account_name"].(types.String).IsNull() {
		t.Error("target.service_account_name should be null when the API returns an empty string")
	}
}

func newTestFederationClient(t *testing.T, srv *httptest.Server) *anthropic.Client {
	t.Helper()
	c := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAuthToken("test"))
	return &c
}

const federationRuleJSONTemplate = `{
	"id": "%s",
	"applies_to_all_workspaces": false,
	"archived_at": null,
	"archived_by_actor_id": "",
	"attributes": null,
	"created_at": "2024-01-15T10:00:00Z",
	"created_by_actor_id": "user_01ABC",
	"description": "",
	"issuer_id": "fdis_01ISSUER",
	"issuer_name": "GitHub Actions",
	"match": {
		"audience": null,
		"claims": null,
		"condition": null,
		"subject_prefix": "repo:ippontech/*"
	},
	"name": "%s",
	"oauth_scope": "workspace:developer",
	"target": {
		"type": "service_account",
		"service_account_id": "svac_01ABC",
		"service_account_name": null
	},
	"token_lifetime_seconds": 3600,
	"type": "federation_rule",
	"updated_at": "2024-01-15T10:00:00Z",
	"updated_by_actor_id": "user_01ABC",
	"workspace_id": "",
	"workspace_ids": []
}`

// TestFederationRulesList_paginatesAndForwardsQueryParams exercises the SDK
// call the Read method makes (BetaOrganizationFederationRuleListParams via
// ListAutoPaging) against a fake two-page server, asserting both that every
// page is fetched and that issuer_id/include_archived are forwarded only on
// the first request (the second request carries the opaque page cursor
// instead).
func TestFederationRulesList_paginatesAndForwardsQueryParams(t *testing.T) {
	var gotQueries []url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQueries = append(gotQueries, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("page") == "" {
			_, _ = io.WriteString(w, `{"data":[`+sprintfRule("fdrl_01ABC", "rule-one")+`],"next_page":"cursor-2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[`+sprintfRule("fdrl_02DEF", "rule-two")+`],"next_page":""}`)
	}))
	defer srv.Close()

	client := newTestFederationClient(t, srv)

	pager := client.Beta.Organization.Federation.Rules.ListAutoPaging(context.Background(), anthropic.BetaOrganizationFederationRuleListParams{
		IssuerID:        param.NewOpt("fdis_01ISSUER"),
		IncludeArchived: param.NewOpt(true),
	})

	var ids []string
	for pager.Next() {
		ids = append(ids, pager.Current().ID)
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pagination error: %v", err)
	}

	if len(ids) != 2 || ids[0] != "fdrl_01ABC" || ids[1] != "fdrl_02DEF" {
		t.Fatalf("expected 2 rules across 2 pages, got %v", ids)
	}

	if len(gotQueries) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(gotQueries))
	}
	if got := gotQueries[0].Get("issuer_id"); got != "fdis_01ISSUER" {
		t.Errorf("first request issuer_id = %q, want fdis_01ISSUER", got)
	}
	if got := gotQueries[0].Get("include_archived"); got != "true" {
		t.Errorf("first request include_archived = %q, want true", got)
	}
	if got := gotQueries[1].Get("page"); got != "cursor-2" {
		t.Errorf("second request page = %q, want cursor-2", got)
	}
}

func TestFederationRulesList_emptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[],"next_page":""}`)
	}))
	defer srv.Close()

	client := newTestFederationClient(t, srv)

	pager := client.Beta.Organization.Federation.Rules.ListAutoPaging(context.Background(), anthropic.BetaOrganizationFederationRuleListParams{})

	count := 0
	for pager.Next() {
		count++
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pagination error: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rules, got %d", count)
	}
}

func sprintfRule(id, name string) string {
	return fmt.Sprintf(federationRuleJSONTemplate, id, name)
}
