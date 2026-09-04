// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// ---------------------------------------------------------------------------
// mapFederationIssuerToState — all three jwks branches
// ---------------------------------------------------------------------------

func federationIssuerFixture(jwksJSON string) string {
	return `{
		"id": "fdis_01ABC",
		"name": "github-actions",
		"issuer_url": "https://token.actions.githubusercontent.com",
		"jwks": ` + jwksJSON + `,
		"check_jti": true,
		"max_jwt_lifetime_seconds": 3600,
		"jwks_polling_disabled_at": null,
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-02T00:00:00Z",
		"archived_at": null,
		"created_by_actor_id": "user_01actor",
		"updated_by_actor_id": "user_01actor",
		"archived_by_actor_id": null,
		"poll_status": {"consecutive_failures": 0, "last_fetched_at": null, "next_poll_at": null},
		"type": "federation_issuer"
	}`
}

func TestMapFederationIssuerToState_Discovery(t *testing.T) {
	t.Parallel()

	const jwksJSON = `{"type": "discovery", "discovery_base": "https://alt.example.com", "ca_cert_pem": "-----BEGIN CERTIFICATE-----\nMII...\n-----END CERTIFICATE-----"}`

	var issuer anthropic.BetaFederationIssuer
	if err := json.Unmarshal([]byte(federationIssuerFixture(jwksJSON)), &issuer); err != nil {
		t.Fatalf("failed to unmarshal fixture: %s", err)
	}

	var data FederationIssuerResourceModel
	if diags := mapFederationIssuerToState(context.Background(), &issuer, &data); diags.HasError() {
		t.Fatalf("mapFederationIssuerToState returned errors: %+v", diags)
	}

	var jwks federationIssuerJWKSModel
	if diags := data.JWKS.As(context.Background(), &jwks, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("failed to read jwks: %+v", diags)
	}

	if jwks.Type.ValueString() != "discovery" {
		t.Errorf("expected type discovery, got %s", jwks.Type.ValueString())
	}
	if jwks.DiscoveryBase.ValueString() != "https://alt.example.com" {
		t.Errorf("expected discovery_base https://alt.example.com, got %s", jwks.DiscoveryBase.ValueString())
	}
	if jwks.URL.IsNull() != true {
		t.Errorf("expected url to be null for discovery, got %s", jwks.URL.ValueString())
	}
	if jwks.Keys.IsNull() != true {
		t.Errorf("expected keys to be null for discovery, got %s", jwks.Keys.ValueString())
	}
	if jwks.CACertPEM.ValueString() == "" {
		t.Error("expected ca_cert_pem to be set for discovery")
	}
}

func TestMapFederationIssuerToState_ExplicitURL(t *testing.T) {
	t.Parallel()

	const jwksJSON = `{"type": "explicit_url", "url": "https://issuer.example.com/.well-known/jwks.json"}`

	var issuer anthropic.BetaFederationIssuer
	if err := json.Unmarshal([]byte(federationIssuerFixture(jwksJSON)), &issuer); err != nil {
		t.Fatalf("failed to unmarshal fixture: %s", err)
	}

	var data FederationIssuerResourceModel
	if diags := mapFederationIssuerToState(context.Background(), &issuer, &data); diags.HasError() {
		t.Fatalf("mapFederationIssuerToState returned errors: %+v", diags)
	}

	var jwks federationIssuerJWKSModel
	if diags := data.JWKS.As(context.Background(), &jwks, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("failed to read jwks: %+v", diags)
	}

	if jwks.Type.ValueString() != "explicit_url" {
		t.Errorf("expected type explicit_url, got %s", jwks.Type.ValueString())
	}
	if jwks.URL.ValueString() != "https://issuer.example.com/.well-known/jwks.json" {
		t.Errorf("expected url to be set, got %s", jwks.URL.ValueString())
	}
	if !jwks.DiscoveryBase.IsNull() {
		t.Errorf("expected discovery_base to be null for explicit_url, got %s", jwks.DiscoveryBase.ValueString())
	}
	if !jwks.Keys.IsNull() {
		t.Errorf("expected keys to be null for explicit_url, got %s", jwks.Keys.ValueString())
	}
	if !jwks.CACertPEM.IsNull() {
		t.Errorf("expected ca_cert_pem to be null when absent from the response, got %s", jwks.CACertPEM.ValueString())
	}
}

// TestMapFederationIssuerToState_Inline verifies that the "keys" field is
// preserved via the raw wire JSON (respjson.Field.Raw()) rather than
// re-marshaled from the parsed []map[string]any, mirroring the InputSchema
// regression covered by agents.TestMapAgentResponseToState_customToolInputSchema:
// re-marshaling with encoding/json can reorder keys and drop/reorder array
// elements, which would break semantic equality against the user's
// jsonencode() output and surface as "Provider produced inconsistent result
// after apply".
func TestMapFederationIssuerToState_Inline(t *testing.T) {
	t.Parallel()

	// API response order: "kty" first.
	const apiKeys = `[{"kty":"RSA","kid":"key-1","n":"vXz...","e":"AQAB"}]`
	jwksJSON := `{"type": "inline", "keys": ` + apiKeys + `}`

	var issuer anthropic.BetaFederationIssuer
	if err := json.Unmarshal([]byte(federationIssuerFixture(jwksJSON)), &issuer); err != nil {
		t.Fatalf("failed to unmarshal fixture: %s", err)
	}

	var data FederationIssuerResourceModel
	if diags := mapFederationIssuerToState(context.Background(), &issuer, &data); diags.HasError() {
		t.Fatalf("mapFederationIssuerToState returned errors: %+v", diags)
	}

	var jwks federationIssuerJWKSModel
	if diags := data.JWKS.As(context.Background(), &jwks, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("failed to read jwks: %+v", diags)
	}

	if jwks.Type.ValueString() != "inline" {
		t.Errorf("expected type inline, got %s", jwks.Type.ValueString())
	}
	if jwks.Keys.IsNull() || jwks.Keys.IsUnknown() {
		t.Fatal("expected keys to be set for inline")
	}
	if strings.Contains(jwks.Keys.ValueString(), "ExtraFields") {
		t.Errorf("keys must not contain the ExtraFields re-marshal artifact; got %q", jwks.Keys.ValueString())
	}

	// User config re-orders keys ("e" before "kty") — must still be semantically equal.
	const userKeys = `[{"e":"AQAB","kid":"key-1","kty":"RSA","n":"vXz..."}]`
	planned := jsontypes.NewNormalizedValue(userKeys)
	equal, diags := planned.StringSemanticEquals(context.Background(), jwks.Keys)
	if diags.HasError() {
		t.Fatalf("semantic equality check returned errors: %+v", diags)
	}
	if !equal {
		t.Errorf("planned and applied keys should be semantically equal\nplanned: %s\napplied: %s", userKeys, jwks.Keys.ValueString())
	}

	if !jwks.URL.IsNull() || !jwks.DiscoveryBase.IsNull() || !jwks.CACertPEM.IsNull() {
		t.Error("expected url/discovery_base/ca_cert_pem to be null for inline")
	}
}

func TestMapFederationIssuerToState_TimestampsAndActorIDsNull(t *testing.T) {
	t.Parallel()

	const jwksJSON = `{"type": "discovery"}`

	var issuer anthropic.BetaFederationIssuer
	if err := json.Unmarshal([]byte(federationIssuerFixture(jwksJSON)), &issuer); err != nil {
		t.Fatalf("failed to unmarshal fixture: %s", err)
	}

	var data FederationIssuerResourceModel
	if diags := mapFederationIssuerToState(context.Background(), &issuer, &data); diags.HasError() {
		t.Fatalf("mapFederationIssuerToState returned errors: %+v", diags)
	}

	if !data.ArchivedAt.IsNull() {
		t.Errorf("expected archived_at to be null, got %s", data.ArchivedAt.ValueString())
	}
	if !data.ArchivedByActorID.IsNull() {
		t.Errorf("expected archived_by_actor_id to be null, got %s", data.ArchivedByActorID.ValueString())
	}
	if !data.JWKSPollingDisabledAt.IsNull() {
		t.Errorf("expected jwks_polling_disabled_at to be null, got %s", data.JWKSPollingDisabledAt.ValueString())
	}
	if data.CreatedAt.ValueString() != "2026-01-01T00:00:00Z" {
		t.Errorf("expected created_at 2026-01-01T00:00:00Z, got %s", data.CreatedAt.ValueString())
	}
	if data.CreatedByActorID.ValueString() != "user_01actor" {
		t.Errorf("expected created_by_actor_id user_01actor, got %s", data.CreatedByActorID.ValueString())
	}
	if data.CheckJTI.ValueBool() != true {
		t.Errorf("expected check_jti true, got %v", data.CheckJTI.ValueBool())
	}
	if data.MaxJWTLifetimeSeconds.ValueInt64() != 3600 {
		t.Errorf("expected max_jwt_lifetime_seconds 3600, got %d", data.MaxJWTLifetimeSeconds.ValueInt64())
	}
}

// ---------------------------------------------------------------------------
// buildJWKSParams — param-union construction per type
// ---------------------------------------------------------------------------

func jwksObjectFor(t *testing.T, values map[string]attr.Value) types.Object {
	t.Helper()
	base := map[string]attr.Value{
		"type":           types.StringNull(),
		"discovery_base": types.StringNull(),
		"url":            types.StringNull(),
		"keys":           jsontypes.NewNormalizedNull(),
		"ca_cert_pem":    types.StringNull(),
	}
	for k, v := range values {
		base[k] = v
	}
	obj, diags := types.ObjectValue(federationIssuerJWKSAttrTypes, base)
	if diags.HasError() {
		t.Fatalf("failed to build jwks object: %+v", diags)
	}
	return obj
}

func TestBuildJWKSParams_Discovery(t *testing.T) {
	t.Parallel()

	obj := jwksObjectFor(t, map[string]attr.Value{
		"type":           types.StringValue("discovery"),
		"discovery_base": types.StringValue("https://alt.example.com"),
	})

	discovery, explicitURL, inline, diags := buildJWKSParams(context.Background(), obj)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	if discovery == nil {
		t.Fatal("expected discovery variant to be set")
	}
	if explicitURL != nil || inline != nil {
		t.Error("expected only the discovery variant to be set")
	}
	if discovery.DiscoveryBase.Value != "https://alt.example.com" {
		t.Errorf("expected discovery_base to be set, got %+v", discovery.DiscoveryBase)
	}
}

func TestBuildJWKSParams_ExplicitURL(t *testing.T) {
	t.Parallel()

	obj := jwksObjectFor(t, map[string]attr.Value{
		"type": types.StringValue("explicit_url"),
		"url":  types.StringValue("https://issuer.example.com/jwks.json"),
	})

	discovery, explicitURL, inline, diags := buildJWKSParams(context.Background(), obj)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	if explicitURL == nil {
		t.Fatal("expected explicit_url variant to be set")
	}
	if discovery != nil || inline != nil {
		t.Error("expected only the explicit_url variant to be set")
	}
	if explicitURL.URL != "https://issuer.example.com/jwks.json" {
		t.Errorf("expected url to be set, got %s", explicitURL.URL)
	}
}

func TestBuildJWKSParams_Inline(t *testing.T) {
	t.Parallel()

	obj := jwksObjectFor(t, map[string]attr.Value{
		"type": types.StringValue("inline"),
		"keys": jsontypes.NewNormalizedValue(`[{"kty":"RSA","kid":"key-1","n":"vXz...","e":"AQAB"}]`),
	})

	discovery, explicitURL, inline, diags := buildJWKSParams(context.Background(), obj)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	if inline == nil {
		t.Fatal("expected inline variant to be set")
	}
	if discovery != nil || explicitURL != nil {
		t.Error("expected only the inline variant to be set")
	}
	if len(inline.Keys) != 1 || inline.Keys[0]["kid"] != "key-1" {
		t.Errorf("expected keys to be parsed, got %+v", inline.Keys)
	}
}

func TestBuildJWKSParams_InlineInvalidJSON(t *testing.T) {
	t.Parallel()

	obj := jwksObjectFor(t, map[string]attr.Value{
		"type": types.StringValue("inline"),
		"keys": jsontypes.NewNormalizedValue(`not-json`),
	})

	_, _, _, diags := buildJWKSParams(context.Background(), obj)
	if !diags.HasError() {
		t.Fatal("expected an error diagnostic for invalid keys JSON")
	}
}

// ---------------------------------------------------------------------------
// ConfigValidator — jwks matrix, including Unknown handling
// ---------------------------------------------------------------------------

func federationIssuerSchemaType(t *testing.T) tftypes.Type {
	t.Helper()
	var schemaResp resource.SchemaResponse
	r := NewFederationIssuerResource()
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	tfType, ok := schemaResp.Schema.Type().(interface {
		TerraformType(context.Context) tftypes.Type
	})
	if !ok {
		t.Fatal("schema type does not implement TerraformType")
	}
	return tfType.TerraformType(context.Background())
}

func federationIssuerNullValues(t *testing.T) map[string]tftypes.Value {
	t.Helper()
	schemaObjType := federationIssuerSchemaType(t).(tftypes.Object)
	vals := make(map[string]tftypes.Value, len(schemaObjType.AttributeTypes))
	for name, typ := range schemaObjType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	return vals
}

func makeFederationIssuerConfig(t *testing.T, rawVal tftypes.Value) tfsdk.Config {
	t.Helper()
	var schemaResp resource.SchemaResponse
	r := NewFederationIssuerResource()
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	return tfsdk.Config{
		Raw:    rawVal,
		Schema: schemaResp.Schema,
	}
}

// jwksTfValue builds a tftypes.Value for the nested "jwks" object attribute,
// filling any field not present in overrides with null.
func jwksTfValue(t *testing.T, jwksType tftypes.Object, overrides map[string]tftypes.Value) tftypes.Value {
	t.Helper()
	vals := make(map[string]tftypes.Value, len(jwksType.AttributeTypes))
	for name, typ := range jwksType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	for k, v := range overrides {
		vals[k] = v
	}
	return tftypes.NewValue(jwksType, vals)
}

func validateFederationIssuerConfig(t *testing.T, jwksOverrides map[string]tftypes.Value) resource.ValidateConfigResponse {
	t.Helper()
	vals := federationIssuerNullValues(t)
	schemaObjType := federationIssuerSchemaType(t).(tftypes.Object)
	jwksType := schemaObjType.AttributeTypes["jwks"].(tftypes.Object)
	vals["jwks"] = jwksTfValue(t, jwksType, jwksOverrides)

	rawVal := tftypes.NewValue(schemaObjType, vals)
	cfg := makeFederationIssuerConfig(t, rawVal)

	v := &federationIssuerConfigValidator{}
	var resp resource.ValidateConfigResponse
	v.ValidateResource(context.Background(), resource.ValidateConfigRequest{Config: cfg}, &resp)
	return resp
}

func hasErrorDetail(diags diag.Diagnostics, detail string) bool {
	for _, d := range diags {
		if d.Severity() == diag.SeverityError && d.Detail() == detail {
			return true
		}
	}
	return false
}

func TestFederationIssuerConfigValidator_DiscoveryValid(t *testing.T) {
	t.Parallel()
	resp := validateFederationIssuerConfig(t, map[string]tftypes.Value{
		"type": tftypes.NewValue(tftypes.String, "discovery"),
	})
	if resp.Diagnostics.HasError() {
		t.Errorf("expected no errors for a bare discovery jwks, got: %v", resp.Diagnostics)
	}
}

func TestFederationIssuerConfigValidator_ExplicitURLMissingURL(t *testing.T) {
	t.Parallel()
	resp := validateFederationIssuerConfig(t, map[string]tftypes.Value{
		"type": tftypes.NewValue(tftypes.String, "explicit_url"),
	})
	if !hasErrorDetail(resp.Diagnostics, `"url" is required when jwks.type is "explicit_url".`) {
		t.Errorf("expected url required error; got: %v", resp.Diagnostics)
	}
}

func TestFederationIssuerConfigValidator_InlineMissingKeys(t *testing.T) {
	t.Parallel()
	resp := validateFederationIssuerConfig(t, map[string]tftypes.Value{
		"type": tftypes.NewValue(tftypes.String, "inline"),
	})
	if !hasErrorDetail(resp.Diagnostics, `"keys" is required when jwks.type is "inline".`) {
		t.Errorf("expected keys required error; got: %v", resp.Diagnostics)
	}
}

func TestFederationIssuerConfigValidator_InlineConflictingCACertPEM(t *testing.T) {
	t.Parallel()
	resp := validateFederationIssuerConfig(t, map[string]tftypes.Value{
		"type":        tftypes.NewValue(tftypes.String, "inline"),
		"keys":        tftypes.NewValue(tftypes.String, `[{"kty":"RSA"}]`),
		"ca_cert_pem": tftypes.NewValue(tftypes.String, "-----BEGIN CERTIFICATE-----"),
	})
	if !hasErrorDetail(resp.Diagnostics, `"ca_cert_pem" must not be set when jwks.type is "inline".`) {
		t.Errorf("expected ca_cert_pem conflict error; got: %v", resp.Diagnostics)
	}
}

func TestFederationIssuerConfigValidator_DiscoveryConflictingURL(t *testing.T) {
	t.Parallel()
	resp := validateFederationIssuerConfig(t, map[string]tftypes.Value{
		"type": tftypes.NewValue(tftypes.String, "discovery"),
		"url":  tftypes.NewValue(tftypes.String, "https://issuer.example.com/jwks.json"),
	})
	if !hasErrorDetail(resp.Diagnostics, `"url" must not be set when jwks.type is "discovery".`) {
		t.Errorf("expected url conflict error; got: %v", resp.Diagnostics)
	}
}

// An Unknown jwks.type (e.g. an unresolved var/output ref) must skip
// validation entirely — it may resolve to a value at apply time.
func TestFederationIssuerConfigValidator_UnknownTypeSkipsValidation(t *testing.T) {
	t.Parallel()
	resp := validateFederationIssuerConfig(t, map[string]tftypes.Value{
		"type": tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
	})
	if resp.Diagnostics.HasError() {
		t.Errorf("expected no errors when jwks.type is unknown; got: %v", resp.Diagnostics)
	}
}

// An Unknown required attribute must not be reported as missing.
func TestFederationIssuerConfigValidator_UnknownRequiredNotMissing(t *testing.T) {
	t.Parallel()
	resp := validateFederationIssuerConfig(t, map[string]tftypes.Value{
		"type": tftypes.NewValue(tftypes.String, "explicit_url"),
		"url":  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
	})
	if hasErrorDetail(resp.Diagnostics, `"url" is required when jwks.type is "explicit_url".`) {
		t.Errorf("unknown url must not be flagged as missing; got: %v", resp.Diagnostics)
	}
}

// An Unknown attribute that is forbidden for the type must not be flagged as
// a conflict — it was never definitively set.
func TestFederationIssuerConfigValidator_UnknownConflictingNotFlagged(t *testing.T) {
	t.Parallel()
	resp := validateFederationIssuerConfig(t, map[string]tftypes.Value{
		"type": tftypes.NewValue(tftypes.String, "discovery"),
		"url":  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
	})
	if hasErrorDetail(resp.Diagnostics, `"url" must not be set when jwks.type is "discovery".`) {
		t.Errorf("unknown url must not be flagged as conflicting; got: %v", resp.Diagnostics)
	}
}

// An entirely Unknown jwks object (the whole nested block is an unresolved
// reference) must also skip validation.
func TestFederationIssuerConfigValidator_UnknownJWKSObjectSkipsValidation(t *testing.T) {
	t.Parallel()
	schemaObjType := federationIssuerSchemaType(t).(tftypes.Object)
	jwksType := schemaObjType.AttributeTypes["jwks"].(tftypes.Object)
	vals := federationIssuerNullValues(t)
	vals["jwks"] = tftypes.NewValue(jwksType, tftypes.UnknownValue)

	rawVal := tftypes.NewValue(schemaObjType, vals)
	cfg := makeFederationIssuerConfig(t, rawVal)

	v := &federationIssuerConfigValidator{}
	var resp resource.ValidateConfigResponse
	v.ValidateResource(context.Background(), resource.ValidateConfigRequest{Config: cfg}, &resp)
	if resp.Diagnostics.HasError() {
		t.Errorf("expected no errors when jwks is entirely unknown; got: %v", resp.Diagnostics)
	}
}

// ---------------------------------------------------------------------------
// 404-on-read
// ---------------------------------------------------------------------------

func newTestFederationClient(t *testing.T, srv *httptest.Server) *providerdata.OAuthClient {
	t.Helper()
	c := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAuthToken("test"))
	return &providerdata.OAuthClient{Client: &c}
}

func TestFederationIssuerGet_notFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"federation issuer not found"}}`)
	}))
	defer srv.Close()

	client := newTestFederationClient(t, srv)
	_, err := client.Beta.Organization.Federation.Issuers.Get(context.Background(), "fdis_missing", anthropic.BetaOrganizationFederationIssuerGetParams{})

	var apierr *anthropic.Error
	if !errors.As(err, &apierr) || apierr.StatusCode != 404 {
		t.Fatalf("expected a 404 *anthropic.Error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// archive-on-delete
// ---------------------------------------------------------------------------

func TestFederationIssuerDelete_archives(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath string
	var gotBetaHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBetaHeader = r.Header.Get("anthropic-beta")
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, federationIssuerFixture(`{"type": "discovery"}`))
	}))
	defer srv.Close()

	client := newTestFederationClient(t, srv)
	issuer, err := client.Beta.Organization.Federation.Issuers.Archive(context.Background(), "fdis_01ABC", anthropic.BetaOrganizationFederationIssuerArchiveParams{})
	if err != nil {
		t.Fatalf("archive: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/v1/organizations/federation_issuers/fdis_01ABC/archive" {
		t.Errorf("expected archive path, got %s", gotPath)
	}
	// No anthropic-beta header is required for these endpoints.
	if gotBetaHeader != "" {
		t.Errorf("expected no anthropic-beta header, got %q", gotBetaHeader)
	}
	if issuer.ID != "fdis_01ABC" {
		t.Errorf("expected issuer ID fdis_01ABC, got %s", issuer.ID)
	}
}
