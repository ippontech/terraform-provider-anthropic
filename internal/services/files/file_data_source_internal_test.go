// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package files

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

func TestMapFileMetadataToModel_withExpiry(t *testing.T) {
	file := &anthropic.FileMetadata{
		ID:           "file_01ABC",
		CreatedAt:    time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC),
		Filename:     "report.txt",
		MimeType:     "text/plain",
		SizeBytes:    42,
		Type:         "file",
		Downloadable: true,
		ExpiresAt:    time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
	}

	m := mapFileMetadataToModel(file)

	if got := m.ID.ValueString(); got != "file_01ABC" {
		t.Errorf("id = %q", got)
	}
	if got := m.Filename.ValueString(); got != "report.txt" {
		t.Errorf("filename = %q", got)
	}
	if got := m.MimeType.ValueString(); got != "text/plain" {
		t.Errorf("mime_type = %q", got)
	}
	if got := m.SizeBytes.ValueInt64(); got != 42 {
		t.Errorf("size_bytes = %d", got)
	}
	if got := m.CreatedAt.ValueString(); got != "2026-09-01T10:00:00Z" {
		t.Errorf("created_at = %q", got)
	}
	if !m.Downloadable.ValueBool() {
		t.Error("downloadable = false, want true")
	}
	if got := m.ExpiresAt.ValueString(); got != "2026-09-02T10:00:00Z" {
		t.Errorf("expires_at = %q", got)
	}
	if got := m.Type.ValueString(); got != "file" {
		t.Errorf("type = %q", got)
	}
}

func TestMapFileMetadataToModel_nullExpiry(t *testing.T) {
	m := mapFileMetadataToModel(&anthropic.FileMetadata{ID: "file_01ABC", Filename: "a.txt"})
	if !m.ExpiresAt.IsNull() {
		t.Errorf("expires_at = %v, want null", m.ExpiresAt)
	}
}

func TestFileDataSourceRead_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/files/file_01ABC" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"file_01ABC","type":"file","filename":"a.txt","mime_type":"text/plain","size_bytes":7,"created_at":"2026-09-01T10:00:00Z","downloadable":false,"expires_at":null}`))
	}))
	defer srv.Close()

	resp := readFileDataSource(t, &FileDataSource{client: oauthtest.NewSDKClient(t, srv)}, "file_01ABC")
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	var got fileDataModel
	resp.Diagnostics.Append(resp.State.Get(context.Background(), &got)...)
	if got.Filename.ValueString() != "a.txt" || got.SizeBytes.ValueInt64() != 7 || !got.ExpiresAt.IsNull() {
		t.Errorf("unexpected state: %+v", got)
	}
}

func TestFileDataSourceRead_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"not_found_error","message":"File not found"}}`))
	}))
	defer srv.Close()

	resp := readFileDataSource(t, &FileDataSource{client: oauthtest.NewSDKClient(t, srv)}, "file_missing")
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic for a 404")
	}
	if got := resp.Diagnostics.Errors()[0].Summary(); got != "File Not Found" {
		t.Errorf("summary = %q, want File Not Found", got)
	}
}

func readFileDataSource(t *testing.T, d *FileDataSource, id string) *datasource.ReadResponse {
	t.Helper()

	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", schemaResp.Diagnostics)
	}
	objType := schemaResp.Schema.Type().TerraformType(context.Background()).(tftypes.Object)

	vals := map[string]tftypes.Value{}
	for name, typ := range objType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	vals["id"] = tftypes.NewValue(tftypes.String, id)

	req := datasource.ReadRequest{Config: tfsdk.Config{
		Raw:    tftypes.NewValue(objType, vals),
		Schema: schemaResp.Schema,
	}}
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	d.Read(context.Background(), req, resp)
	return resp
}
