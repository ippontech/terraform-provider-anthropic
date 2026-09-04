// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts

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
)

// newTestServiceAccountDataSourceClient builds a bare SDK client pointed at an
// httptest server. Named distinctly from the (sibling-branch) resource's
// newTestServiceAccountClient to avoid a symbol collision once both land in
// the same package.
func newTestServiceAccountDataSourceClient(t *testing.T, srv *httptest.Server) *anthropic.Client {
	t.Helper()
	c := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAuthToken("test"))
	return &c
}

func TestMapServiceAccountDataSourceToState_basicFields(t *testing.T) {
	createdAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	sa := &anthropic.BetaServiceAccount{
		ID:                "svac_01ABC",
		Name:              "ci-runner",
		Description:       "used by the CI pipeline",
		OrganizationRole:  anthropic.BetaServiceAccountOrganizationRoleDeveloper,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
		CreatedByActorID:  "user_01ABC",
		UpdatedByActorID:  "user_01ABC",
		ArchivedByActorID: "",
	}

	var data ServiceAccountDataSourceModel
	diags := mapServiceAccountDataSourceToState(sa, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if got := data.ID.ValueString(); got != "svac_01ABC" {
		t.Errorf("ID = %q, want svac_01ABC", got)
	}
	if got := data.Name.ValueString(); got != "ci-runner" {
		t.Errorf("Name = %q, want ci-runner", got)
	}
	if got := data.Description.ValueString(); got != "used by the CI pipeline" {
		t.Errorf("Description = %q, want %q", got, "used by the CI pipeline")
	}
	if got := data.OrganizationRole.ValueString(); got != "developer" {
		t.Errorf("OrganizationRole = %q, want developer", got)
	}
	if got := data.CreatedAt.ValueString(); got != "2024-01-15T10:00:00Z" {
		t.Errorf("CreatedAt = %q, want 2024-01-15T10:00:00Z", got)
	}
	if got := data.UpdatedAt.ValueString(); got != "2024-01-15T11:00:00Z" {
		t.Errorf("UpdatedAt = %q, want 2024-01-15T11:00:00Z", got)
	}
	if got := data.CreatedByActorID.ValueString(); got != "user_01ABC" {
		t.Errorf("CreatedByActorID = %q, want user_01ABC", got)
	}
	if !data.ArchivedByActorID.IsNull() {
		t.Errorf("ArchivedByActorID should be null when the API returns an empty string, got %q", data.ArchivedByActorID.ValueString())
	}
	if !data.ArchivedAt.IsNull() {
		t.Errorf("ArchivedAt should be null for a live service account, got %q", data.ArchivedAt.ValueString())
	}
}

func TestMapServiceAccountDataSourceToState_emptyDescriptionMapsToNull(t *testing.T) {
	sa := &anthropic.BetaServiceAccount{
		ID:               "svac_02DEF",
		Name:             "no-description",
		Description:      "",
		OrganizationRole: anthropic.BetaServiceAccountOrganizationRoleDeveloper,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	var data ServiceAccountDataSourceModel
	diags := mapServiceAccountDataSourceToState(sa, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if !data.Description.IsNull() {
		t.Errorf("expected Description to be null for an empty API string, got %q", data.Description.ValueString())
	}
}

func TestMapServiceAccountDataSourceToState_archivedFields(t *testing.T) {
	archivedAt := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

	sa := &anthropic.BetaServiceAccount{
		ID:                "svac_03GHI",
		Name:              "retired",
		OrganizationRole:  anthropic.BetaServiceAccountOrganizationRoleAdmin,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
		ArchivedAt:        archivedAt,
		ArchivedByActorID: "user_02XYZ",
	}

	var data ServiceAccountDataSourceModel
	diags := mapServiceAccountDataSourceToState(sa, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if data.ArchivedAt.IsNull() {
		t.Fatal("expected ArchivedAt to be non-null for an archived service account")
	}
	if got := data.ArchivedAt.ValueString(); got != "2024-06-01T12:00:00Z" {
		t.Errorf("ArchivedAt = %q, want 2024-06-01T12:00:00Z", got)
	}
	if got := data.ArchivedByActorID.ValueString(); got != "user_02XYZ" {
		t.Errorf("ArchivedByActorID = %q, want user_02XYZ", got)
	}
	if got := data.OrganizationRole.ValueString(); got != "admin" {
		t.Errorf("OrganizationRole = %q, want admin", got)
	}
}

func TestServiceAccountDataSource_readNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"service account not found"}}`)
	}))
	defer srv.Close()

	client := newTestServiceAccountDataSourceClient(t, srv)

	_, err := client.Beta.Organization.ServiceAccounts.Get(context.Background(), "svac_missing", anthropic.BetaOrganizationServiceAccountGetParams{})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	var apierr *anthropic.Error
	if !errors.As(err, &apierr) {
		t.Fatalf("expected *anthropic.Error, got: %T (%v)", err, err)
	}
	if apierr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", apierr.StatusCode, http.StatusNotFound)
	}
}

func TestServiceAccountDataSource_readSuccess(t *testing.T) {
	var gotPath, gotMethod string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{
			"id": "svac_01ABC",
			"name": "ci-runner",
			"description": "used by the CI pipeline",
			"organization_role": "developer",
			"created_at": "2024-01-15T10:00:00Z",
			"updated_at": "2024-01-15T10:00:00Z",
			"archived_at": "0001-01-01T00:00:00Z",
			"created_by_actor_id": "user_01ABC",
			"updated_by_actor_id": "user_01ABC",
			"archived_by_actor_id": "",
			"type": "service_account"
		}`)
	}))
	defer srv.Close()

	client := newTestServiceAccountDataSourceClient(t, srv)

	sa, err := client.Beta.Organization.ServiceAccounts.Get(context.Background(), "svac_01ABC", anthropic.BetaOrganizationServiceAccountGetParams{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	// The SDK appends "?beta=true" as a query string, not part of the path.
	if gotPath != "/v1/organizations/service_accounts/svac_01ABC" {
		t.Errorf("path = %q, want .../service_accounts/svac_01ABC", gotPath)
	}

	var data ServiceAccountDataSourceModel
	diags := mapServiceAccountDataSourceToState(sa, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if got := data.ID.ValueString(); got != "svac_01ABC" {
		t.Errorf("ID = %q, want svac_01ABC", got)
	}
	if data.ArchivedAt.ValueString() != "" && !data.ArchivedAt.IsNull() {
		t.Errorf("expected ArchivedAt to be null for a live service account, got %q", data.ArchivedAt.ValueString())
	}
}
