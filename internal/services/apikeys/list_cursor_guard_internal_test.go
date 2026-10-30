// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
)

// cursorPage is one page of an Admin API list: has_more and last_id
// (https://platform.claude.com/docs/en/api/organization/users/list, same
// shape on every Admin list endpoint). A nil lastID is sent as JSON null.
type cursorPage struct {
	ids     []string
	hasMore bool
	lastID  *string
}

// maxCursorRequests ends the list once reached, so a loop that never stops
// fails the test instead of hanging it.
const maxCursorRequests = 5

// newCursorServer answers GET listPath with pages(after_id) and records the
// after_id of every request.
func newCursorServer(t *testing.T, listPath string, item func(id string) string, pages func(afterID string) cursorPage) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var afterIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != listPath {
			http.Error(w, "unexpected request "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
			return
		}
		mu.Lock()
		afterIDs = append(afterIDs, r.URL.Query().Get("after_id"))
		n := len(afterIDs)
		mu.Unlock()

		page := pages(r.URL.Query().Get("after_id"))
		if n >= maxCursorRequests {
			page.hasMore = false
		}
		items := make([]string, len(page.ids))
		for i, id := range page.ids {
			items[i] = item(id)
		}
		lastID := "null"
		if page.lastID != nil {
			lastID = fmt.Sprintf("%q", *page.lastID)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[%s],"has_more":%v,"first_id":null,"last_id":%s}`, strings.Join(items, ","), page.hasMore, lastID)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), afterIDs...)
	}
}

// readListDataSource runs d's real Read with a config holding overrides
// (every other attribute null) and returns the state and diagnostics.
func readListDataSource(t *testing.T, d datasource.DataSource, overrides map[string]tftypes.Value) (tfsdk.State, diag.Diagnostics) {
	t.Helper()
	ctx := context.Background()
	var schemaResp datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)
	objType := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)

	values := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, typ := range objType.AttributeTypes {
		values[name] = tftypes.NewValue(typ, nil)
	}
	maps.Copy(values, overrides)

	resp := datasource.ReadResponse{State: tfsdk.State{Raw: tftypes.NewValue(objType, nil), Schema: schemaResp.Schema}}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Raw: tftypes.NewValue(objType, values), Schema: schemaResp.Schema}}, &resp)
	return resp.State, resp.Diagnostics
}

// The list loop follows last_id through the real Read, and stops with an
// error instead of requesting the first page again when has_more comes with
// a null last_id or with the cursor it was just sent.
func TestListDataSources_cursorGuard(t *testing.T) {
	type listCase struct {
		name       string
		listPath   string
		listAttr   string
		dataSource func(*httptest.Server) datasource.DataSource
		overrides  map[string]tftypes.Value
		item       func(id string) string
	}
	lists := []listCase{
		{
			name:     "api_keys",
			listPath: "/v1/organizations/api_keys",
			listAttr: "api_keys",
			dataSource: func(srv *httptest.Server) datasource.DataSource {
				return &APIKeysDataSource{adminClient: admintest.NewClient(t, srv)}
			},
			overrides: nil,
			item: func(id string) string {
				return fmt.Sprintf(`{"id":%q,"name":"key","status":"active","workspace_id":null,"created_at":"2026-01-01T00:00:00Z","created_by":{"id":"user_1","type":"user"},"partial_key_hint":"abcd","type":"api_key"}`, id)
			},
		},
	}

	scenarios := []struct {
		name         string
		pages        func(afterID string) cursorPage
		wantAfterIDs []string
		wantItems    int // -1: want an error diagnostic instead
	}{
		{
			name: "two pages",
			pages: func(afterID string) cursorPage {
				if afterID == "" {
					return cursorPage{ids: []string{"id_1"}, hasMore: true, lastID: new("id_1")}
				}
				return cursorPage{ids: []string{"id_2"}, lastID: new("id_2")}
			},
			wantAfterIDs: []string{"", "id_1"},
			wantItems:    2,
		},
		{
			name: "has_more with a null last_id",
			pages: func(string) cursorPage {
				return cursorPage{ids: []string{"id_1"}, hasMore: true}
			},
			wantAfterIDs: []string{""},
			wantItems:    -1,
		},
		{
			name: "has_more with a repeated last_id",
			pages: func(string) cursorPage {
				return cursorPage{ids: []string{"id_1"}, hasMore: true, lastID: new("id_1")}
			},
			wantAfterIDs: []string{"", "id_1"},
			wantItems:    -1,
		},
	}

	for _, lc := range lists {
		for _, sc := range scenarios {
			t.Run(lc.name+"/"+sc.name, func(t *testing.T) {
				srv, afterIDs := newCursorServer(t, lc.listPath, lc.item, sc.pages)
				state, diags := readListDataSource(t, lc.dataSource(srv), lc.overrides)

				if got := afterIDs(); !reflect.DeepEqual(got, sc.wantAfterIDs) {
					t.Errorf("after_id of each request = %q, want %q", got, sc.wantAfterIDs)
				}
				if sc.wantItems < 0 {
					if !diags.HasError() {
						t.Fatal("want an error diagnostic, got none")
					}
					if detail := diags.Errors()[0].Detail(); !strings.Contains(detail, "has_more") {
						t.Errorf("error detail = %q, want it to mention has_more", detail)
					}
					return
				}
				if diags.HasError() {
					t.Fatalf("Read: %v", diags)
				}
				var list types.List
				if d := state.GetAttribute(context.Background(), path.Root(lc.listAttr), &list); d.HasError() {
					t.Fatalf("get %s: %v", lc.listAttr, d)
				}
				if got := len(list.Elements()); got != sc.wantItems {
					t.Errorf("len(%s) = %d, want %d", lc.listAttr, got, sc.wantItems)
				}
			})
		}
	}
}
