// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
)

func makeAPIKeyListPage(keys []apiKeyAPIResponse, hasMore bool, lastID string) string {
	data, _ := json.Marshal(keys)
	return fmt.Sprintf(`{"data":%s,"has_more":%v,"first_id":"","last_id":%q}`, data, hasMore, lastID)
}

// runAPIKeysRead drives APIKeysDataSource.Read with the given string filters
// (attribute name → value) set in the configuration.
func runAPIKeysRead(t *testing.T, srv *httptest.Server, filters map[string]string) (*datasource.ReadResponse, APIKeysDataSourceModel) {
	t.Helper()

	d := &APIKeysDataSource{adminClient: admintest.NewClient(t, srv)}
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
	for name, v := range filters {
		vals[name] = tftypes.NewValue(tftypes.String, v)
	}

	req := datasource.ReadRequest{Config: tfsdk.Config{
		Raw:    tftypes.NewValue(objType, vals),
		Schema: schemaResp.Schema,
	}}
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	d.Read(context.Background(), req, resp)

	var got APIKeysDataSourceModel
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Get(context.Background(), &got)...)
	}
	return resp, got
}

func TestAPIKeysDataSourceRead_paginates(t *testing.T) {
	wsID := "wrkspc_01WS"
	page1 := []apiKeyAPIResponse{
		{ID: "apikey_01A", Name: "Key A", Status: "active", WorkspaceID: &wsID, CreatedBy: &apiKeyCreatedBy{ID: "u", Type: "user"}, Type: "api_key"},
	}
	page2 := []apiKeyAPIResponse{
		{ID: "apikey_01B", Name: "Key B", Status: "active", WorkspaceID: &wsID, CreatedBy: &apiKeyCreatedBy{ID: "u", Type: "user"}, Type: "api_key"},
	}

	var queries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/api_keys" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		queries = append(queries, r.URL.Query())
		w.Header().Set("content-type", "application/json")
		if r.URL.Query().Get("after_id") == "" {
			_, _ = io.WriteString(w, makeAPIKeyListPage(page1, true, "apikey_01A"))
			return
		}
		_, _ = io.WriteString(w, makeAPIKeyListPage(page2, false, ""))
	}))
	defer srv.Close()

	resp, got := runAPIKeysRead(t, srv, nil)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	if n := len(got.APIKeys.Elements()); n != 2 {
		t.Errorf("api_keys length = %d, want 2", n)
	}
	if len(queries) != 2 {
		t.Fatalf("requests = %d, want 2", len(queries))
	}
	if got := queries[1].Get("after_id"); got != "apikey_01A" {
		t.Errorf("second request after_id = %q, want apikey_01A", got)
	}
}

func TestAPIKeysDataSourceRead_filtersAndNewFields(t *testing.T) {
	var query url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{
			"id": "apikey_01A", "name": "Key A", "status": "expired", "workspace_id": null,
			"scope": {"type": "workspace", "workspace_id": "wrkspc_01DEFAULT"},
			"principal": {"type": "user_actor", "user_id": "user_01X"},
			"expires_at": "2026-10-01T00:00:00Z", "created_at": "2026-01-01T00:00:00Z",
			"created_by": {"id": "user_01X", "type": "user"}, "partial_key_hint": "aaaa", "type": "api_key"
		}],"has_more":false,"first_id":"apikey_01A","last_id":"apikey_01A"}`)
	}))
	defer srv.Close()

	resp, got := runAPIKeysRead(t, srv, map[string]string{
		"status":             "expired",
		"workspace_id":       "wrkspc_01DEFAULT",
		"created_by_user_id": "user_01X",
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}

	for key, want := range map[string]string{
		"limit":              "1000",
		"status":             "expired",
		"workspace_id":       "wrkspc_01DEFAULT",
		"created_by_user_id": "user_01X",
	} {
		if got := query.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}

	elems := got.APIKeys.Elements()
	if len(elems) != 1 {
		t.Fatalf("api_keys length = %d, want 1", len(elems))
	}
	item, ok := elems[0].(types.Object)
	if !ok {
		t.Fatalf("list item is %T, want types.Object", elems[0])
	}
	attrs := item.Attributes()
	if ws := attrs["workspace_id"].(types.String); !ws.IsNull() {
		t.Errorf("workspace_id = %v, want null", ws)
	}
	if got := objectString(t, attrs["scope"].(types.Object), "workspace_id").ValueString(); got != "wrkspc_01DEFAULT" {
		t.Errorf("scope.workspace_id = %q, want wrkspc_01DEFAULT", got)
	}
	if got := objectString(t, attrs["principal"].(types.Object), "user_id").ValueString(); got != "user_01X" {
		t.Errorf("principal.user_id = %q, want user_01X", got)
	}
	if got := attrs["expires_at"].(types.String).ValueString(); got != "2026-10-01T00:00:00Z" {
		t.Errorf("expires_at = %q, want 2026-10-01T00:00:00Z", got)
	}
	if got := attrs["status"].(types.String).ValueString(); got != "expired" {
		t.Errorf("status = %q, want expired", got)
	}
}

func TestBuildAPIKeysQuery_omitsUnsetFilters(t *testing.T) {
	data := APIKeysDataSourceModel{
		Status:          types.StringNull(),
		WorkspaceID:     types.StringUnknown(),
		CreatedByUserID: types.StringNull(),
	}
	q := buildAPIKeysQuery(&data, "")
	if got := q.Encode(); got != "limit=1000" {
		t.Errorf("query = %q, want limit=1000", got)
	}
}

// The list filter accepts every status the API reports, expired included.
func TestAPIKeysDataSource_statusFilterValidator(t *testing.T) {
	d := &APIKeysDataSource{}
	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
	attrSchema, ok := schemaResp.Schema.Attributes["status"].(interface{ StringValidators() []validator.String })
	if !ok {
		t.Fatal("status attribute has no string validators")
	}

	for value, wantErr := range map[string]bool{
		"active":   false,
		"inactive": false,
		"archived": false,
		"expired":  false,
		"deleted":  true,
	} {
		var resp validator.StringResponse
		for _, v := range attrSchema.StringValidators() {
			v.ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringValue(value)}, &resp)
		}
		if got := resp.Diagnostics.HasError(); got != wantErr {
			t.Errorf("status %q: error = %v, want %v", value, got, wantErr)
		}
	}
}

func TestMapAPIKeyToListObject_withWorkspaceID(t *testing.T) {
	wsID := "wrkspc_01WS"
	key := &apiKeyAPIResponse{
		ID:             "apikey_01ABC",
		Name:           "Test Key",
		Status:         "active",
		WorkspaceID:    &wsID,
		Scope:          &apiKeyScope{Type: "workspace", WorkspaceID: wsID},
		CreatedAt:      "2026-01-01T00:00:00Z",
		CreatedBy:      &apiKeyCreatedBy{ID: "user_01X", Type: "user"},
		PartialKeyHint: "abcd",
		Type:           "api_key",
	}

	obj, diags := mapAPIKeyToListObject(context.Background(), key)
	if diags.HasError() {
		t.Fatalf("mapAPIKeyToListObject error: %v", diags)
	}
	typedObj, ok := obj.(types.Object)
	if !ok {
		t.Fatalf("expected types.Object, got %T", obj)
	}
	if got := typedObj.Attributes()["workspace_id"].(types.String).ValueString(); got != wsID {
		t.Errorf("workspace_id = %q, want %q", got, wsID)
	}
	if p := typedObj.Attributes()["principal"].(types.Object); !p.IsNull() {
		t.Errorf("principal = %v, want null", p)
	}
}

func TestMapAPIKeyToListObject_nullWorkspaceID(t *testing.T) {
	key := &apiKeyAPIResponse{
		ID:             "apikey_01ABC",
		Name:           "Org Key",
		Status:         "active",
		WorkspaceID:    nil,
		CreatedAt:      "2026-01-01T00:00:00Z",
		CreatedBy:      &apiKeyCreatedBy{ID: "user_01X", Type: "user"},
		PartialKeyHint: "abcd",
		Type:           "api_key",
	}

	obj, diags := mapAPIKeyToListObject(context.Background(), key)
	if diags.HasError() {
		t.Fatalf("mapAPIKeyToListObject error: %v", diags)
	}
	if obj == nil {
		t.Fatal("expected non-nil object")
	}

	// Cast to types.Object to inspect workspace_id
	typedObj, ok := obj.(types.Object)
	if !ok {
		t.Fatalf("expected types.Object, got %T", obj)
	}
	wsIDVal, exists := typedObj.Attributes()["workspace_id"]
	if !exists {
		t.Fatal("workspace_id attribute missing")
	}
	wsIDStr, ok := wsIDVal.(types.String)
	if !ok {
		t.Fatalf("workspace_id is not types.String, got %T", wsIDVal)
	}
	if !wsIDStr.IsNull() {
		t.Errorf("workspace_id should be null, got %q", wsIDStr.ValueString())
	}
}
