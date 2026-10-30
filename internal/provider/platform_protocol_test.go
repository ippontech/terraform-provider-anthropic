// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// protocolServer is the provider as Terraform drives it: the framework's
// protocol 6 server, in process, without the Terraform CLI or any network.
type protocolServer struct {
	t      *testing.T
	server tfprotov6.ProviderServer
	schema *tfprotov6.GetProviderSchemaResponse
}

func newProtocolServer(t *testing.T) *protocolServer {
	t.Helper()
	server, err := providerserver.NewProtocol6WithError(New("test")())()
	if err != nil {
		t.Fatal(err)
	}
	schema, err := server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || hasProtocolError(schema.Diagnostics) {
		t.Fatalf("GetProviderSchema: %v %v", err, schema.Diagnostics)
	}
	return &protocolServer{t: t, server: server, schema: schema}
}

func hasProtocolError(diags []*tfprotov6.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			return true
		}
	}
	return false
}

// dynamicValue encodes an object of typ holding set, every other attribute
// null.
func (s *protocolServer) dynamicValue(typ tftypes.Type, set map[string]tftypes.Value) *tfprotov6.DynamicValue {
	s.t.Helper()
	obj, ok := typ.(tftypes.Object)
	if !ok {
		s.t.Fatalf("%v is not an object type", typ)
	}
	values := map[string]tftypes.Value{}
	for name, attrType := range obj.AttributeTypes {
		values[name] = tftypes.NewValue(attrType, nil)
	}
	for name, v := range set {
		values[name] = v
	}
	dv, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, values))
	if err != nil {
		s.t.Fatal(err)
	}
	return &dv
}

func (s *protocolServer) configure(set map[string]tftypes.Value) []*tfprotov6.Diagnostic {
	s.t.Helper()
	resp, err := s.server.ConfigureProvider(context.Background(), &tfprotov6.ConfigureProviderRequest{
		TerraformVersion: "1.16.6",
		Config:           s.dynamicValue(s.schema.Provider.ValueType(), set),
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return resp.Diagnostics
}

func (s *protocolServer) planCreate(typeName string, set map[string]tftypes.Value) []*tfprotov6.Diagnostic {
	s.t.Helper()
	typ := s.schema.ResourceSchemas[typeName].ValueType()
	config := s.dynamicValue(typ, set)
	prior, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, nil))
	if err != nil {
		s.t.Fatal(err)
	}
	resp, err := s.server.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, PriorState: &prior, ProposedNewState: config, Config: config,
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return resp.Diagnostics
}

func (s *protocolServer) validateResource(typeName string, set map[string]tftypes.Value) []*tfprotov6.Diagnostic {
	s.t.Helper()
	resp, err := s.server.ValidateResourceConfig(context.Background(), &tfprotov6.ValidateResourceConfigRequest{
		TypeName: typeName, Config: s.dynamicValue(s.schema.ResourceSchemas[typeName].ValueType(), set),
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return resp.Diagnostics
}

func (s *protocolServer) readDataSource(typeName string, set map[string]tftypes.Value) []*tfprotov6.Diagnostic {
	s.t.Helper()
	resp, err := s.server.ReadDataSource(context.Background(), &tfprotov6.ReadDataSourceRequest{
		TypeName: typeName, Config: s.dynamicValue(s.schema.DataSourceSchemas[typeName].ValueType(), set),
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return resp.Diagnostics
}

func assertProtocolUnavailable(t *testing.T, diags []*tfprotov6.Diagnostic, typeName, platformName string) {
	t.Helper()
	want := typeName + " Not Available on " + platformName
	for _, d := range diags {
		if d.Severity == tfprotov6.DiagnosticSeverityError && d.Summary == want {
			return
		}
	}
	t.Errorf("diagnostics %v do not contain the error %q", summaries(diags), want)
}

func summaries(diags []*tfprotov6.Diagnostic) []string {
	var out []string
	for _, d := range diags {
		out = append(out, d.Summary+": "+d.Detail)
	}
	return out
}

// TestAWSGateThroughTheProtocol plays Terraform's sequence for a plan against
// an aws alias: validation before the provider is configured (nothing is
// refused), then ConfigureProvider, then planning. A type the gateway does not
// serve fails at plan time with the platform's diagnostic; a type it serves
// plans without sending anything (the base URL is a closed local port).
//
// This is the offline counterpart of an `expect_failures` native test, which
// Terraform cannot express: expect_failures matches custom condition
// failures, not a provider's error diagnostics.
func TestAWSGateThroughTheProtocol(t *testing.T) {
	isolateAWSEnv(t)
	exportFirstPartyEnvironment(t)
	s := newProtocolServer(t)

	invite := map[string]tftypes.Value{"email": tfString("new-teammate@example.com"), "role": tfString("developer")}
	if diags := s.validateResource("anthropic_invite", invite); hasProtocolError(diags) {
		t.Fatalf("validation before Configure refused the invite: %v", summaries(diags))
	}

	awsType := s.schema.Provider.ValueType().(tftypes.Object).AttributeTypes["aws"]
	block := map[string]tftypes.Value{}
	for name, attrType := range awsType.(tftypes.Object).AttributeTypes {
		block[name] = tftypes.NewValue(attrType, nil)
	}
	block["region"] = tfString(awsTestRegion)
	block["access_key"] = tfString(awsTestAccessKey)
	block["secret_key"] = tfString(awsTestSecretKey)
	block["base_url"] = tfString("http://127.0.0.1:9")
	if diags := s.configure(map[string]tftypes.Value{
		"workspace_id": tfString(awsTestWorkspace),
		"aws":          tftypes.NewValue(awsType, block),
	}); hasProtocolError(diags) {
		t.Fatalf("ConfigureProvider: %v", summaries(diags))
	}

	assertProtocolUnavailable(t, s.planCreate("anthropic_invite", invite), "anthropic_invite", "Claude Platform on AWS")
	assertProtocolUnavailable(t, s.planCreate("anthropic_workspace_member", map[string]tftypes.Value{
		"workspace_id": tfString("wrkspc_1"), "user_id": tfString("user_1"), "workspace_role": tfString("workspace_user"),
	}), "anthropic_workspace_member", "Claude Platform on AWS")
	assertProtocolUnavailable(t, s.readDataSource("anthropic_organization", nil), "anthropic_organization", "Claude Platform on AWS")

	for typeName, set := range map[string]map[string]tftypes.Value{
		"anthropic_agent":     {"name": tfString("Simple Agent"), "model": tfString("claude-sonnet-5-5")},
		"anthropic_workspace": {"name": tfString("platform-workspace")},
	} {
		if diags := s.planCreate(typeName, set); hasProtocolError(diags) {
			t.Errorf("%s: plan failed on AWS: %v", typeName, summaries(diags))
		}
	}

	// The detail names the reason and the documentation page.
	diags := s.planCreate("anthropic_invite", invite)
	if len(diags) == 0 || !strings.Contains(diags[0].Detail, "AWS IAM manages organization membership") ||
		!strings.Contains(diags[0].Detail, "https://platform.claude.com/docs/en/build-with-claude/claude-platform-on-aws") {
		t.Errorf("detail = %v", summaries(diags))
	}
}

// TestGoogleCloudGateThroughTheProtocol: TestAWSGateThroughTheProtocol for a
// google_cloud alias, with a static access token so that no credential is
// looked up, and a closed local base URL.
func TestGoogleCloudGateThroughTheProtocol(t *testing.T) {
	isolateGoogleEnv(t)
	exportFirstPartyEnvironment(t)
	s := newProtocolServer(t)

	gcType := s.schema.Provider.ValueType().(tftypes.Object).AttributeTypes["google_cloud"]
	block := map[string]tftypes.Value{}
	for name, attrType := range gcType.(tftypes.Object).AttributeTypes {
		block[name] = tftypes.NewValue(attrType, nil)
	}
	block["project"] = tfString(gcTestProject)
	block["access_token"] = tfString(gcTestToken)
	block["base_url"] = tfString("http://127.0.0.1:9")
	if diags := s.configure(map[string]tftypes.Value{
		"workspace_id": tfString(gcTestWorkspace),
		"google_cloud": tftypes.NewValue(gcType, block),
	}); hasProtocolError(diags) {
		t.Fatalf("ConfigureProvider: %v", summaries(diags))
	}

	const name = "Claude Models on Google Cloud (Anthropic managed)"
	assertProtocolUnavailable(t, s.planCreate("anthropic_workspace", map[string]tftypes.Value{"name": tfString("w")}), "anthropic_workspace", name)
	assertProtocolUnavailable(t, s.readDataSource("anthropic_workspaces", nil), "anthropic_workspaces", name)
	assertProtocolUnavailable(t, s.planCreate("anthropic_invite", map[string]tftypes.Value{
		"email": tfString("new-teammate@example.com"), "role": tfString("developer"),
	}), "anthropic_invite", name)
	if diags := s.planCreate("anthropic_agent", map[string]tftypes.Value{"name": tfString("Simple Agent"), "model": tfString("claude-opus-5-5")}); hasProtocolError(diags) {
		t.Errorf("anthropic_agent: plan failed on Google Cloud: %v", summaries(diags))
	}
}
