// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

const testRoleARN = "arn:aws:iam::123456789012:role/agent-runner"

func executionIdentity(typ string, roleARN types.String) types.Object {
	return types.ObjectValueMust(agentExecutionIdentityAttrTypes, map[string]attr.Value{
		"type": types.StringValue(typ), "role_arn": roleARN,
	})
}

var noExecutionIdentity = types.ObjectNull(agentExecutionIdentityAttrTypes)

func TestBuildExecutionIdentityParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		plan     types.Object
		state    types.Object
		wantSent bool
		want     string
	}{
		{"unset stays omitted", noExecutionIdentity, noExecutionIdentity, false, ""},
		{"aws role", executionIdentity("aws_role", types.StringValue(testRoleARN)), noExecutionIdentity, true,
			`{"role_arn":"` + testRoleARN + `","type":"aws_role"}`},
		{"explicit service account", executionIdentity("service_account", types.StringNull()), noExecutionIdentity, true,
			`{"type":"service_account"}`},
		{"removed restores the default", noExecutionIdentity, executionIdentity("aws_role", types.StringValue(testRoleARN)), true,
			`{"type":"service_account"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u, sent, diags := buildExecutionIdentityParams(context.Background(), tc.plan, tc.state)
			if diags.HasError() {
				t.Fatalf("diags: %+v", diags)
			}
			if sent != tc.wantSent {
				t.Fatalf("sent = %t, want %t", sent, tc.wantSent)
			}
			if !sent {
				return
			}
			raw, err := json.Marshal(u)
			if err != nil {
				t.Fatal(err)
			}
			assertJSONEqual(t, string(raw), tc.want)
		})
	}
}

func parseExecutionIdentity(t *testing.T, raw string) anthropic.BetaManagedAgentsExecutionIdentityUnion {
	t.Helper()
	var agent anthropic.BetaManagedAgentsAgent
	j := `{"id":"agent_1","name":"n","model":{"id":"claude-sonnet-4-6"},"version":1,` +
		`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"` + raw + `}`
	if err := json.Unmarshal([]byte(j), &agent); err != nil {
		t.Fatal(err)
	}
	return agent.ExecutionIdentity
}

// On Read the API always returns an identity, `service_account` by default: it
// stays null while the configuration leaves the attribute unset, and any other
// value (an IAM role set out of band included) surfaces as drift. A response
// without one keeps state. On Create and Update the plan is kept whatever the
// API returns, since anything else fails the apply as an inconsistent result.
func TestMapExecutionIdentityToState(t *testing.T) {
	t.Parallel()

	serviceAccount := `,"execution_identity":{"type":"service_account"}`
	awsRole := `,"execution_identity":{"type":"aws_role","role_arn":"` + testRoleARN + `"}`
	otherRole := `,"execution_identity":{"type":"aws_role","role_arn":"arn:aws:iam::123456789012:role/other"}`
	sa := executionIdentity("service_account", types.StringNull())
	role := executionIdentity("aws_role", types.StringValue(testRoleARN))

	tests := []struct {
		name    string
		mode    mappingMode
		api     string
		current types.Object
		want    types.Object
	}{
		{"default while unset", readMapping, serviceAccount, noExecutionIdentity, noExecutionIdentity},
		{"default configured explicitly", readMapping, serviceAccount, sa, sa},
		{"aws role echoed", readMapping, awsRole, role, role},
		{"aws role set out of band", readMapping, awsRole, noExecutionIdentity, role},
		{"aws role removed out of band", readMapping, serviceAccount, role, sa},
		{"absent", readMapping, ``, noExecutionIdentity, noExecutionIdentity},
		{"absent keeps the explicit default", readMapping, ``, sa, sa},
		{"null keeps the explicit default", readMapping, `,"execution_identity":null`, sa, sa},
		{"absent keeps an aws role", readMapping, ``, role, role},
		{"apply: default while unset", applyMapping, serviceAccount, noExecutionIdentity, noExecutionIdentity},
		{"apply: aws role echoed", applyMapping, awsRole, role, role},
		{"apply: unset plan over a role the refresh missed", applyMapping, awsRole, noExecutionIdentity, noExecutionIdentity},
		{"apply: explicit default not echoed", applyMapping, ``, sa, sa},
		{"apply: aws role not echoed", applyMapping, ``, role, role},
		{"apply: aws role echoed in another shape", applyMapping, otherRole, role, role},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, diags := mapExecutionIdentityToState(parseExecutionIdentity(t, tc.api), tc.current, tc.mode)
			if diags.HasError() {
				t.Fatalf("diags: %+v", diags)
			}
			if !got.Equal(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// The data sources map the identity verbatim, the default included.
func TestMapAgentToDataSources_executionIdentity(t *testing.T) {
	t.Parallel()

	agent := apiAgentWithMultiagent(t, `,"execution_identity":{"type":"service_account"}`)
	var data AgentDataSourceModel
	if diags := mapAgentResponseToDataSource(agent, &data); diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	want := executionIdentity("service_account", types.StringNull())
	if !data.ExecutionIdentity.Equal(want) {
		t.Errorf("agent data source = %v, want %v", data.ExecutionIdentity, want)
	}

	agent = apiAgentWithMultiagent(t, `,"execution_identity":{"type":"aws_role","role_arn":"`+testRoleARN+`"}`)
	obj, diags := mapAgentToDataSourceObject(agent)
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	got := obj.(types.Object).Attributes()["execution_identity"]
	if want := executionIdentity("aws_role", types.StringValue(testRoleARN)); !got.Equal(want) {
		t.Errorf("agents data source = %v, want %v", got, want)
	}
}

func TestValidateExecutionIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		obj     types.Object
		wantErr bool
	}{
		{"unset", noExecutionIdentity, false},
		{"aws role", executionIdentity("aws_role", types.StringValue(testRoleARN)), false},
		{"aws role without arn", executionIdentity("aws_role", types.StringNull()), true},
		{"aws role with unknown arn", executionIdentity("aws_role", types.StringUnknown()), false},
		{"service account", executionIdentity("service_account", types.StringNull()), false},
		{"service account with arn", executionIdentity("service_account", types.StringValue(testRoleARN)), true},
		{"unknown type", types.ObjectValueMust(agentExecutionIdentityAttrTypes, map[string]attr.Value{
			"type": types.StringUnknown(), "role_arn": types.StringNull(),
		}), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			diags := validateExecutionIdentity(context.Background(), tc.obj)
			if diags.HasError() != tc.wantErr {
				t.Fatalf("error = %t, want %t: %+v", diags.HasError(), tc.wantErr, diags)
			}
			if tc.wantErr {
				withPath, ok := diags.Errors()[0].(interface{ Path() path.Path })
				if !ok || withPath.Path().String() != "execution_identity.role_arn" {
					t.Errorf("error = %+v, want it on execution_identity.role_arn", diags.Errors()[0])
				}
			}
		})
	}
}

func TestExecutionIdentityRoleARNPattern(t *testing.T) {
	t.Parallel()
	for arn, want := range map[string]bool{
		testRoleARN: true,
		"arn:aws:iam::123456789012:role/service-role/agent-runner": true,
		"arn:aws-us-gov:iam::123456789012:role/agent-runner":       true,
		"arn:aws:iam::123456789012:user/alice":                     false,
		"arn:aws:iam::12345:role/agent-runner":                     false,
		"agent-runner":                                             false,
	} {
		if got := executionIdentityRoleARN.MatchString(arn); got != want {
			t.Errorf("%q: match = %t, want %t", arn, got, want)
		}
	}
}

// agentIdentityServer answers every agent request with an agent carrying
// identity (no execution_identity key when it is empty), and records the
// request bodies.
func agentIdentityServer(t *testing.T, identity string) (*httptest.Server, func() []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		field := ""
		if identity != "" {
			field = `,"execution_identity":` + identity
		}
		_, _ = io.WriteString(w, `{"id":"agent_1","name":"n","model":{"id":"claude-sonnet-4-6"},"version":2,`+
			`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z","description":"","system":"",`+
			`"metadata":{},"mcp_servers":[],"skills":[],"tools":[],"multiagent":null`+field+`}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return bodies
	}
}

func agentResourceValues(t *testing.T, r *AgentResource, identity tftypes.Value) tftypes.Value {
	t.Helper()
	objType := schematest.ResourceObjectType(t, r)
	vals := schematest.NullValues(t, r)
	vals["id"] = tftypes.NewValue(tftypes.String, "agent_1")
	vals["model"] = tftypes.NewValue(tftypes.String, "claude-sonnet-4-6")
	vals["name"] = tftypes.NewValue(tftypes.String, "n")
	vals["version"] = tftypes.NewValue(tftypes.Number, 1)
	vals["execution_identity"] = identity
	return tftypes.NewValue(objType, vals)
}

func identityValue(t *testing.T, r *AgentResource, typ, roleARN string) tftypes.Value {
	t.Helper()
	objType := schematest.ResourceObjectType(t, r).AttributeTypes["execution_identity"].(tftypes.Object)
	if typ == "" {
		return tftypes.NewValue(objType, nil)
	}
	arn := tftypes.NewValue(tftypes.String, nil)
	if roleARN != "" {
		arn = tftypes.NewValue(tftypes.String, roleARN)
	}
	return tftypes.NewValue(objType, map[string]tftypes.Value{"type": tftypes.NewValue(tftypes.String, typ), "role_arn": arn})
}

// Create sends a configured identity and omits an unset one; the echoed
// identity reaches state as configured.
func TestAgentResourceCreate_executionIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		typ, roleARN string
		apiIdentity  string
		wantBody     string // "" when the key must be absent
	}{
		{"aws role", "aws_role", testRoleARN, `{"type":"aws_role","role_arn":"` + testRoleARN + `"}`,
			`{"role_arn":"` + testRoleARN + `","type":"aws_role"}`},
		{"unset", "", "", `{"type":"service_account"}`, ""},
		// The plan is kept even when the API leaves the identity out of its answer.
		{"explicit default not echoed", "service_account", "", "", `{"type":"service_account"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, bodies := agentIdentityServer(t, tc.apiIdentity)
			r := &AgentResource{client: oauthtest.NewSDKClient(t, srv)}
			schema := schematest.ResourceSchema(t, r)
			plan := agentResourceValues(t, r, identityValue(t, r, tc.typ, tc.roleARN))

			resp := &resource.CreateResponse{State: tfsdk.State{Schema: schema}}
			r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Raw: plan, Schema: schema}}, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Create: %+v", resp.Diagnostics)
			}
			got := bodies()
			if len(got) != 1 {
				t.Fatalf("got %d requests, want 1", len(got))
			}
			assertBodyKey(t, got[0], "execution_identity", tc.wantBody)

			var state AgentResourceModel
			resp.Diagnostics.Append(resp.State.Get(context.Background(), &state)...)
			want := noExecutionIdentity
			if tc.typ != "" {
				want = executionIdentity(tc.typ, roleARNValue(tc.roleARN))
			}
			if !state.ExecutionIdentity.Equal(want) {
				t.Errorf("state = %v, want %v", state.ExecutionIdentity, want)
			}
		})
	}
}

func roleARNValue(arn string) types.String {
	if arn == "" {
		return types.StringNull()
	}
	return types.StringValue(arn)
}

// Update restores the default when the identity is removed, and keeps the
// request body unchanged for a configuration that never set one. The state it
// returns is the planned identity, whatever the API holds.
func TestAgentResourceUpdate_executionIdentity(t *testing.T) {
	t.Parallel()

	serviceAccount := `{"type":"service_account"}`
	awsRole := `{"type":"aws_role","role_arn":"` + testRoleARN + `"}`
	tests := []struct {
		name        string
		stateType   string
		planType    string
		apiIdentity string
		wantBody    string
	}{
		{"removed", "aws_role", "", serviceAccount, `{"type":"service_account"}`},
		{"never set", "", "", serviceAccount, ""},
		{"set", "", "aws_role", awsRole, `{"role_arn":"` + testRoleARN + `","type":"aws_role"}`},
		// A role set out of band after the last refresh (or with -refresh=false):
		// the update leaves it alone and the next Read reports it.
		{"never set, role unseen by the refresh", "", "", awsRole, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, bodies := agentIdentityServer(t, tc.apiIdentity)
			r := &AgentResource{client: oauthtest.NewSDKClient(t, srv)}
			schema := schematest.ResourceSchema(t, r)
			arn := func(typ string) string {
				if typ == "aws_role" {
					return testRoleARN
				}
				return ""
			}
			state := agentResourceValues(t, r, identityValue(t, r, tc.stateType, arn(tc.stateType)))
			plan := agentResourceValues(t, r, identityValue(t, r, tc.planType, arn(tc.planType)))

			resp := &resource.UpdateResponse{State: tfsdk.State{Schema: schema}}
			r.Update(context.Background(), resource.UpdateRequest{
				Plan:  tfsdk.Plan{Raw: plan, Schema: schema},
				State: tfsdk.State{Raw: state, Schema: schema},
			}, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Update: %+v", resp.Diagnostics)
			}
			got := bodies()
			if len(got) != 1 {
				t.Fatalf("got %d requests, want 1", len(got))
			}
			assertBodyKey(t, got[0], "execution_identity", tc.wantBody)

			var after AgentResourceModel
			resp.Diagnostics.Append(resp.State.Get(context.Background(), &after)...)
			if tc.planType == "" && !after.ExecutionIdentity.IsNull() {
				t.Errorf("state = %v, want null (the plan)", after.ExecutionIdentity)
			}
		})
	}
}

// assertBodyKey checks that body[key] is the JSON want, or absent when want is "".
func assertBodyKey(t *testing.T, body map[string]any, key, want string) {
	t.Helper()
	v, ok := body[key]
	if want == "" {
		if ok {
			t.Fatalf("%s = %v, want it absent", key, v)
		}
		return
	}
	if !ok {
		t.Fatalf("%s absent from the request body, want %s", key, want)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, string(raw), want)
	if strings.Contains(string(raw), "null") {
		t.Errorf("%s carries a null: %s", key, raw)
	}
}
