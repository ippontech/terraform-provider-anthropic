// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

const (
	tunnelLiveJSON = `{
		"id": "tnl_01ABC",
		"type": "tunnel",
		"archived_at": null,
		"created_at": "2026-07-01T10:00:00Z",
		"display_name": "corp-mcp",
		"domain": "abc123.tunnel.anthropic.com"
	}`
	tunnelArchivedJSON = `{
		"id": "tnl_01ABC",
		"type": "tunnel",
		"archived_at": "2026-07-02T10:00:00Z",
		"created_at": "2026-07-01T10:00:00Z",
		"display_name": "corp-mcp",
		"domain": "abc123.tunnel.anthropic.com"
	}`
	tunnelTokenJSON   = `{"id": "ttok_01", "tunnel_token": "secret-token-1", "type": "tunnel_token"}`
	tunnelRotatedJSON = `{"id": "ttok_02", "tunnel_token": "secret-token-2", "type": "tunnel_token"}`
)

// fakeTunnelsAPI serves the tunnel endpoints from a route table keyed by
// "METHOD /path", recording every request.
func fakeTunnelsAPI(t *testing.T, routes map[string]func(w http.ResponseWriter)) (*httptest.Server, *requestLog) {
	t.Helper()
	log := &requestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		log.add(r, string(body))
		w.Header().Set("Content-Type", "application/json")
		if handler, ok := routes[r.Method+" "+r.URL.Path]; ok {
			handler(w)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, notFoundBody)
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func respond(status int, body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func TestTokenRotationRequested(t *testing.T) {
	str := types.StringValue
	null := types.StringNull()
	unknown := types.StringUnknown()

	tests := []struct {
		name        string
		plan, state types.String
		want        bool
	}{
		{"unchanged", str("1"), str("1"), false},
		{"changed", str("2"), str("1"), true},
		{"first value is a baseline", str("1"), null, false},
		{"removed", null, str("1"), false},
		{"both null", null, null, false},
		{"unknown against a value", unknown, str("1"), true},
		{"unknown against null", unknown, null, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tokenRotationRequested(tc.plan, tc.state); got != tc.want {
				t.Errorf("tokenRotationRequested(%v, %v) = %v, want %v", tc.plan, tc.state, got, tc.want)
			}
		})
	}
}

// TestTokenFollowsRotation drives the plan modifier through the framework
// request types: the token keeps its state value unless token_rotation
// changes between two non-null values.
func TestTokenFollowsRotation(t *testing.T) {
	r := &TunnelResource{}
	base := map[string]string{"id": "tnl_01ABC", "domain": "d", "created_at": "2026-07-01T10:00:00Z", "token": "secret-token-1", "token_id": "ttok_01"}
	with := func(rotation string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		if rotation != "" {
			m["token_rotation"] = rotation
		}
		return m
	}

	tests := []struct {
		name               string
		stateRot, planRot  string
		wantUnknown        bool
		wantStateValueKept bool
	}{
		{"rotation changed", "1", "2", true, false},
		{"rotation unchanged", "1", "1", false, true},
		{"first rotation value", "", "1", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := resourceState(t, r, with(tc.stateRot))
			plan := resourcePlan(t, r, with(tc.planRot))
			req := planmodifier.StringRequest{
				State:      state,
				Plan:       plan,
				StateValue: types.StringValue("secret-token-1"),
				PlanValue:  types.StringUnknown(),
			}
			resp := &planmodifier.StringResponse{PlanValue: req.PlanValue}
			tokenFollowsRotation().PlanModifyString(context.Background(), req, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
			}
			if got := resp.PlanValue.IsUnknown(); got != tc.wantUnknown {
				t.Errorf("plan value unknown = %v, want %v", got, tc.wantUnknown)
			}
			if tc.wantStateValueKept && resp.PlanValue.ValueString() != "secret-token-1" {
				t.Errorf("plan value = %v, want the state value", resp.PlanValue)
			}
		})
	}

	// After an import, state has no token. With no configuration change the
	// framework plans the state's null, and the modifier turns it unknown,
	// so the plan shows an update and the apply reveals the token.
	t.Run("state without a token plans it unknown", func(t *testing.T) {
		imported := map[string]string{"id": "tnl_01ABC", "domain": "d", "created_at": "2026-07-01T10:00:00Z"}
		req := planmodifier.StringRequest{
			State:      resourceState(t, r, imported),
			Plan:       resourcePlan(t, r, imported),
			StateValue: types.StringNull(),
			PlanValue:  types.StringNull(),
		}
		resp := &planmodifier.StringResponse{PlanValue: req.PlanValue}
		tokenFollowsRotation().PlanModifyString(context.Background(), req, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if !resp.PlanValue.IsUnknown() {
			t.Errorf("plan value = %v, want unknown", resp.PlanValue)
		}
	})

	t.Run("create leaves the value unknown", func(t *testing.T) {
		req := planmodifier.StringRequest{
			State: tfsdk.State{
				Raw:    tftypes.NewValue(schematest.ResourceObjectType(t, r), nil),
				Schema: schematest.ResourceSchema(t, r),
			},
			Plan:      resourcePlan(t, r, nil),
			PlanValue: types.StringUnknown(),
		}
		resp := &planmodifier.StringResponse{PlanValue: req.PlanValue}
		tokenFollowsRotation().PlanModifyString(context.Background(), req, resp)
		if !resp.PlanValue.IsUnknown() {
			t.Errorf("plan value = %v, want unknown on create", resp.PlanValue)
		}
	})
}

// TestTunnelResource_Create pins the create flow: POST /v1/tunnels with the
// display name, then reveal_token on the new ID, both under the tunnels beta
// header and the bearer credential, with the token landing in state.
func TestTunnelResource_Create(t *testing.T) {
	srv, log := fakeTunnelsAPI(t, map[string]func(http.ResponseWriter){
		"POST /v1/tunnels":                        respond(http.StatusOK, tunnelLiveJSON),
		"POST /v1/tunnels/tnl_01ABC/reveal_token": respond(http.StatusOK, tunnelTokenJSON),
	})
	r := &TunnelResource{client: oauthtest.NewClient(t, srv)}

	plan := resourcePlan(t, r, map[string]string{"display_name": "corp-mcp", "token_rotation": "1"})
	resp := &resource.CreateResponse{State: resourceState(t, r, nil)}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create returned errors: %v", resp.Diagnostics)
	}

	reqs := log.all()
	if len(reqs) != 2 {
		t.Fatalf("expected 2 requests (create, reveal), got %d: %+v", len(reqs), reqs)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(reqs[0].Body), &body); err != nil {
		t.Fatalf("create body: %v", err)
	}
	if body["display_name"] != "corp-mcp" {
		t.Errorf("create body = %v, want display_name corp-mcp", body)
	}
	for _, req := range reqs {
		if !strings.Contains(req.Beta, "mcp-tunnels-2026-06-22") {
			t.Errorf("%s %s: anthropic-beta = %q, want mcp-tunnels-2026-06-22", req.Method, req.Path, req.Beta)
		}
		if req.Auth != "Bearer test" {
			t.Errorf("%s %s: Authorization = %q, want the bearer token", req.Method, req.Path, req.Auth)
		}
	}

	var data TunnelResourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if data.ID.ValueString() != "tnl_01ABC" || data.Domain.ValueString() != "abc123.tunnel.anthropic.com" {
		t.Errorf("id/domain = %v/%v", data.ID, data.Domain)
	}
	if data.Token.ValueString() != "secret-token-1" || data.TokenID.ValueString() != "ttok_01" {
		t.Errorf("token/token_id = %v/%v, want the revealed token", data.Token, data.TokenID)
	}
	if data.TokenRotation.ValueString() != "1" {
		t.Errorf("token_rotation = %v, want the configured 1", data.TokenRotation)
	}
	if data.CreatedAt.ValueString() != "2026-07-01T10:00:00Z" {
		t.Errorf("created_at = %v", data.CreatedAt)
	}
}

// TestTunnelResource_Create_omitsUnsetDisplayName: the API treats an absent
// display_name as unset; the provider must not send "" or null.
func TestTunnelResource_Create_omitsUnsetDisplayName(t *testing.T) {
	srv, log := fakeTunnelsAPI(t, map[string]func(http.ResponseWriter){
		"POST /v1/tunnels":                        respond(http.StatusOK, strings.Replace(tunnelLiveJSON, `"corp-mcp"`, "null", 1)),
		"POST /v1/tunnels/tnl_01ABC/reveal_token": respond(http.StatusOK, tunnelTokenJSON),
	})
	r := &TunnelResource{client: oauthtest.NewClient(t, srv)}

	resp := &resource.CreateResponse{State: resourceState(t, r, nil)}
	r.Create(context.Background(), resource.CreateRequest{Plan: resourcePlan(t, r, nil)}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create returned errors: %v", resp.Diagnostics)
	}
	if body := log.all()[0].Body; strings.Contains(body, "display_name") {
		t.Errorf("create body = %s, want no display_name", body)
	}
	var data TunnelResourceModel
	resp.State.Get(context.Background(), &data)
	if !data.DisplayName.IsNull() {
		t.Errorf("display_name = %v, want null", data.DisplayName)
	}
}

// TestTunnelResource_Create_isNotRetried: tunnel creation is not idempotent,
// so a 5xx (which may follow a create that landed) must not be replayed by
// the SDK's default retry policy.
func TestTunnelResource_Create_isNotRetried(t *testing.T) {
	srv, log := fakeTunnelsAPI(t, map[string]func(http.ResponseWriter){
		"POST /v1/tunnels": respond(http.StatusInternalServerError, `{"type":"error","error":{"type":"api_error","message":"boom"}}`),
	})
	r := &TunnelResource{client: oauthtest.NewClient(t, srv)}

	resp := &resource.CreateResponse{State: resourceState(t, r, nil)}
	r.Create(context.Background(), resource.CreateRequest{Plan: resourcePlan(t, r, nil)}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error")
	}
	if n := log.count(http.MethodPost, "/v1/tunnels"); n != 1 {
		t.Errorf("POST /v1/tunnels sent %d times, want exactly 1", n)
	}
	var data TunnelResourceModel
	resp.State.Get(context.Background(), &data)
	if !data.ID.IsNull() {
		t.Errorf("id = %v after a failed create, want null", data.ID)
	}
}

// TestTunnelResource_Create_revealFailureSavesTunnel: when the tunnel exists
// but its token cannot be revealed, the tunnel is still saved (so Terraform
// taints it instead of losing track of a non-idempotent create).
func TestTunnelResource_Create_revealFailureSavesTunnel(t *testing.T) {
	srv, _ := fakeTunnelsAPI(t, map[string]func(http.ResponseWriter){
		"POST /v1/tunnels":                        respond(http.StatusOK, tunnelLiveJSON),
		"POST /v1/tunnels/tnl_01ABC/reveal_token": respond(http.StatusForbidden, `{"type":"error","error":{"type":"permission_error","message":"nope"}}`),
	})
	r := &TunnelResource{client: oauthtest.NewClient(t, srv, option.WithMaxRetries(0))}

	resp := &resource.CreateResponse{State: resourceState(t, r, nil)}
	r.Create(context.Background(), resource.CreateRequest{Plan: resourcePlan(t, r, nil)}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error when the token cannot be revealed")
	}
	var data TunnelResourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if data.ID.ValueString() != "tnl_01ABC" {
		t.Errorf("id = %v, want the created tunnel saved in state", data.ID)
	}
	if !data.Token.IsNull() {
		t.Errorf("token = %v, want null", data.Token)
	}
}

func readTunnel(t *testing.T, r *TunnelResource, state map[string]string) *resource.ReadResponse {
	t.Helper()
	st := resourceState(t, r, state)
	resp := &resource.ReadResponse{State: st}
	r.Read(context.Background(), resource.ReadRequest{State: st}, resp)
	return resp
}

// TestTunnelResource_Read_keepsTokenWithoutRevealing: a refresh never
// re-reveals the token (each reveal is an audited compliance event).
func TestTunnelResource_Read_keepsTokenWithoutRevealing(t *testing.T) {
	srv, log := fakeTunnelsAPI(t, map[string]func(http.ResponseWriter){
		"GET /v1/tunnels/tnl_01ABC":               respond(http.StatusOK, tunnelLiveJSON),
		"POST /v1/tunnels/tnl_01ABC/reveal_token": respond(http.StatusOK, tunnelRotatedJSON),
	})
	r := &TunnelResource{client: oauthtest.NewClient(t, srv)}

	resp := readTunnel(t, r, map[string]string{"id": "tnl_01ABC", "token": "secret-token-1", "token_id": "ttok_01", "token_rotation": "1"})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read returned errors: %v", resp.Diagnostics)
	}
	if n := log.count(http.MethodPost, "/v1/tunnels/tnl_01ABC/reveal_token"); n != 0 {
		t.Errorf("reveal_token called %d times on refresh, want 0", n)
	}
	var data TunnelResourceModel
	resp.State.Get(context.Background(), &data)
	if data.Token.ValueString() != "secret-token-1" || data.TokenID.ValueString() != "ttok_01" {
		t.Errorf("token/token_id = %v/%v, want the state values kept", data.Token, data.TokenID)
	}
	if data.TokenRotation.ValueString() != "1" {
		t.Errorf("token_rotation = %v, want the state value kept", data.TokenRotation)
	}
	if data.DisplayName.ValueString() != "corp-mcp" || data.Domain.ValueString() != "abc123.tunnel.anthropic.com" {
		t.Errorf("display_name/domain = %v/%v", data.DisplayName, data.Domain)
	}
}

// TestTunnelResource_Read_importDoesNotReveal: after an import there is no
// token in state, and Read still does not reveal it. Read runs on every plan,
// a plan never saves its refreshed state, and each reveal is an audited
// compliance event, so a pending import would add one per plan.
func TestTunnelResource_Read_importDoesNotReveal(t *testing.T) {
	srv, log := fakeTunnelsAPI(t, map[string]func(http.ResponseWriter){
		"GET /v1/tunnels/tnl_01ABC":               respond(http.StatusOK, tunnelLiveJSON),
		"POST /v1/tunnels/tnl_01ABC/reveal_token": respond(http.StatusOK, tunnelTokenJSON),
	})
	r := &TunnelResource{client: oauthtest.NewClient(t, srv)}

	resp := readTunnel(t, r, map[string]string{"id": "tnl_01ABC"})
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read returned errors: %v", resp.Diagnostics)
	}
	if n := log.count(http.MethodPost, "/v1/tunnels/tnl_01ABC/reveal_token"); n != 0 {
		t.Errorf("reveal_token called %d times after an import, want 0", n)
	}
	var data TunnelResourceModel
	resp.State.Get(context.Background(), &data)
	if data.ID.ValueString() != "tnl_01ABC" || data.Domain.ValueString() != "abc123.tunnel.anthropic.com" {
		t.Errorf("id/domain = %v/%v, want the tunnel read", data.ID, data.Domain)
	}
	if !data.Token.IsNull() || !data.TokenID.IsNull() {
		t.Errorf("token/token_id = %v/%v, want null until the apply reveals them", data.Token, data.TokenID)
	}
}

func TestTunnelResource_Read_archivedOrMissingRemovesFromState(t *testing.T) {
	for name, routes := range map[string]map[string]func(http.ResponseWriter){
		"archived": {"GET /v1/tunnels/tnl_01ABC": respond(http.StatusOK, tunnelArchivedJSON)},
		"missing":  {},
	} {
		t.Run(name, func(t *testing.T) {
			srv, log := fakeTunnelsAPI(t, routes)
			r := &TunnelResource{client: oauthtest.NewClient(t, srv)}

			resp := readTunnel(t, r, map[string]string{"id": "tnl_01ABC", "token": "secret-token-1"})
			if resp.Diagnostics.HasError() {
				t.Fatalf("Read returned errors: %v", resp.Diagnostics)
			}
			if !resp.State.Raw.IsNull() {
				t.Error("expected the tunnel to be removed from state")
			}
			if n := log.count(http.MethodPost, "/v1/tunnels/tnl_01ABC/reveal_token"); n != 0 {
				t.Errorf("reveal_token called %d times, want 0", n)
			}
		})
	}
}

func updateTunnel(t *testing.T, r *TunnelResource, state, plan map[string]string) *resource.UpdateResponse {
	t.Helper()
	resp := &resource.UpdateResponse{State: resourceState(t, r, state)}
	r.Update(context.Background(), resource.UpdateRequest{
		State: resourceState(t, r, state),
		Plan:  resourcePlan(t, r, plan),
	}, resp)
	return resp
}

func TestTunnelResource_Update_rotatesToken(t *testing.T) {
	srv, log := fakeTunnelsAPI(t, map[string]func(http.ResponseWriter){
		"POST /v1/tunnels/tnl_01ABC/rotate_token": respond(http.StatusOK, tunnelRotatedJSON),
	})
	r := &TunnelResource{client: oauthtest.NewClient(t, srv)}

	common := map[string]string{"id": "tnl_01ABC", "domain": "abc123.tunnel.anthropic.com"}
	state := map[string]string{"token_rotation": "1", "token": "secret-token-1", "token_id": "ttok_01"}
	plan := map[string]string{"token_rotation": "2"}
	for k, v := range common {
		state[k], plan[k] = v, v
	}

	resp := updateTunnel(t, r, state, plan)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update returned errors: %v", resp.Diagnostics)
	}
	reqs := log.all()
	if len(reqs) != 1 {
		t.Fatalf("expected exactly one rotate_token call, got %+v", reqs)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(reqs[0].Body), &body)
	if body["reason"] != tunnelTokenRotationReason {
		t.Errorf("rotate body = %v, want the audit reason", body)
	}

	var data TunnelResourceModel
	resp.State.Get(context.Background(), &data)
	if data.Token.ValueString() != "secret-token-2" || data.TokenID.ValueString() != "ttok_02" {
		t.Errorf("token/token_id = %v/%v, want the rotated token", data.Token, data.TokenID)
	}
	if data.TokenRotation.ValueString() != "2" {
		t.Errorf("token_rotation = %v, want 2", data.TokenRotation)
	}
}

// TestTunnelResource_Update_baselineDoesNotRotate: adding token_rotation to a
// tunnel that had none (an import, typically) is recorded without any API
// call, so the running connector keeps working.
func TestTunnelResource_Update_baselineDoesNotRotate(t *testing.T) {
	srv, log := fakeTunnelsAPI(t, nil)
	r := &TunnelResource{client: oauthtest.NewClient(t, srv)}

	resp := updateTunnel(t, r,
		map[string]string{"id": "tnl_01ABC", "token": "secret-token-1", "token_id": "ttok_01"},
		map[string]string{"id": "tnl_01ABC", "token_rotation": "1"},
	)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update returned errors: %v", resp.Diagnostics)
	}
	if reqs := log.all(); len(reqs) != 0 {
		t.Errorf("expected no API call, got %+v", reqs)
	}
	var data TunnelResourceModel
	resp.State.Get(context.Background(), &data)
	if data.Token.ValueString() != "secret-token-1" || data.TokenID.ValueString() != "ttok_01" {
		t.Errorf("token/token_id = %v/%v, want the state values kept", data.Token, data.TokenID)
	}
	if data.TokenRotation.ValueString() != "1" {
		t.Errorf("token_rotation = %v, want 1 recorded", data.TokenRotation)
	}
}

// TestTunnelResource_Update_afterImportRevealsToken: the first apply after an
// import (state without a token) reveals the token once and stores it,
// without rotating it.
func TestTunnelResource_Update_afterImportRevealsToken(t *testing.T) {
	srv, log := fakeTunnelsAPI(t, map[string]func(http.ResponseWriter){
		"POST /v1/tunnels/tnl_01ABC/reveal_token": respond(http.StatusOK, tunnelTokenJSON),
	})
	r := &TunnelResource{client: oauthtest.NewClient(t, srv)}

	imported := map[string]string{"id": "tnl_01ABC", "display_name": "corp-mcp", "domain": "abc123.tunnel.anthropic.com"}
	resp := updateTunnel(t, r, imported, imported)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update returned errors: %v", resp.Diagnostics)
	}
	if n := log.count(http.MethodPost, "/v1/tunnels/tnl_01ABC/reveal_token"); n != 1 {
		t.Errorf("reveal_token called %d times, want 1", n)
	}
	if n := log.count(http.MethodPost, "/v1/tunnels/tnl_01ABC/rotate_token"); n != 0 {
		t.Errorf("rotate_token called %d times, want 0", n)
	}
	var data TunnelResourceModel
	resp.State.Get(context.Background(), &data)
	if data.Token.ValueString() != "secret-token-1" || data.TokenID.ValueString() != "ttok_01" {
		t.Errorf("token/token_id = %v/%v, want the revealed token stored", data.Token, data.TokenID)
	}
}

// TestTunnelResource_Update_afterImportRevealFailureKeepsPriorState: a failed
// reveal fails the apply and leaves the prior state, still without a token,
// so the next plan plans the reveal again.
func TestTunnelResource_Update_afterImportRevealFailureKeepsPriorState(t *testing.T) {
	srv, _ := fakeTunnelsAPI(t, map[string]func(http.ResponseWriter){
		"POST /v1/tunnels/tnl_01ABC/reveal_token": respond(http.StatusForbidden, `{"type":"error","error":{"type":"permission_error","message":"nope"}}`),
	})
	r := &TunnelResource{client: oauthtest.NewClient(t, srv, option.WithMaxRetries(0))}

	resp := updateTunnel(t, r,
		map[string]string{"id": "tnl_01ABC"},
		map[string]string{"id": "tnl_01ABC", "token_rotation": "1"},
	)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error when the token cannot be revealed")
	}
	var data TunnelResourceModel
	resp.State.Get(context.Background(), &data)
	if !data.Token.IsNull() || !data.TokenRotation.IsNull() {
		t.Errorf("token/token_rotation = %v/%v, want the prior state kept", data.Token, data.TokenRotation)
	}
}

func TestTunnelResource_Delete_archives(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		body    string
		wantErr bool
	}{
		"archived":        {http.StatusOK, tunnelArchivedJSON, false},
		"already gone":    {http.StatusNotFound, notFoundBody, false},
		"permission lost": {http.StatusForbidden, `{"type":"error","error":{"type":"permission_error","message":"nope"}}`, true},
	} {
		t.Run(name, func(t *testing.T) {
			srv, log := fakeTunnelsAPI(t, map[string]func(http.ResponseWriter){
				"POST /v1/tunnels/tnl_01ABC/archive": respond(tc.status, tc.body),
			})
			r := &TunnelResource{client: oauthtest.NewClient(t, srv)}

			st := resourceState(t, r, map[string]string{"id": "tnl_01ABC"})
			resp := &resource.DeleteResponse{State: st}
			r.Delete(context.Background(), resource.DeleteRequest{State: st}, resp)
			if got := resp.Diagnostics.HasError(); got != tc.wantErr {
				t.Errorf("error = %v, want %v (%v)", got, tc.wantErr, resp.Diagnostics)
			}
			if n := log.count(http.MethodPost, "/v1/tunnels/tnl_01ABC/archive"); n != 1 {
				t.Errorf("archive called %d times, want 1", n)
			}
		})
	}
}

// tunnelTestProvider serves a single TunnelResource, so a test can drive it
// through the framework's protocol server: PlanResourceChange then applies
// the schema defaults, marks unconfigured Computed values unknown and runs
// the plan modifiers as it does for Terraform. ConfigureProvider is never
// called, so the resource keeps the client the test gave it.
type tunnelTestProvider struct{ r *TunnelResource }

func (tunnelTestProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "anthropic"
}

func (tunnelTestProvider) Schema(context.Context, provider.SchemaRequest, *provider.SchemaResponse) {}

func (tunnelTestProvider) Configure(context.Context, provider.ConfigureRequest, *provider.ConfigureResponse) {
}

func (p tunnelTestProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{func() resource.Resource { return p.r }}
}

func (tunnelTestProvider) DataSources(context.Context) []func() datasource.DataSource { return nil }

// tunnelProtocol runs ReadResource, PlanResourceChange and
// ApplyResourceChange for anthropic_tunnel, with values in their Terraform
// encoding.
type tunnelProtocol struct {
	t      *testing.T
	r      *TunnelResource
	server tfprotov6.ProviderServer
}

func newTunnelProtocol(t *testing.T, r *TunnelResource) tunnelProtocol {
	t.Helper()
	server, err := providerserver.NewProtocol6WithError(tunnelTestProvider{r: r})()
	if err != nil {
		t.Fatalf("provider server: %s", err)
	}
	return tunnelProtocol{t: t, r: r, server: server}
}

func (p tunnelProtocol) dynamic(v tftypes.Value) *tfprotov6.DynamicValue {
	p.t.Helper()
	dv, err := tfprotov6.NewDynamicValue(schematest.ResourceObjectType(p.t, p.r), v)
	if err != nil {
		p.t.Fatalf("dynamic value: %s", err)
	}
	return &dv
}

// decode fails the test on an error diagnostic and returns the value as a
// model, unknown values included.
func (p tunnelProtocol) decode(dv *tfprotov6.DynamicValue, diags []*tfprotov6.Diagnostic) (tftypes.Value, TunnelResourceModel) {
	p.t.Helper()
	for _, d := range diags {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			p.t.Fatalf("%s: %s", d.Summary, d.Detail)
		}
	}
	v, err := dv.Unmarshal(schematest.ResourceObjectType(p.t, p.r))
	if err != nil {
		p.t.Fatalf("unmarshal: %s", err)
	}
	var m TunnelResourceModel
	if diags := (tfsdk.Plan{Schema: schematest.ResourceSchema(p.t, p.r), Raw: v}).Get(context.Background(), &m); diags.HasError() {
		p.t.Fatalf("decode: %+v", diags)
	}
	return v, m
}

func (p tunnelProtocol) read(state tftypes.Value) (tftypes.Value, TunnelResourceModel) {
	p.t.Helper()
	resp, err := p.server.ReadResource(context.Background(), &tfprotov6.ReadResourceRequest{
		TypeName: "anthropic_tunnel", CurrentState: p.dynamic(state),
	})
	if err != nil {
		p.t.Fatalf("read: %s", err)
	}
	return p.decode(resp.NewState, resp.Diagnostics)
}

// plan returns the planned state. proposed is what Terraform derives from
// prior and config: the configured values, and the prior value of every
// Computed attribute the configuration leaves null.
func (p tunnelProtocol) plan(prior, config, proposed tftypes.Value) (tftypes.Value, TunnelResourceModel) {
	p.t.Helper()
	resp, err := p.server.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{
		TypeName: "anthropic_tunnel", PriorState: p.dynamic(prior), Config: p.dynamic(config), ProposedNewState: p.dynamic(proposed),
	})
	if err != nil {
		p.t.Fatalf("plan: %s", err)
	}
	return p.decode(resp.PlannedState, resp.Diagnostics)
}

func (p tunnelProtocol) apply(prior, config, planned tftypes.Value) (tftypes.Value, TunnelResourceModel) {
	p.t.Helper()
	resp, err := p.server.ApplyResourceChange(context.Background(), &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "anthropic_tunnel", PriorState: p.dynamic(prior), Config: p.dynamic(config), PlannedState: p.dynamic(planned),
	})
	if err != nil {
		p.t.Fatalf("apply: %s", err)
	}
	return p.decode(resp.NewState, resp.Diagnostics)
}

// TestTunnelResource_importThenPlanAndApply drives an import through the
// protocol: the refresh and the plan never reveal the token, the plan shows
// it unknown (an update), and the apply reveals it once and stores it. A
// second plan is then empty and reveals nothing.
func TestTunnelResource_importThenPlanAndApply(t *testing.T) {
	srv, log := fakeTunnelsAPI(t, map[string]func(http.ResponseWriter){
		"GET /v1/tunnels/tnl_01ABC":               respond(http.StatusOK, tunnelLiveJSON),
		"POST /v1/tunnels/tnl_01ABC/reveal_token": respond(http.StatusOK, tunnelTokenJSON),
	})
	r := &TunnelResource{client: oauthtest.NewClient(t, srv)}
	p := newTunnelProtocol(t, r)
	reveals := func() int { return log.count(http.MethodPost, "/v1/tunnels/tnl_01ABC/reveal_token") }

	// terraform import, or an import block: ImportState sets the ID, then a
	// refresh fills the rest.
	refreshed, _ := p.read(resourceValue(t, r, map[string]string{"id": "tnl_01ABC"}))
	config := resourceValue(t, r, map[string]string{"display_name": "corp-mcp"})

	planned, plan := p.plan(refreshed, config, refreshed)
	if !plan.Token.IsUnknown() || !plan.TokenID.IsUnknown() {
		t.Errorf("planned token/token_id = %v/%v, want unknown after an import", plan.Token, plan.TokenID)
	}
	if n := reveals(); n != 0 {
		t.Fatalf("reveal_token called %d times by refresh and plan, want 0", n)
	}

	applied, state := p.apply(refreshed, config, planned)
	if n := reveals(); n != 1 {
		t.Errorf("reveal_token called %d times by the apply, want 1", n)
	}
	if state.Token.ValueString() != "secret-token-1" || state.TokenID.ValueString() != "ttok_01" {
		t.Errorf("token/token_id = %v/%v, want the revealed token", state.Token, state.TokenID)
	}

	refreshed, _ = p.read(applied)
	planned, _ = p.plan(refreshed, config, refreshed)
	if !planned.Equal(refreshed) {
		t.Errorf("second plan differs from state:\n%s\nwant\n%s", planned, refreshed)
	}
	if n := reveals(); n != 1 {
		t.Errorf("reveal_token called %d times in all, want 1", n)
	}
}
