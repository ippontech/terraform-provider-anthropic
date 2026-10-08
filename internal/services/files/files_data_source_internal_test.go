// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package files

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

func fileJSON(id string) string {
	return fmt.Sprintf(`{"id":%q,"type":"file","filename":"%s.txt","mime_type":"text/plain","size_bytes":1,"created_at":"2026-09-01T10:00:00Z","downloadable":false,"expires_at":null}`, id, id)
}

// runFilesRead drives FilesDataSource.Read with the given optional ids config.
func runFilesRead(t *testing.T, srv *httptest.Server, ids []string) (*datasource.ReadResponse, FilesDataSourceModel) {
	t.Helper()

	d := &FilesDataSource{client: oauthtest.NewSDKClient(t, srv)}
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
	if ids != nil {
		elems := make([]tftypes.Value, len(ids))
		for i, id := range ids {
			elems[i] = tftypes.NewValue(tftypes.String, id)
		}
		vals["ids"] = tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, elems)
	}

	req := datasource.ReadRequest{Config: tfsdk.Config{
		Raw:    tftypes.NewValue(objType, vals),
		Schema: schemaResp.Schema,
	}}
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	d.Read(context.Background(), req, resp)

	var got FilesDataSourceModel
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Get(context.Background(), &got)...)
	}
	return resp, got
}

func TestFilesDataSourceRead_paginatesAcrossPages(t *testing.T) {
	var mu sync.Mutex
	var queries []map[string][]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.Query())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "" {
			_, _ = fmt.Fprintf(w, `{"data":[%s],"has_more":true,"next_page":"page_2"}`, fileJSON("file_a"))
			return
		}
		_, _ = fmt.Fprintf(w, `{"data":[%s],"has_more":false,"next_page":null}`, fileJSON("file_b"))
	}))
	defer srv.Close()

	resp, got := runFilesRead(t, srv, nil)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if n := len(got.Files.Elements()); n != 2 {
		t.Fatalf("files length = %d, want 2", n)
	}
	if len(queries) != 2 {
		t.Fatalf("requests = %d, want 2", len(queries))
	}
	if got := queries[0]["limit"]; len(got) != 1 || got[0] != "1000" {
		t.Errorf("first request limit = %v, want [1000]", got)
	}
	if _, ok := queries[0]["page"]; ok {
		t.Errorf("first request must not send page, got %v", queries[0]["page"])
	}
	if got := queries[1]["page"]; len(got) != 1 || got[0] != "page_2" {
		t.Errorf("second request page = %v, want [page_2]", got)
	}
	if got := queries[1]["limit"]; len(got) != 1 || got[0] != "1000" {
		t.Errorf("second request limit = %v, want [1000]", got)
	}
}

func TestFilesDataSourceRead_idsFilter(t *testing.T) {
	var query map[string][]string
	var rawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		rawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[%s],"has_more":false,"next_page":null}`, fileJSON("file_a"))
	}))
	defer srv.Close()

	resp, got := runFilesRead(t, srv, []string{"file_a", "file_b"})
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if n := len(got.Files.Elements()); n != 1 {
		t.Errorf("files length = %d, want 1", n)
	}
	if _, ok := query["limit"]; ok {
		t.Errorf("limit must not be sent with ids: %s", rawQuery)
	}
	if _, ok := query["page"]; ok {
		t.Errorf("page must not be sent with ids: %s", rawQuery)
	}
	// The SDK serialises arrays in bracket form (ids[]=a&ids[]=b).
	if ids := query["ids[]"]; len(ids) != 2 || ids[0] != "file_a" || ids[1] != "file_b" {
		t.Errorf("ids[] = %v (raw query %q), want [file_a file_b]", ids, rawQuery)
	}
}

func TestFilesDataSourceRead_emptyListIsNotNull(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"has_more":false,"next_page":null}`))
	}))
	defer srv.Close()

	resp, got := runFilesRead(t, srv, nil)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if got.Files.IsNull() {
		t.Fatal("files is null, want an empty list")
	}
	if n := len(got.Files.Elements()); n != 0 {
		t.Errorf("files length = %d, want 0", n)
	}
}

func TestFilesDataSourceRead_apiError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`))
	}))
	defer srv.Close()

	resp, _ := runFilesRead(t, srv, nil)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic")
	}
}
