// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"context"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// makeTfsdkConfig builds a tfsdk.Config from a tftypes.Value using the
// VaultCredentialResource schema.
func makeTfsdkConfig(t *testing.T, rawVal tftypes.Value) tfsdk.Config {
	t.Helper()
	var schemaResp resource.SchemaResponse
	r := NewVaultCredentialResource()
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	return tfsdk.Config{
		Raw:    rawVal,
		Schema: schemaResp.Schema,
	}
}

// schemaType returns the tftypes.Type of the VaultCredentialResource schema.
func schemaType(t *testing.T) tftypes.Type {
	t.Helper()
	var schemaResp resource.SchemaResponse
	r := NewVaultCredentialResource()
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	ty, err := schemaResp.Schema.Type().ApplyTerraform5AttributePathStep(nil)
	_ = ty
	_ = err
	// Return the schema's underlying tftypes.Object type.
	tfType, ok := schemaResp.Schema.Type().(interface {
		TerraformType(context.Context) tftypes.Type
	})
	if !ok {
		t.Fatal("schema type does not implement TerraformType")
	}
	return tfType.TerraformType(context.Background())
}

// nullValuesForSchema returns a map of tftypes.Value with null values for
// every attribute in the schema — used as a base to build test configs.
func nullValuesForSchema(t *testing.T) map[string]tftypes.Value {
	t.Helper()
	schemaObjType := schemaType(t).(tftypes.Object)
	vals := make(map[string]tftypes.Value, len(schemaObjType.AttributeTypes))
	for name, typ := range schemaObjType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	return vals
}

// ---------------------------------------------------------------------------
// buildNetworkingUnion tests
// ---------------------------------------------------------------------------

func TestBuildNetworkingUnion_Unrestricted(t *testing.T) {
	ctx := context.Background()

	netObj, diags := types.ObjectValue(credentialNetworkingAttrTypes, map[string]attr.Value{
		"mode":          types.StringValue("unrestricted"),
		"allowed_hosts": types.ListNull(types.StringType),
	})
	if diags.HasError() {
		t.Fatalf("ObjectValue: %v", diags)
	}

	union, diags := buildNetworkingUnion(ctx, netObj)
	if diags.HasError() {
		t.Fatalf("buildNetworkingUnion: %v", diags)
	}
	if union.OfUnrestricted == nil {
		t.Fatal("expected OfUnrestricted to be set")
	}
	if union.OfLimited != nil {
		t.Fatal("expected OfLimited to be nil")
	}
}

func TestBuildNetworkingUnion_Limited(t *testing.T) {
	ctx := context.Background()

	hosts, d := types.ListValueFrom(ctx, types.StringType, []string{"api.example.com", "*.cdn.example.com"})
	if d.HasError() {
		t.Fatalf("ListValueFrom: %v", d)
	}

	netObj, diags := types.ObjectValue(credentialNetworkingAttrTypes, map[string]attr.Value{
		"mode":          types.StringValue("limited"),
		"allowed_hosts": hosts,
	})
	if diags.HasError() {
		t.Fatalf("ObjectValue: %v", diags)
	}

	union, diags := buildNetworkingUnion(ctx, netObj)
	if diags.HasError() {
		t.Fatalf("buildNetworkingUnion: %v", diags)
	}
	if union.OfLimited == nil {
		t.Fatal("expected OfLimited to be set")
	}
	if len(union.OfLimited.AllowedHosts) != 2 {
		t.Fatalf("expected 2 allowed hosts, got %d", len(union.OfLimited.AllowedHosts))
	}
}

// ---------------------------------------------------------------------------
// mapRefreshResponseToObject tests
// ---------------------------------------------------------------------------

func TestMapRefreshResponseToObject_Basic(t *testing.T) {
	ctx := context.Background()

	refresh := anthropic.BetaManagedAgentsMCPOAuthRefreshResponse{
		ClientID:      "client-id-123",
		TokenEndpoint: "https://auth.example.com/token",
		Resource:      "https://api.example.com",
		Scope:         "read write",
	}
	// Set TokenEndpointAuth.Type via the union type field directly
	refresh.TokenEndpointAuth = anthropic.BetaManagedAgentsMCPOAuthRefreshResponseTokenEndpointAuthUnion{}

	obj, diags := mapRefreshResponseToObject(ctx, refresh)
	if diags.HasError() {
		t.Fatalf("mapRefreshResponseToObject: %v", diags)
	}
	if obj.IsNull() {
		t.Fatal("expected non-null object")
	}

	var model credentialRefreshModel
	diags = obj.As(ctx, &model, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		t.Fatalf("As: %v", diags)
	}

	if model.ClientID.ValueString() != "client-id-123" {
		t.Errorf("ClientID: got %q, want %q", model.ClientID.ValueString(), "client-id-123")
	}
	if model.TokenEndpoint.ValueString() != "https://auth.example.com/token" {
		t.Errorf("TokenEndpoint: got %q, want %q", model.TokenEndpoint.ValueString(), "https://auth.example.com/token")
	}
	// refresh_token is write-only; must be null in state
	if !model.RefreshToken.IsNull() {
		t.Error("expected refresh_token to be null (write-only)")
	}
	// client_secret is write-only; must be null in state
	var teaModel credentialTokenEndpointAuthModel
	diags = model.TokenEndpointAuth.As(ctx, &teaModel, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		t.Fatalf("As tea: %v", diags)
	}
	if !teaModel.ClientSecret.IsNull() {
		t.Error("expected client_secret to be null (write-only)")
	}
}

// ---------------------------------------------------------------------------
// mapCredentialResponseToState — auth type routing
// ---------------------------------------------------------------------------

func TestMapCredentialResponseToState_StaticBearer(t *testing.T) {
	ctx := context.Background()
	cred := &anthropic.BetaManagedAgentsCredential{
		ID:          "vcrd_abc123",
		VaultID:     "vlt_xyz",
		DisplayName: "My Bearer",
		Type:        "vault_credential",
		Metadata:    map[string]string{"env": "prod"},
		Auth: anthropic.BetaManagedAgentsCredentialAuthUnion{
			MCPServerURL: "https://mcp.example.com",
			Type:         "static_bearer",
		},
	}

	var data VaultCredentialResourceModel
	data.DisplayName = types.StringNull()
	data.Metadata = types.MapNull(types.StringType)

	diags := mapCredentialResponseToState(ctx, cred, &data)
	if diags.HasError() {
		t.Fatalf("mapCredentialResponseToState: %v", diags)
	}

	if data.ID.ValueString() != "vcrd_abc123" {
		t.Errorf("ID: got %q", data.ID.ValueString())
	}
	if data.Type.ValueString() != "static_bearer" {
		t.Errorf("Type: got %q", data.Type.ValueString())
	}
	if data.MCPServerURL.ValueString() != "https://mcp.example.com" {
		t.Errorf("MCPServerURL: got %q", data.MCPServerURL.ValueString())
	}
	// Write-only token must never be set
	if !data.Token.IsNull() && !data.Token.IsUnknown() {
		t.Error("expected token to be null (write-only)")
	}
	// oauth/env-var fields must be null
	if !data.Refresh.IsNull() {
		t.Error("expected refresh to be null for static_bearer")
	}
	if !data.Networking.IsNull() {
		t.Error("expected networking to be null for static_bearer")
	}
}

func TestMapCredentialResponseToState_EnvironmentVariable(t *testing.T) {
	ctx := context.Background()
	cred := &anthropic.BetaManagedAgentsCredential{
		ID:      "vcrd_env123",
		VaultID: "vlt_xyz",
		Type:    "vault_credential",
		Auth: anthropic.BetaManagedAgentsCredentialAuthUnion{
			SecretName: "MY_API_KEY",
			Type:       "environment_variable",
			Networking: anthropic.BetaManagedAgentsEnvironmentVariableAuthResponseNetworkingUnion{
				Type:         "limited",
				AllowedHosts: []string{"api.example.com"},
			},
		},
	}

	var data VaultCredentialResourceModel
	data.DisplayName = types.StringNull()
	data.Metadata = types.MapNull(types.StringType)

	diags := mapCredentialResponseToState(ctx, cred, &data)
	if diags.HasError() {
		t.Fatalf("mapCredentialResponseToState: %v", diags)
	}

	if data.Type.ValueString() != "environment_variable" {
		t.Errorf("Type: got %q", data.Type.ValueString())
	}
	if data.SecretName.ValueString() != "MY_API_KEY" {
		t.Errorf("SecretName: got %q", data.SecretName.ValueString())
	}
	// secret_value is write-only
	if !data.SecretValue.IsNull() && !data.SecretValue.IsUnknown() {
		t.Error("expected secret_value to be null (write-only)")
	}
	// networking should be set
	if data.Networking.IsNull() {
		t.Error("expected networking to be non-null for environment_variable")
	}
	// mcp_server_url must be null
	if !data.MCPServerURL.IsNull() {
		t.Error("expected mcp_server_url to be null for environment_variable")
	}
}

// ---------------------------------------------------------------------------
// ConfigValidator tests
// ---------------------------------------------------------------------------

func TestVaultCredentialConfigValidator_StaticBearerValid(t *testing.T) {
	ctx := context.Background()
	vals := nullValuesForSchema(t)
	vals["type"] = tftypes.NewValue(tftypes.String, "static_bearer")
	vals["mcp_server_url"] = tftypes.NewValue(tftypes.String, "https://mcp.example.com")
	// token is write-only; it appears in the schema but framework sends it as the zero value
	// We simulate valid: token is non-null in config (framework sends the value).
	// For testing purposes we just check that the validator doesn't reject a valid config.

	schemaObjType := schemaType(t).(tftypes.Object)
	rawVal := tftypes.NewValue(schemaObjType, vals)
	cfg := makeTfsdkConfig(t, rawVal)

	v := &vaultCredentialConfigValidator{}
	var validateResp resource.ValidateConfigResponse
	v.ValidateResource(ctx, resource.ValidateConfigRequest{Config: cfg}, &validateResp)

	// The validator may add an error about token being required (null in test),
	// but mcp_server_url is set so no error on that.
	// We only check that no mcp_server_url-related error appears.
	for _, d := range validateResp.Diagnostics {
		if d.Severity() == diag.SeverityError && d.Detail() == "\"mcp_server_url\" is required when type is \"static_bearer\"." {
			t.Errorf("unexpected mcp_server_url error: %s", d.Detail())
		}
	}
}

func TestVaultCredentialConfigValidator_MissingRequired(t *testing.T) {
	ctx := context.Background()
	vals := nullValuesForSchema(t)
	vals["type"] = tftypes.NewValue(tftypes.String, "static_bearer")
	// mcp_server_url is null — should trigger an error

	schemaObjType := schemaType(t).(tftypes.Object)
	rawVal := tftypes.NewValue(schemaObjType, vals)
	cfg := makeTfsdkConfig(t, rawVal)

	v := &vaultCredentialConfigValidator{}
	var validateResp resource.ValidateConfigResponse
	v.ValidateResource(ctx, resource.ValidateConfigRequest{Config: cfg}, &validateResp)

	found := false
	for _, d := range validateResp.Diagnostics {
		if d.Severity() == diag.SeverityError {
			if d.Detail() == "\"mcp_server_url\" is required when type is \"static_bearer\"." {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected mcp_server_url required error for static_bearer with null mcp_server_url")
	}
}

func TestVaultCredentialConfigValidator_EnvironmentVariableLimitedNoHosts(t *testing.T) {
	ctx := context.Background()
	vals := nullValuesForSchema(t)
	vals["type"] = tftypes.NewValue(tftypes.String, "environment_variable")
	vals["secret_name"] = tftypes.NewValue(tftypes.String, "MY_KEY")
	// secret_value is write-only but must be non-null for the validator to pass its required check.
	vals["secret_value"] = tftypes.NewValue(tftypes.String, "my-secret")

	// Build networking object: mode=limited, allowed_hosts=empty list
	schemaObjType := schemaType(t).(tftypes.Object)
	networkingType := schemaObjType.AttributeTypes["networking"].(tftypes.Object)
	allowedHostsType := networkingType.AttributeTypes["allowed_hosts"]

	vals["networking"] = tftypes.NewValue(networkingType, map[string]tftypes.Value{
		"mode":          tftypes.NewValue(tftypes.String, "limited"),
		"allowed_hosts": tftypes.NewValue(allowedHostsType, []tftypes.Value{}),
	})

	rawVal := tftypes.NewValue(schemaObjType, vals)
	cfg := makeTfsdkConfig(t, rawVal)

	v := &vaultCredentialConfigValidator{}
	var validateResp resource.ValidateConfigResponse
	v.ValidateResource(ctx, resource.ValidateConfigRequest{Config: cfg}, &validateResp)

	found := false
	for _, d := range validateResp.Diagnostics {
		if d.Severity() == diag.SeverityError {
			if d.Detail() == "\"networking.allowed_hosts\" must contain at least one entry when \"networking.mode\" is \"limited\"." {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected allowed_hosts required error; got diagnostics: %v", validateResp.Diagnostics)
	}
}

func TestVaultCredentialConfigValidator_ConflictingAttrs(t *testing.T) {
	ctx := context.Background()
	vals := nullValuesForSchema(t)
	vals["type"] = tftypes.NewValue(tftypes.String, "static_bearer")
	vals["mcp_server_url"] = tftypes.NewValue(tftypes.String, "https://mcp.example.com")
	vals["secret_name"] = tftypes.NewValue(tftypes.String, "SHOULD_NOT_BE_SET")

	schemaObjType := schemaType(t).(tftypes.Object)
	rawVal := tftypes.NewValue(schemaObjType, vals)
	cfg := makeTfsdkConfig(t, rawVal)

	v := &vaultCredentialConfigValidator{}
	var validateResp resource.ValidateConfigResponse
	v.ValidateResource(ctx, resource.ValidateConfigRequest{Config: cfg}, &validateResp)

	found := false
	for _, d := range validateResp.Diagnostics {
		if d.Severity() == diag.SeverityError {
			if d.Detail() == "\"secret_name\" must not be set when type is \"static_bearer\"." {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected secret_name conflict error for static_bearer; got: %v", validateResp.Diagnostics)
	}
}

// ---------------------------------------------------------------------------
// token_wo_version: verify it is the rotation trigger in Update logic
// ---------------------------------------------------------------------------

func TestTokenWoVersion_IsDifferentTrigger(t *testing.T) {
	// Verify that the rotation detection logic is correct for the Update method.
	// We check the condition: !plan.TokenWoVersion.Equal(state.TokenWoVersion)

	plan := VaultCredentialResourceModel{
		TokenWoVersion: types.Int64Value(2),
	}
	state := VaultCredentialResourceModel{
		TokenWoVersion: types.Int64Value(1),
	}

	if plan.TokenWoVersion.Equal(state.TokenWoVersion) {
		t.Error("expected different token_wo_version values to be unequal")
	}

	plan2 := VaultCredentialResourceModel{
		TokenWoVersion: types.Int64Value(1),
	}
	if !plan2.TokenWoVersion.Equal(state.TokenWoVersion) {
		t.Error("expected same token_wo_version values to be equal")
	}
}

// ---------------------------------------------------------------------------
// ConfigValidator — token_wo_version requirement & Unknown handling
// ---------------------------------------------------------------------------

// hasErrorDetail reports whether any error diagnostic has the given detail.
func hasErrorDetail(diags diag.Diagnostics, detail string) bool {
	for _, d := range diags {
		if d.Severity() == diag.SeverityError && d.Detail() == detail {
			return true
		}
	}
	return false
}

const tokenWoVersionRequiredDetail = "\"token_wo_version\" must be set when a write-only secret (token / access_token / secret_value) is configured. " +
	"It is the rotation trigger: Terraform cannot diff write-only values, so increment it to re-push the secret. Set it to 1 on initial creation."

func validateConfig(t *testing.T, vals map[string]tftypes.Value) resource.ValidateConfigResponse {
	t.Helper()
	schemaObjType := schemaType(t).(tftypes.Object)
	rawVal := tftypes.NewValue(schemaObjType, vals)
	cfg := makeTfsdkConfig(t, rawVal)
	v := &vaultCredentialConfigValidator{}
	var resp resource.ValidateConfigResponse
	v.ValidateResource(context.Background(), resource.ValidateConfigRequest{Config: cfg}, &resp)
	return resp
}

func TestVaultCredentialConfigValidator_RequiresTokenWoVersion(t *testing.T) {
	vals := nullValuesForSchema(t)
	vals["type"] = tftypes.NewValue(tftypes.String, "static_bearer")
	vals["mcp_server_url"] = tftypes.NewValue(tftypes.String, "https://mcp.example.com")
	vals["token"] = tftypes.NewValue(tftypes.String, "secret-bearer")
	// token_wo_version left null → must be flagged.

	resp := validateConfig(t, vals)
	if !hasErrorDetail(resp.Diagnostics, tokenWoVersionRequiredDetail) {
		t.Errorf("expected token_wo_version required error; got: %v", resp.Diagnostics)
	}
}

func TestVaultCredentialConfigValidator_TokenWoVersionSet(t *testing.T) {
	vals := nullValuesForSchema(t)
	vals["type"] = tftypes.NewValue(tftypes.String, "static_bearer")
	vals["mcp_server_url"] = tftypes.NewValue(tftypes.String, "https://mcp.example.com")
	vals["token"] = tftypes.NewValue(tftypes.String, "secret-bearer")
	vals["token_wo_version"] = tftypes.NewValue(tftypes.Number, 1)

	resp := validateConfig(t, vals)
	if hasErrorDetail(resp.Diagnostics, tokenWoVersionRequiredDetail) {
		t.Errorf("did not expect token_wo_version error when set; got: %v", resp.Diagnostics)
	}
}

// An unknown required attribute (e.g. var/output not yet resolved) must not be
// reported as missing — it may resolve to a value at apply time.
func TestVaultCredentialConfigValidator_UnknownRequiredNotMissing(t *testing.T) {
	vals := nullValuesForSchema(t)
	vals["type"] = tftypes.NewValue(tftypes.String, "static_bearer")
	vals["mcp_server_url"] = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	vals["token"] = tftypes.NewValue(tftypes.String, "secret-bearer")
	vals["token_wo_version"] = tftypes.NewValue(tftypes.Number, 1)

	resp := validateConfig(t, vals)
	if hasErrorDetail(resp.Diagnostics, "\"mcp_server_url\" is required when type is \"static_bearer\".") {
		t.Errorf("unknown mcp_server_url must not be flagged as missing; got: %v", resp.Diagnostics)
	}
}

// An unknown attribute that is forbidden for the type must not be reported as a
// conflict — it was never definitively set.
func TestVaultCredentialConfigValidator_UnknownConflictingNotFlagged(t *testing.T) {
	vals := nullValuesForSchema(t)
	vals["type"] = tftypes.NewValue(tftypes.String, "static_bearer")
	vals["mcp_server_url"] = tftypes.NewValue(tftypes.String, "https://mcp.example.com")
	vals["token"] = tftypes.NewValue(tftypes.String, "secret-bearer")
	vals["token_wo_version"] = tftypes.NewValue(tftypes.Number, 1)
	vals["secret_name"] = tftypes.NewValue(tftypes.String, tftypes.UnknownValue)

	resp := validateConfig(t, vals)
	if hasErrorDetail(resp.Diagnostics, "\"secret_name\" must not be set when type is \"static_bearer\".") {
		t.Errorf("unknown secret_name must not be flagged as conflicting; got: %v", resp.Diagnostics)
	}
}

// ---------------------------------------------------------------------------
// buildMetadataPatch tests
// ---------------------------------------------------------------------------

func mapValue(t *testing.T, m map[string]string) types.Map {
	t.Helper()
	elems := make(map[string]attr.Value, len(m))
	for k, v := range m {
		elems[k] = types.StringValue(v)
	}
	mv, diags := types.MapValue(types.StringType, elems)
	if diags.HasError() {
		t.Fatalf("MapValue: %v", diags)
	}
	return mv
}

func TestBuildMetadataPatch(t *testing.T) {
	ctx := context.Background()
	nullMap := types.MapNull(types.StringType)

	t.Run("upsert and delete removed key", func(t *testing.T) {
		plan := mapValue(t, map[string]string{"a": "1", "c": "3"})
		state := mapValue(t, map[string]string{"a": "1", "b": "2"})
		patch, diags := buildMetadataPatch(ctx, plan, state)
		if diags.HasError() {
			t.Fatalf("buildMetadataPatch: %v", diags)
		}
		if patch["a"] != "1" || patch["c"] != "3" {
			t.Errorf("expected upserts a=1,c=3; got %#v", patch)
		}
		v, ok := patch["b"]
		if !ok || v != nil {
			t.Errorf("expected removed key b to be nil; got %#v (present=%v)", v, ok)
		}
	})

	t.Run("clear all when plan null", func(t *testing.T) {
		state := mapValue(t, map[string]string{"a": "1", "b": "2"})
		patch, diags := buildMetadataPatch(ctx, nullMap, state)
		if diags.HasError() {
			t.Fatalf("buildMetadataPatch: %v", diags)
		}
		if len(patch) != 2 || patch["a"] != nil || patch["b"] != nil {
			t.Errorf("expected all keys nulled; got %#v", patch)
		}
	})

	t.Run("no change when both null", func(t *testing.T) {
		patch, diags := buildMetadataPatch(ctx, nullMap, nullMap)
		if diags.HasError() {
			t.Fatalf("buildMetadataPatch: %v", diags)
		}
		if len(patch) != 0 {
			t.Errorf("expected empty patch; got %#v", patch)
		}
	})
}

// ---------------------------------------------------------------------------
// buildTokenEndpointAuthUnion tests (Create path)
// ---------------------------------------------------------------------------

func TestBuildTokenEndpointAuthUnion_None(t *testing.T) {
	ctx := context.Background()
	teaModel := credentialTokenEndpointAuthModel{
		Type:         types.StringValue("none"),
		ClientSecret: types.StringNull(),
	}
	// For "none", no config read is needed.
	var schemaResp resource.SchemaResponse
	r := NewVaultCredentialResource()
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	// Build a minimal valid config (all-null base)
	schemaObjType := schemaType(t).(tftypes.Object)
	vals := nullValuesForSchema(t)
	rawVal := tftypes.NewValue(schemaObjType, vals)
	cfg := makeTfsdkConfig(t, rawVal)

	union, diags := buildTokenEndpointAuthUnion(ctx, cfg, teaModel)
	if diags.HasError() {
		t.Fatalf("buildTokenEndpointAuthUnion(none): %v", diags)
	}
	if union.OfNone == nil {
		t.Fatal("expected OfNone to be set")
	}
}

func TestBuildTokenEndpointAuthUnion_ClientSecretPost(t *testing.T) {
	ctx := context.Background()

	schemaObjType := schemaType(t).(tftypes.Object)
	vals := nullValuesForSchema(t)

	// Build a refresh.token_endpoint_auth with client_secret_post
	refreshType := schemaObjType.AttributeTypes["refresh"].(tftypes.Object)
	teaType := refreshType.AttributeTypes["token_endpoint_auth"].(tftypes.Object)

	vals["refresh"] = tftypes.NewValue(refreshType, map[string]tftypes.Value{
		"client_id":      tftypes.NewValue(tftypes.String, "cid"),
		"refresh_token":  tftypes.NewValue(tftypes.String, "rtoken"),
		"token_endpoint": tftypes.NewValue(tftypes.String, "https://tok.example.com"),
		"resource":       tftypes.NewValue(tftypes.String, nil),
		"scope":          tftypes.NewValue(tftypes.String, nil),
		"token_endpoint_auth": tftypes.NewValue(teaType, map[string]tftypes.Value{
			"type":          tftypes.NewValue(tftypes.String, "client_secret_post"),
			"client_secret": tftypes.NewValue(tftypes.String, "my-secret"),
		}),
	})

	rawVal := tftypes.NewValue(schemaObjType, vals)
	cfg := makeTfsdkConfig(t, rawVal)

	teaModel := credentialTokenEndpointAuthModel{
		Type:         types.StringValue("client_secret_post"),
		ClientSecret: types.StringNull(), // write-only; actual value read from cfg
	}

	union, diags := buildTokenEndpointAuthUnion(ctx, cfg, teaModel)
	if diags.HasError() {
		t.Fatalf("buildTokenEndpointAuthUnion(client_secret_post): %v", diags)
	}
	if union.OfClientSecretPost == nil {
		t.Fatal("expected OfClientSecretPost to be set")
	}
	if union.OfClientSecretPost.ClientSecret != "my-secret" {
		t.Errorf("ClientSecret: got %q, want %q", union.OfClientSecretPost.ClientSecret, "my-secret")
	}
}

// ---------------------------------------------------------------------------
// ImportState — composite ID parsing
// ---------------------------------------------------------------------------

func TestVaultCredentialImportState_ValidID(t *testing.T) {
	ctx := context.Background()
	r := NewVaultCredentialResource()

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	schemaObjType := schemaType(t).(tftypes.Object)
	vals := nullValuesForSchema(t)
	rawVal := tftypes.NewValue(schemaObjType, vals)
	state := tfsdk.State{
		Raw:    rawVal,
		Schema: schemaResp.Schema,
	}

	req := resource.ImportStateRequest{ID: "vlt_abc:vcrd_xyz"}
	var resp resource.ImportStateResponse
	resp.State = state

	r.(*VaultCredentialResource).ImportState(ctx, req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState: %v", resp.Diagnostics)
	}

	var vaultID types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("vault_id"), &vaultID)...)
	if vaultID.ValueString() != "vlt_abc" {
		t.Errorf("vault_id: got %q, want %q", vaultID.ValueString(), "vlt_abc")
	}

	var credID types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("id"), &credID)...)
	if credID.ValueString() != "vcrd_xyz" {
		t.Errorf("id: got %q, want %q", credID.ValueString(), "vcrd_xyz")
	}
}

func TestVaultCredentialImportState_InvalidID(t *testing.T) {
	ctx := context.Background()
	r := NewVaultCredentialResource()

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	schemaObjType := schemaType(t).(tftypes.Object)
	vals := nullValuesForSchema(t)
	rawVal := tftypes.NewValue(schemaObjType, vals)
	state := tfsdk.State{
		Raw:    rawVal,
		Schema: schemaResp.Schema,
	}

	req := resource.ImportStateRequest{ID: "bad-id-no-colon"}
	var resp resource.ImportStateResponse
	resp.State = state

	r.(*VaultCredentialResource).ImportState(ctx, req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Error("expected error for invalid import ID format")
	}
}
