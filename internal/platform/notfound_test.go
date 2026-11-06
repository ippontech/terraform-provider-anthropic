// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package platform_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
	"github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/files"
)

const apiNotFound = `{"type":"error","error":{"type":"not_found_error","message":"File not found"},"request_id":"req_1"}`

// fixedServer answers every request with status and body, and counts requests.
func fixedServer(t *testing.T, status int, contentType, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// guardedClient is an SDK client as a gateway platform builds it: the guard
// installed, the SDK's default two retries left on.
func guardedClient(srv *httptest.Server, p platform.Platform) *anthropic.Client {
	c := anthropic.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(srv.URL),
		option.WithAPIKey("test"),
		option.WithMiddleware(platform.NotFoundGuard(p)),
	)
	return &c
}

// TestNotFoundGuardPassesTheAPIsNotFound: the API's own not_found_error is a
// real "object missing", and resources must keep seeing it as a 404.
func TestNotFoundGuardPassesTheAPIsNotFound(t *testing.T) {
	srv, hits := fixedServer(t, http.StatusNotFound, "application/json", apiNotFound)

	_, err := guardedClient(srv, platform.AWS).Files.GetMetadata(context.Background(), "file_1", anthropic.FileGetMetadataParams{})

	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("err = %v, want an *anthropic.Error with status 404", err)
	}
	var unserved *platform.UnservedRouteError
	if errors.As(err, &unserved) {
		t.Errorf("an API not_found_error was reported as an unserved route: %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("server hit %d times, want 1", n)
	}
}

// TestNotFoundGuardRejectsOther404s: any other 404 body is the gateway's, not
// the API's. It must not reach resources as a 404, and must not be retried.
func TestNotFoundGuardRejectsOther404s(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{"gateway json", "application/json", `{"message":"Not Found"}`},
		{"html page", "text/html", "<html><body>404 Not Found</body></html>"},
		{"empty body", "", ""},
		{"api envelope of another type", "application/json", `{"type":"error","error":{"type":"invalid_request_error","message":"x"}}`},
		{"not_found_error without the error envelope type", "application/json", `{"error":{"type":"not_found_error"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, p := range []platform.Platform{platform.AWS, platform.GoogleCloud} {
				srv, hits := fixedServer(t, http.StatusNotFound, tt.contentType, tt.body)

				_, err := guardedClient(srv, p).Files.GetMetadata(context.Background(), "file_1", anthropic.FileGetMetadataParams{})

				var unserved *platform.UnservedRouteError
				if !errors.As(err, &unserved) {
					t.Fatalf("%s: err = %v (%T), want *platform.UnservedRouteError", p, err, err)
				}
				var apiErr *anthropic.Error
				if errors.As(err, &apiErr) {
					t.Errorf("%s: the error also matches *anthropic.Error, so a status check could still read it as a deletion", p)
				}
				if unserved.Platform != p || unserved.Method != http.MethodGet || !strings.HasSuffix(unserved.URL, "/v1/files/file_1") {
					t.Errorf("%s: error fields = %+v", p, unserved)
				}
				for _, want := range []string{p.DisplayName(), p.DocURL(), "keeps the object in state"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("%s: message %q does not contain %q", p, err.Error(), want)
					}
				}
				if n := hits.Load(); n != 1 {
					t.Errorf("%s: server hit %d times, want 1: a gateway 404 must not be retried", p, n)
				}
			}
		})
	}
}

func TestNotFoundGuardTruncatesTheBody(t *testing.T) {
	srv, _ := fixedServer(t, http.StatusNotFound, "text/plain", strings.Repeat("x", 4096))

	_, err := guardedClient(srv, platform.AWS).Files.GetMetadata(context.Background(), "file_1", anthropic.FileGetMetadataParams{})

	var unserved *platform.UnservedRouteError
	if !errors.As(err, &unserved) {
		t.Fatalf("err = %v, want *platform.UnservedRouteError", err)
	}
	if len(unserved.Body) != 512 {
		t.Errorf("body excerpt is %d bytes, want 512", len(unserved.Body))
	}
}

// TestNotFoundGuardLeavesOtherStatusesAlone: only 404 is reinterpreted.
func TestNotFoundGuardLeavesOtherStatusesAlone(t *testing.T) {
	t.Run("403", func(t *testing.T) {
		srv, _ := fixedServer(t, http.StatusForbidden, "application/json", `{"message":"User is not authorized"}`)
		_, err := guardedClient(srv, platform.AWS).Files.GetMetadata(context.Background(), "file_1", anthropic.FileGetMetadataParams{})
		var apiErr *anthropic.Error
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
			t.Fatalf("err = %v, want an *anthropic.Error with status 403", err)
		}
	})
	t.Run("200", func(t *testing.T) {
		srv, _ := fixedServer(t, http.StatusOK, "application/json",
			`{"id":"file_1","type":"file","filename":"a.txt","mime_type":"text/plain","size_bytes":1,"created_at":"2026-10-25T00:00:00Z"}`)
		f, err := guardedClient(srv, platform.AWS).Files.GetMetadata(context.Background(), "file_1", anthropic.FileGetMetadataParams{})
		if err != nil || f.ID != "file_1" {
			t.Fatalf("GetMetadata = %+v, %v", f, err)
		}
	})
}

func TestIsAPINotFound(t *testing.T) {
	if !platform.IsAPINotFound([]byte(apiNotFound)) {
		t.Error("the API's not_found_error envelope was not recognised")
	}
	for _, body := range []string{"", "null", "[]", `{"type":"error"}`, `{"type":"error","error":{"type":"not_found"}}`} {
		if platform.IsAPINotFound([]byte(body)) {
			t.Errorf("IsAPINotFound(%q) = true", body)
		}
	}
}

// TestNotFoundGuardKeepsTheResourceInState drives a real resource whose Read
// drops the object from state on a 404 (anthropic_file), through a guarded
// client: a gateway 404 is reported and the object stays; the API's
// not_found_error still removes it.
func TestNotFoundGuardKeepsTheResourceInState(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantRemoved bool
	}{
		{"gateway 404 keeps the object", `{"message":"Not Found"}`, false},
		{"API not_found_error removes it", apiNotFound, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := fixedServer(t, http.StatusNotFound, "application/json", tt.body)
			r := files.NewFileResource()
			cr, ok := r.(resource.ResourceWithConfigure)
			if !ok {
				t.Fatal("anthropic_file does not implement Configure")
			}
			var configureResp resource.ConfigureResponse
			cr.Configure(context.Background(), resource.ConfigureRequest{ProviderData: &providerdata.ProviderData{
				Platform: platform.AWS,
				Client:   guardedClient(srv, platform.AWS),
			}}, &configureResp)
			if configureResp.Diagnostics.HasError() {
				t.Fatalf("Configure: %v", configureResp.Diagnostics)
			}

			values := schematest.NullValues(t, r)
			values["id"] = tftypes.NewValue(tftypes.String, "file_1")
			state := tfsdk.State{
				Schema: schematest.ResourceSchema(t, r),
				Raw:    tftypes.NewValue(schematest.ResourceObjectType(t, r), values),
			}
			resp := &resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, resp)

			if removed := resp.State.Raw.IsNull(); removed != tt.wantRemoved {
				t.Fatalf("removed from state = %v, want %v (diagnostics: %v)", removed, tt.wantRemoved, resp.Diagnostics)
			}
			if !tt.wantRemoved {
				if !resp.Diagnostics.HasError() {
					t.Fatal("a gateway 404 kept the object but reported nothing")
				}
				if detail := resp.Diagnostics.Errors()[0].Detail(); !strings.Contains(detail, "Claude Platform on AWS") {
					t.Errorf("diagnostic %q does not name the platform", detail)
				}
			}
		})
	}
}
