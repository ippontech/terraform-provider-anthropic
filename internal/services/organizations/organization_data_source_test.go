// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
)

const organizationFixture = `{
	"id": "12345678-1234-5678-1234-567812345678",
	"name": "Test Organization",
	"type": "organization"
}`

func TestOrganizationDataSource_read(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/organizations/me" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, organizationFixture)
	}))
	defer srv.Close()

	client := admintest.NewClient(t, srv)

	body, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/me", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var org organizationAPIResponse
	if err := json.Unmarshal(body, &org); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if org.ID != "12345678-1234-5678-1234-567812345678" {
		t.Errorf("ID = %q, want %q", org.ID, "12345678-1234-5678-1234-567812345678")
	}
	if org.Name != "Test Organization" {
		t.Errorf("Name = %q, want %q", org.Name, "Test Organization")
	}
	if org.Type != "organization" {
		t.Errorf("Type = %q, want %q", org.Type, "organization")
	}
}

func TestOrganizationDataSource_error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"type":"authentication_error","message":"invalid admin key"}}`)
	}))
	defer srv.Close()

	client := admintest.NewClient(t, srv)

	_, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/me", nil)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	var apiErr *admin.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected *admin.APIError with status 401, got: %v", err)
	}
}
