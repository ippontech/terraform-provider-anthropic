// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

// ---------------------------------------------------------------------------
// mapFederationIssuerToState — all three jwks branches
// ---------------------------------------------------------------------------

func federationIssuerFixture(jwksJSON string) string {
	return federationIssuerFixtureWith(jwksJSON, `null`, `{"consecutive_failures": 0, "last_fetched_at": null, "next_poll_at": null}`)
}

// federationIssuerFixtureWith builds an issuer payload with the given raw JSON
// for jwks_polling_disabled_at and poll_status (an empty pollStatusJSON omits
// the field).
func federationIssuerFixtureWith(jwksJSON, pollingDisabledAtJSON, pollStatusJSON string) string {
	pollStatus := ""
	if pollStatusJSON != "" {
		pollStatus = `"poll_status": ` + pollStatusJSON + `,`
	}
	return `{
		"id": "fdis_01ABC",
		"name": "github-actions",
		"issuer_url": "https://token.actions.githubusercontent.com",
		"jwks": ` + jwksJSON + `,
		"check_jti": true,
		"max_jwt_lifetime_seconds": 3600,
		"jwks_polling_disabled_at": ` + pollingDisabledAtJSON + `,
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-02T00:00:00Z",
		"archived_at": null,
		"created_by_actor_id": "user_01actor",
		"updated_by_actor_id": "user_01actor",
		"archived_by_actor_id": null,
		` + pollStatus + `
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
	maps.Copy(base, values)
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
	maps.Copy(vals, overrides)
	return tftypes.NewValue(jwksType, vals)
}

func validateFederationIssuerConfig(t *testing.T, jwksOverrides map[string]tftypes.Value) resource.ValidateConfigResponse {
	t.Helper()
	vals := schematest.NullValues(t, NewFederationIssuerResource())
	schemaObjType := schematest.ResourceObjectType(t, NewFederationIssuerResource())
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
	schemaObjType := schematest.ResourceObjectType(t, NewFederationIssuerResource())
	jwksType := schemaObjType.AttributeTypes["jwks"].(tftypes.Object)
	vals := schematest.NullValues(t, NewFederationIssuerResource())
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

func TestFederationIssuerGet_notFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"federation issuer not found"}}`)
	}))
	defer srv.Close()

	client := oauthtest.NewClient(t, srv)
	_, err := client.Beta.Organization.Federation.Issuers.Get(context.Background(), "fdis_missing", anthropic.BetaOrganizationFederationIssuerGetParams{})

	var apierr *anthropic.Error
	if !errors.As(err, &apierr) || apierr.StatusCode != http.StatusNotFound {
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

	client := oauthtest.NewClient(t, srv)
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

// ---------------------------------------------------------------------------
// poll_status mapping
// ---------------------------------------------------------------------------

func TestMapFederationIssuerToState_PollStatus(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		pollStatusJSON string
		wantNull       bool
		wantFailures   int64
		wantLast       string // "" means null
		wantNext       string // "" means null
	}{
		"polling": {
			pollStatusJSON: `{"consecutive_failures": 0, "last_fetched_at": "2026-06-01T12:00:00Z", "next_poll_at": "2026-06-01T13:00:00Z"}`,
			wantLast:       "2026-06-01T12:00:00Z",
			wantNext:       "2026-06-01T13:00:00Z",
		},
		"paused": {
			pollStatusJSON: `{"consecutive_failures": 7, "last_fetched_at": "2026-06-01T12:00:00Z", "next_poll_at": null}`,
			wantFailures:   7,
			wantLast:       "2026-06-01T12:00:00Z",
		},
		"never fetched": {
			pollStatusJSON: `{"consecutive_failures": 0, "last_fetched_at": null, "next_poll_at": null}`,
		},
		// Inline-key issuers, or polling not started: the API sends null,
		// which must not become an all-zero object.
		"null":    {pollStatusJSON: `null`, wantNull: true},
		"omitted": {pollStatusJSON: ``, wantNull: true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var issuer anthropic.BetaFederationIssuer
			if err := json.Unmarshal([]byte(federationIssuerFixtureWith(`{"type": "discovery"}`, `null`, tc.pollStatusJSON)), &issuer); err != nil {
				t.Fatalf("failed to unmarshal fixture: %s", err)
			}

			var data FederationIssuerResourceModel
			if diags := mapFederationIssuerToState(context.Background(), &issuer, &data); diags.HasError() {
				t.Fatalf("mapFederationIssuerToState returned errors: %+v", diags)
			}

			if tc.wantNull {
				if !data.PollStatus.IsNull() {
					t.Fatalf("poll_status = %v, want null", data.PollStatus)
				}
				return
			}
			if data.PollStatus.IsNull() || data.PollStatus.IsUnknown() {
				t.Fatalf("poll_status = %v, want a known object", data.PollStatus)
			}

			attrs := data.PollStatus.Attributes()
			if got := attrs["consecutive_failures"].(types.Int64).ValueInt64(); got != tc.wantFailures {
				t.Errorf("consecutive_failures = %d, want %d", got, tc.wantFailures)
			}
			for field, want := range map[string]string{"last_fetched_at": tc.wantLast, "next_poll_at": tc.wantNext} {
				got := attrs[field].(types.String)
				if want == "" {
					if !got.IsNull() {
						t.Errorf("%s = %q, want null", field, got.ValueString())
					}
					continue
				}
				if got.ValueString() != want {
					t.Errorf("%s = %q, want %q", field, got.ValueString(), want)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// jwks_polling_disabled: validator, plan and update body
// ---------------------------------------------------------------------------

func TestFederationIssuerSchema_JWKSPollingDisabledOnlyAcceptsFalse(t *testing.T) {
	t.Parallel()

	attr, ok := schematest.ResourceSchema(t, NewFederationIssuerResource()).Attributes["jwks_polling_disabled"].(rschema.BoolAttribute)
	if !ok {
		t.Fatal("jwks_polling_disabled is not a BoolAttribute")
	}
	if !attr.Optional || attr.Computed {
		t.Errorf("jwks_polling_disabled must be Optional and not Computed (the API never returns it), got Optional=%v Computed=%v", attr.Optional, attr.Computed)
	}

	for value, wantErr := range map[bool]bool{false: false, true: true} {
		var resp validator.BoolResponse
		for _, v := range attr.Validators {
			v.ValidateBool(context.Background(), validator.BoolRequest{
				Path:        path.Root("jwks_polling_disabled"),
				ConfigValue: types.BoolValue(value),
			}, &resp)
		}
		if resp.Diagnostics.HasError() != wantErr {
			t.Errorf("jwks_polling_disabled = %v: error = %v, want %v (%v)", value, resp.Diagnostics.HasError(), wantErr, resp.Diagnostics)
		}
	}
}

// federationIssuerStateModel returns the model a refresh would leave in state
// for an issuer whose polling was paused at pausedAt ("" for active polling).
func federationIssuerStateModel(t *testing.T, pausedAt string) FederationIssuerResourceModel {
	t.Helper()

	pollingDisabledAt := `null`
	pollStatus := `{"consecutive_failures": 0, "last_fetched_at": "2026-06-01T12:00:00Z", "next_poll_at": "2026-06-01T13:00:00Z"}`
	if pausedAt != "" {
		pollingDisabledAt = `"` + pausedAt + `"`
		pollStatus = `{"consecutive_failures": 12, "last_fetched_at": "2026-06-01T12:00:00Z", "next_poll_at": null}`
	}

	var issuer anthropic.BetaFederationIssuer
	if err := json.Unmarshal([]byte(federationIssuerFixtureWith(`{"type": "discovery"}`, pollingDisabledAt, pollStatus)), &issuer); err != nil {
		t.Fatalf("failed to unmarshal fixture: %s", err)
	}
	var data FederationIssuerResourceModel
	if diags := mapFederationIssuerToState(context.Background(), &issuer, &data); diags.HasError() {
		t.Fatalf("mapFederationIssuerToState returned errors: %+v", diags)
	}
	return data
}

func TestBuildFederationIssuerUpdateParams_JWKSPollingDisabled(t *testing.T) {
	t.Parallel()

	const pausedAt = "2026-06-02T00:00:00Z"

	cases := map[string]struct {
		pausedAt     string
		archived     bool
		planValue    types.Bool
		rename       bool
		wantBody     string
		wantSend     bool
		wantReenable bool
	}{
		"paused and requested": {
			pausedAt: pausedAt, planValue: types.BoolValue(false),
			wantBody: `{"jwks_polling_disabled":false}`, wantSend: true, wantReenable: true,
		},
		"paused and requested with a rename": {
			pausedAt: pausedAt, planValue: types.BoolValue(false), rename: true,
			wantBody: `{"jwks_polling_disabled":false,"name":"renamed"}`, wantSend: true, wantReenable: true,
		},
		// Polling is active: nothing to re-enable, and nothing else changed.
		"active and requested": {
			planValue: types.BoolValue(false),
			wantBody:  `{}`,
		},
		"paused but not requested": {
			pausedAt: pausedAt, planValue: types.BoolNull(),
			wantBody: `{}`,
		},
		"paused and unknown": {
			pausedAt: pausedAt, planValue: types.BoolUnknown(),
			wantBody: `{}`,
		},
		// The API rejects any update of an archived issuer.
		"paused but archived": {
			pausedAt: pausedAt, archived: true, planValue: types.BoolValue(false),
			wantBody: `{}`,
		},
		"rename only": {
			planValue: types.BoolNull(), rename: true,
			wantBody: `{"name":"renamed"}`, wantSend: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			state := federationIssuerStateModel(t, tc.pausedAt)
			if tc.archived {
				state.ArchivedAt = types.StringValue("2026-06-03T00:00:00Z")
			}
			plan := state
			plan.JWKSPollingDisabled = tc.planValue
			if tc.rename {
				plan.Name = types.StringValue("renamed")
			}

			update, diags := buildFederationIssuerUpdateParams(context.Background(), plan, state)
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if update.send != tc.wantSend {
				t.Errorf("send = %v, want %v", update.send, tc.wantSend)
			}
			if update.reenablePolling != tc.wantReenable {
				t.Errorf("reenablePolling = %v, want %v", update.reenablePolling, tc.wantReenable)
			}
			body, err := json.Marshal(update.params)
			if err != nil {
				t.Fatalf("marshal params: %v", err)
			}
			if string(body) != tc.wantBody {
				t.Errorf("body = %s, want %s", body, tc.wantBody)
			}
		})
	}
}

// runFederationIssuerModifyPlan runs ModifyPlan for an update from state to
// plan (a nil state means a create) and returns the modified plan.
func runFederationIssuerModifyPlan(t *testing.T, plan FederationIssuerResourceModel, state *FederationIssuerResourceModel) FederationIssuerResourceModel {
	t.Helper()

	r := &FederationIssuerResource{}
	s := schematest.ResourceSchema(t, r)

	planTF := tfsdk.Plan{Schema: s}
	if diags := planTF.Set(context.Background(), &plan); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	// A create has no prior state: the whole object is null.
	stateTF := tfsdk.State{Schema: s, Raw: tftypes.NewValue(schematest.ResourceObjectType(t, r), nil)}
	if state != nil {
		if diags := stateTF.Set(context.Background(), state); diags.HasError() {
			t.Fatalf("set state: %v", diags)
		}
	}

	req := resource.ModifyPlanRequest{
		Config: tfsdk.Config{Schema: s, Raw: planTF.Raw},
		Plan:   planTF,
		State:  stateTF,
	}
	resp := &resource.ModifyPlanResponse{Plan: planTF}
	r.ModifyPlan(context.Background(), req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan returned errors: %v", resp.Diagnostics)
	}

	var got FederationIssuerResourceModel
	if diags := resp.Plan.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("get plan: %v", diags)
	}
	return got
}

// A paused poller with jwks_polling_disabled = false in configuration must
// plan an update even though no configured value changed: the attributes the
// re-enable rewrites go unknown, everything else stays as planned.
func TestFederationIssuerModifyPlan_PausedPollingPlansReenable(t *testing.T) {
	t.Parallel()

	state := federationIssuerStateModel(t, "2026-06-02T00:00:00Z")
	state.JWKSPollingDisabled = types.BoolValue(false)
	plan := state

	got := runFederationIssuerModifyPlan(t, plan, &state)

	if !got.JWKSPollingDisabledAt.IsUnknown() {
		t.Errorf("jwks_polling_disabled_at = %v, want unknown", got.JWKSPollingDisabledAt)
	}
	if !got.PollStatus.IsUnknown() {
		t.Errorf("poll_status = %v, want unknown", got.PollStatus)
	}
	if !got.UpdatedAt.IsUnknown() || !got.UpdatedByActorID.IsUnknown() {
		t.Errorf("updated_at/updated_by_actor_id = %v/%v, want unknown", got.UpdatedAt, got.UpdatedByActorID)
	}
	if !got.Name.Equal(state.Name) || !got.ID.Equal(state.ID) || !got.CreatedAt.Equal(state.CreatedAt) || !got.JWKS.Equal(state.JWKS) {
		t.Error("ModifyPlan must leave the other attributes as planned")
	}
}

// Every case where nothing must be re-enabled leaves the plan untouched, so a
// steady configuration keeps planning no changes.
func TestFederationIssuerModifyPlan_NoReenable(t *testing.T) {
	t.Parallel()

	const pausedAt = "2026-06-02T00:00:00Z"

	cases := map[string]struct {
		pausedAt  string
		archived  bool
		planValue types.Bool
		create    bool
	}{
		"active polling":            {planValue: types.BoolValue(false)},
		"paused, attribute unset":   {pausedAt: pausedAt, planValue: types.BoolNull()},
		"paused, attribute unknown": {pausedAt: pausedAt, planValue: types.BoolUnknown()},
		"paused but archived":       {pausedAt: pausedAt, archived: true, planValue: types.BoolValue(false)},
		"create":                    {planValue: types.BoolValue(false), create: true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			state := federationIssuerStateModel(t, tc.pausedAt)
			if tc.archived {
				state.ArchivedAt = types.StringValue("2026-06-03T00:00:00Z")
			}
			state.JWKSPollingDisabled = tc.planValue
			if tc.planValue.IsUnknown() {
				state.JWKSPollingDisabled = types.BoolNull()
			}
			plan := state
			plan.JWKSPollingDisabled = tc.planValue

			var prior *FederationIssuerResourceModel
			if !tc.create {
				prior = &state
			}
			got := runFederationIssuerModifyPlan(t, plan, prior)

			if !got.JWKSPollingDisabledAt.Equal(plan.JWKSPollingDisabledAt) || !got.PollStatus.Equal(plan.PollStatus) ||
				!got.UpdatedAt.Equal(plan.UpdatedAt) || !got.UpdatedByActorID.Equal(plan.UpdatedByActorID) {
				t.Errorf("ModifyPlan changed the plan: got %+v, want %+v", got, plan)
			}
		})
	}
}

// federationIssuerUpdateRequest builds the Update request for a refreshed
// state and the plan ModifyPlan would produce from it.
func federationIssuerUpdateRequest(t *testing.T, r *FederationIssuerResource, plan, state FederationIssuerResourceModel) (resource.UpdateRequest, *resource.UpdateResponse) {
	t.Helper()

	s := schematest.ResourceSchema(t, r)
	planTF := tfsdk.Plan{Schema: s}
	if diags := planTF.Set(context.Background(), &plan); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	stateTF := tfsdk.State{Schema: s}
	if diags := stateTF.Set(context.Background(), &state); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return resource.UpdateRequest{Plan: planTF, State: stateTF, Config: tfsdk.Config{Schema: s, Raw: planTF.Raw}},
		&resource.UpdateResponse{State: tfsdk.State{Schema: s}}
}

// reenablePlan is the plan ModifyPlan produces for a paused issuer whose
// configuration sets jwks_polling_disabled = false.
func reenablePlan(state FederationIssuerResourceModel) FederationIssuerResourceModel {
	plan := state
	plan.JWKSPollingDisabled = types.BoolValue(false)
	plan.JWKSPollingDisabledAt = types.StringUnknown()
	plan.PollStatus = types.ObjectUnknown(federationIssuerPollStatusAttrTypes)
	plan.UpdatedAt = types.StringUnknown()
	plan.UpdatedByActorID = types.StringUnknown()
	return plan
}

func TestFederationIssuerUpdate_ReenablesPolling(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("content-type", "application/json")
		// Polling resumed: no pause timestamp, a next poll scheduled.
		_, _ = io.WriteString(w, federationIssuerFixtureWith(`{"type": "discovery"}`, `null`,
			`{"consecutive_failures": 0, "last_fetched_at": "2026-06-01T12:00:00Z", "next_poll_at": "2026-06-04T00:05:00Z"}`))
	}))
	defer srv.Close()

	r := &FederationIssuerResource{client: oauthtest.NewClient(t, srv)}
	state := federationIssuerStateModel(t, "2026-06-02T00:00:00Z")
	state.JWKSPollingDisabled = types.BoolValue(false)
	req, resp := federationIssuerUpdateRequest(t, r, reenablePlan(state), state)

	r.Update(context.Background(), req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update returned errors: %v", resp.Diagnostics)
	}

	if gotMethod != http.MethodPost || gotPath != "/v1/organizations/federation_issuers/fdis_01ABC" {
		t.Errorf("request = %s %s, want POST /v1/organizations/federation_issuers/fdis_01ABC", gotMethod, gotPath)
	}
	if v, ok := gotBody["jwks_polling_disabled"]; !ok || v != false {
		t.Errorf("body = %v, want jwks_polling_disabled: false", gotBody)
	}
	if len(gotBody) != 1 {
		t.Errorf("body = %v, want only jwks_polling_disabled", gotBody)
	}

	var got FederationIssuerResourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if !got.JWKSPollingDisabledAt.IsNull() {
		t.Errorf("jwks_polling_disabled_at = %v, want null after the re-enable", got.JWKSPollingDisabledAt)
	}
	if !got.JWKSPollingDisabled.Equal(types.BoolValue(false)) {
		t.Errorf("jwks_polling_disabled = %v, want false kept from the plan", got.JWKSPollingDisabled)
	}
	if got.PollStatus.IsNull() || got.PollStatus.IsUnknown() {
		t.Errorf("poll_status = %v, want the object the API returned", got.PollStatus)
	}
}

// The API refuses jwks_polling_disabled from an OAuth caller when the issuer
// backs a rule scoped beyond workspace:developer / workspace:inference. The
// diagnostic must explain that restriction and keep the API's own message.
func TestFederationIssuerUpdate_ReenableRefusedExplainsOAuthRestriction(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("content-type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"type":"error","error":{"type":"permission_error","message":"requires a Console session"}}`)
			}))
			defer srv.Close()

			r := &FederationIssuerResource{client: oauthtest.NewClient(t, srv)}
			state := federationIssuerStateModel(t, "2026-06-02T00:00:00Z")
			state.JWKSPollingDisabled = types.BoolValue(false)
			req, resp := federationIssuerUpdateRequest(t, r, reenablePlan(state), state)

			r.Update(context.Background(), req, resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected an error diagnostic")
			}
			d := resp.Diagnostics.Errors()[0]
			if d.Summary() != "Unable to Update Federation Issuer" {
				t.Errorf("summary = %q", d.Summary())
			}
			for _, want := range []string{"fdis_01ABC", "jwks_polling_disabled", "workspace:developer", "workspace:inference", "Console session", "requires a Console session"} {
				if !strings.Contains(d.Detail(), want) {
					t.Errorf("detail does not mention %q:\n%s", want, d.Detail())
				}
			}
		})
	}
}

// Without the polling toggle in the body, an update error stays generic: the
// OAuth restriction would only mislead.
func TestFederationIssuerUpdateErrorDiagnostic_WithoutReenable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Archived issuers cannot be updated"}}`)
	}))
	defer srv.Close()

	client := oauthtest.NewClient(t, srv)
	_, err := client.Beta.Organization.Federation.Issuers.Update(context.Background(), "fdis_01ABC", anthropic.BetaOrganizationFederationIssuerUpdateParams{})
	if err == nil {
		t.Fatal("expected an error")
	}

	summary, detail := federationIssuerUpdateErrorDiagnostic("fdis_01ABC", err, false)
	if summary != "Client Error" {
		t.Errorf("summary = %q, want Client Error", summary)
	}
	if strings.Contains(detail, "workspace:developer") {
		t.Errorf("detail must not mention the polling restriction:\n%s", detail)
	}
	if !strings.Contains(detail, "Archived issuers cannot be updated") {
		t.Errorf("detail must keep the API error:\n%s", detail)
	}
}

// Setting jwks_polling_disabled = false while polling is active changes only
// configuration: Update must not send an (unverified) empty body, and reads
// the issuer back instead to resolve the Computed attributes.
func TestFederationIssuerUpdate_NothingToSendReadsBack(t *testing.T) {
	t.Parallel()

	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, federationIssuerFixture(`{"type": "discovery"}`))
	}))
	defer srv.Close()

	r := &FederationIssuerResource{client: oauthtest.NewClient(t, srv)}
	state := federationIssuerStateModel(t, "")
	plan := state
	plan.JWKSPollingDisabled = types.BoolValue(false)
	// The framework marks Computed attributes without a plan modifier unknown
	// once any configured value changes.
	plan.JWKSPollingDisabledAt = types.StringUnknown()
	plan.PollStatus = types.ObjectUnknown(federationIssuerPollStatusAttrTypes)
	plan.UpdatedAt = types.StringUnknown()
	plan.UpdatedByActorID = types.StringUnknown()
	req, resp := federationIssuerUpdateRequest(t, r, plan, state)

	r.Update(context.Background(), req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update returned errors: %v", resp.Diagnostics)
	}

	if len(methods) != 1 || methods[0] != "GET /v1/organizations/federation_issuers/fdis_01ABC" {
		t.Errorf("requests = %v, want a single GET of the issuer", methods)
	}
	var got FederationIssuerResourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if got.UpdatedAt.IsUnknown() || got.PollStatus.IsUnknown() {
		t.Error("Computed attributes must be resolved from the read")
	}
	if !got.JWKSPollingDisabled.Equal(types.BoolValue(false)) {
		t.Errorf("jwks_polling_disabled = %v, want false", got.JWKSPollingDisabled)
	}
}
