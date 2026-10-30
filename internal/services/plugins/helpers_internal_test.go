// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// The Plugins API is available to Claude Enterprise organizations only, and
// the provider's test organization is a Claude Console one, so this package
// has no acceptance tests: the resources and data sources are covered by these
// httptest unit tests and by `command = plan` / mock_provider native tests.
package plugins

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

// recordedRequest is one request the fake API received.
type recordedRequest struct {
	Method      string
	Path        string
	Query       url.Values
	Beta        string
	ContentType string
	Body        []byte
}

// fakeResponse is one canned answer of the fake API, sent after delay.
type fakeResponse struct {
	status int
	body   string
	header map[string]string
	delay  time.Duration
}

func ok(body string) fakeResponse { return fakeResponse{status: http.StatusOK, body: body} }

// apiError builds an error answer in the API's envelope, with optional
// details (a JSON object) and headers.
func apiError(status int, errType, message, details string, header map[string]string) fakeResponse {
	if details == "" {
		details = "null"
	}
	return fakeResponse{
		status: status,
		body: fmt.Sprintf(`{"type":"error","error":{"type":%q,"message":%q,"details":%s},"request_id":"req_01"}`,
			errType, message, details),
		header: header,
	}
}

// fakePluginsAPI starts a server that answers each "METHOD /path" route with
// its responses in turn (repeating the last one) and records every request.
// A request to any other route fails the test. The client retries like the
// production one, with millisecond delays.
func fakePluginsAPI(t *testing.T, routes map[string][]fakeResponse) (*admin.Client, func() []recordedRequest) {
	t.Helper()
	var (
		mu       sync.Mutex
		requests []recordedRequest
		served   = map[string]int{}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Method + " " + r.URL.Path

		mu.Lock()
		requests = append(requests, recordedRequest{
			Method:      r.Method,
			Path:        r.URL.Path,
			Query:       r.URL.Query(),
			Beta:        r.Header.Get("anthropic-beta"),
			ContentType: r.Header.Get("content-type"),
			Body:        body,
		})
		responses := routes[key]
		i := served[key]
		served[key]++
		mu.Unlock()

		if len(responses) == 0 {
			t.Errorf("unexpected request %s", key)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		resp := responses[min(i, len(responses)-1)]
		time.Sleep(resp.delay)
		for k, v := range resp.header {
			w.Header().Set(k, v)
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(resp.status)
		_, _ = io.WriteString(w, resp.body)
	}))
	t.Cleanup(srv.Close)

	client := admintest.NewClient(t, srv)
	client.MaxRetries = admin.DefaultMaxRetries
	client.BaseRetryDelay = time.Millisecond
	client.MaxRetryDelay = time.Millisecond

	return client, func() []recordedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedRequest(nil), requests...)
	}
}

// assertPluginsBeta checks that every request carried the beta header and
// the beta=true query flag: without the header the API answers 404.
func assertPluginsBeta(t *testing.T, requests []recordedRequest) {
	t.Helper()
	for _, r := range requests {
		if r.Beta != pluginsBeta {
			t.Errorf("%s %s: anthropic-beta = %q, want %q", r.Method, r.Path, r.Beta, pluginsBeta)
		}
		if r.Query.Get("beta") != "true" {
			t.Errorf("%s %s: query beta = %q, want true", r.Method, r.Path, r.Query.Get("beta"))
		}
	}
}

// --- Framework plumbing ---

func planOf(t *testing.T, r resource.Resource, model any) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: schematest.ResourceSchema(t, r)}
	if diags := plan.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("building plan: %v", diags)
	}
	return plan
}

func stateOf(t *testing.T, r resource.Resource, model any) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: schematest.ResourceSchema(t, r)}
	if diags := state.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("building state: %v", diags)
	}
	return state
}

func configOf(t *testing.T, r resource.Resource, model any) tfsdk.Config {
	t.Helper()
	plan := planOf(t, r, model)
	return tfsdk.Config(plan)
}

func emptyState(t *testing.T, r resource.Resource) tfsdk.State {
	t.Helper()
	return tfsdk.State{Schema: schematest.ResourceSchema(t, r)}
}

// readDataSource runs d's Read with a configuration whose attributes are null
// except those in set.
func readDataSource(t *testing.T, d datasource.DataSource, set map[string]tftypes.Value) datasource.ReadResponse {
	t.Helper()
	ctx := context.Background()
	var schemaResp datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", schemaResp.Diagnostics)
	}
	objType := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	vals := map[string]tftypes.Value{}
	for name, typ := range objType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	maps.Copy(vals, set)

	config := tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objType, vals)}
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	d.Read(ctx, datasource.ReadRequest{Config: config}, &resp)
	return resp
}

func str(s string) tftypes.Value { return tftypes.NewValue(tftypes.String, s) }

func stateString(t *testing.T, resp datasource.ReadResponse, p path.Path) types.String {
	t.Helper()
	var v types.String
	if diags := resp.State.GetAttribute(context.Background(), p, &v); diags.HasError() {
		t.Fatalf("reading %s: %v", p, diags)
	}
	return v
}

func stateList(t *testing.T, resp datasource.ReadResponse, name string) types.List {
	t.Helper()
	var v types.List
	if diags := resp.State.GetAttribute(context.Background(), path.Root(name), &v); diags.HasError() {
		t.Fatalf("reading %s: %v", name, diags)
	}
	return v
}

// --- Fixtures ---

// pluginJSON is a plugin as the API returns it, serving servedVersionID.
func pluginJSON(servedVersionID string, pinned bool) string {
	return fmt.Sprintf(`{
		"type": "plugin",
		"id": "plugin_01",
		"name": "sales-toolkit",
		"display_name": "Sales Toolkit",
		"description": "Account research and call prep.",
		"served_version_id": %q,
		"served_version_pinned": %t,
		"latest_version_id": "pluginver_02",
		"manifest_version": "1.4.0",
		"owner": {"type": "organization"},
		"marketplace_id": "marketplace_01",
		"created_by": {"type": "api_actor", "api_key_id": "apikey_01"},
		"organization_installation_preference": "available",
		"organization_installation_preference_inherited": true,
		"content_scan": {"status": "completed", "assessment": "pass", "reason": null},
		"components": [
			{"type": "skill", "name": "account-research", "description": "Researches a customer account."},
			{"type": "mcp_server", "name": "crm", "description": null}
		],
		"reach": "remote",
		"created_at": "2026-09-01T17:04:11Z",
		"updated_at": "2026-09-15T14:12:30Z"
	}`, servedVersionID, pinned)
}

// memberPluginJSON is a plugin in a member's personal marketplace: it has no
// installation settings, so both organization fields are null.
const memberPluginJSON = `{
	"type": "plugin",
	"id": "plugin_02",
	"name": "my-helpers",
	"display_name": null,
	"description": null,
	"served_version_id": "pluginver_10",
	"served_version_pinned": false,
	"latest_version_id": "pluginver_10",
	"manifest_version": null,
	"owner": {"type": "user", "user_id": "user_01"},
	"marketplace_id": "marketplace_02",
	"created_by": {"type": "user_actor", "user_id": "user_01", "email_address": "member@example.com"},
	"organization_installation_preference": null,
	"organization_installation_preference_inherited": null,
	"content_scan": null,
	"components": null,
	"reach": null,
	"created_at": "2026-08-20T15:12:00Z",
	"updated_at": "2026-08-20T15:12:00Z"
}`

const pluginVersionJSON = `{
	"type": "plugin_version",
	"id": "pluginver_03",
	"plugin_id": "plugin_01",
	"display_name": "Sales Toolkit",
	"description": "Account research and call prep.",
	"manifest_version": "1.5.0",
	"release_notes": "Adds the call-prep command.",
	"created_by": {"type": "user_actor", "user_id": "user_01", "email_address": null},
	"content_scan": {"status": "processing", "assessment": null, "reason": null},
	"components": [],
	"reach": null,
	"created_at": "2026-09-15T14:12:30Z"
}`
