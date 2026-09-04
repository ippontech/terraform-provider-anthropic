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
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestMapServiceAccountToState_basicFields(t *testing.T) {
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

	var data ServiceAccountResourceModel
	diags := mapServiceAccountToState(sa, &data)
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

func TestMapServiceAccountToState_emptyDescriptionMapsToNull(t *testing.T) {
	sa := &anthropic.BetaServiceAccount{
		ID:               "svac_02DEF",
		Name:             "no-description",
		Description:      "",
		OrganizationRole: anthropic.BetaServiceAccountOrganizationRoleDeveloper,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	var data ServiceAccountResourceModel
	diags := mapServiceAccountToState(sa, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if !data.Description.IsNull() {
		t.Errorf("expected Description to be null for an empty API string, got %q", data.Description.ValueString())
	}
}

func TestMapServiceAccountToState_archivedFields(t *testing.T) {
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

	var data ServiceAccountResourceModel
	diags := mapServiceAccountToState(sa, &data)
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

func TestBuildServiceAccountCreateParams(t *testing.T) {
	t.Run("minimal", func(t *testing.T) {
		data := &ServiceAccountResourceModel{
			Name:             types.StringValue("ci-runner"),
			Description:      types.StringNull(),
			OrganizationRole: types.StringNull(),
		}

		params := buildServiceAccountCreateParams(data)

		if params.Name != "ci-runner" {
			t.Errorf("Name = %q, want ci-runner", params.Name)
		}
		if params.Description.Valid() {
			t.Errorf("Description should be omitted (zero value) when unset in config, got %+v", params.Description)
		}
		if params.OrganizationRole != "" {
			t.Errorf("OrganizationRole should be omitted (zero value) when unset in config, got %q", params.OrganizationRole)
		}
	})

	t.Run("full", func(t *testing.T) {
		data := &ServiceAccountResourceModel{
			Name:             types.StringValue("ci-runner"),
			Description:      types.StringValue("used by CI"),
			OrganizationRole: types.StringValue("admin"),
		}

		params := buildServiceAccountCreateParams(data)

		if !params.Description.Valid() || params.Description.Value != "used by CI" {
			t.Errorf("Description = %+v, want set to %q", params.Description, "used by CI")
		}
		if params.OrganizationRole != anthropic.BetaOrganizationServiceAccountNewParamsOrganizationRoleAdmin {
			t.Errorf("OrganizationRole = %q, want admin", params.OrganizationRole)
		}
	})
}

func TestBuildServiceAccountUpdateParams(t *testing.T) {
	t.Run("clears description when removed from config", func(t *testing.T) {
		plan := &ServiceAccountResourceModel{
			Description:      types.StringNull(),
			OrganizationRole: types.StringValue("developer"),
		}

		params := buildServiceAccountUpdateParams(plan)

		if param.IsOmitted(params.Description) {
			t.Fatal("Description should carry an explicit null, not be omitted")
		}
		if !param.IsNull(params.Description) {
			t.Errorf("expected Description to be an explicit null via param.Null[string]()")
		}
		if params.Description.Value != "" {
			t.Errorf("Description.Value = %q, want empty string for an explicit null", params.Description.Value)
		}
	})

	t.Run("sends description and organization_role when set", func(t *testing.T) {
		plan := &ServiceAccountResourceModel{
			Description:      types.StringValue("updated description"),
			OrganizationRole: types.StringValue("admin"),
		}

		params := buildServiceAccountUpdateParams(plan)

		if !params.Description.Valid() || params.Description.Value != "updated description" {
			t.Errorf("Description = %+v, want set to %q", params.Description, "updated description")
		}
		if params.OrganizationRole != anthropic.BetaOrganizationServiceAccountUpdateParamsOrganizationRoleAdmin {
			t.Errorf("OrganizationRole = %q, want admin", params.OrganizationRole)
		}
	})
}

func newTestServiceAccountClient(t *testing.T, srv *httptest.Server) *anthropic.Client {
	t.Helper()
	c := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAuthToken("test"))
	return &c
}

func TestServiceAccountResource_readNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"service account not found"}}`)
	}))
	defer srv.Close()

	client := newTestServiceAccountClient(t, srv)

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

func TestServiceAccountResource_archiveOnDelete(t *testing.T) {
	var gotPath, gotMethod string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{
			"id": "svac_01ABC",
			"name": "ci-runner",
			"description": "",
			"organization_role": "developer",
			"created_at": "2024-01-15T10:00:00Z",
			"updated_at": "2024-01-15T10:00:00Z",
			"archived_at": "2024-06-01T12:00:00Z",
			"created_by_actor_id": "user_01ABC",
			"updated_by_actor_id": "user_01ABC",
			"archived_by_actor_id": "user_01ABC",
			"type": "service_account"
		}`)
	}))
	defer srv.Close()

	client := newTestServiceAccountClient(t, srv)

	sa, err := client.Beta.Organization.ServiceAccounts.Archive(context.Background(), "svac_01ABC", anthropic.BetaOrganizationServiceAccountArchiveParams{})
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	// The SDK appends "?beta=true" as a query string, not part of the path.
	if gotPath != "/v1/organizations/service_accounts/svac_01ABC/archive" {
		t.Errorf("path = %q, want .../service_accounts/svac_01ABC/archive", gotPath)
	}
	if sa.ArchivedAt.IsZero() {
		t.Error("expected the archived service account to carry a non-zero archived_at")
	}
}
