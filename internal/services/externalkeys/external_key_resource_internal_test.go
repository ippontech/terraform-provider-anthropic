// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Live create/update/delete acceptance tests for anthropic_external_key are
// not written: Admin API resource tests wait for a dedicated test organization
// (#58, see CLAUDE.md), and registering a key needs CMEK turned on for the
// organization plus a real KMS key. Coverage here is httptest-based.
package externalkeys

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

const (
	testKeyID  = "ekey_01SDCCSbTxrXDpWc1phhtcfK"
	testKMSARN = "arn:aws:kms:us-east-1:111122223333:key/abcd1234-5678-90ab-cdef-000011112222"
)

// awsKeyFixture mirrors the reference example, deprecated role_arn included.
func awsKeyFixture(attachment string) string {
	return `{
		"type": "external_key",
		"id": "` + testKeyID + `",
		"attachment": {"type": "` + attachment + `"},
		"created_at": "2024-10-30T23:58:27.427722Z",
		"display_name": "prod-us-key",
		"geo": "us",
		"provider_config": {
			"type": "aws",
			"kms_arn": "` + testKMSARN + `",
			"region": "us-east-1",
			"role_arn": "arn:aws:iam::111122223333:role/anthropic-cmek"
		},
		"updated_at": "2024-10-30T23:58:27.427722Z"
	}`
}

const azureKeyFixture = `{
	"type": "external_key",
	"id": "ekey_01AZURE",
	"attachment": {"type": "unattached"},
	"created_at": "2026-10-01T00:00:00Z",
	"display_name": null,
	"geo": "us",
	"provider_config": {
		"type": "azure",
		"key_name": "anthropic-cmek",
		"tenant_id": "00000000-0000-0000-0000-000000000000",
		"vault_uri": "https://example.vault.azure.net",
		"client_id": null
	},
	"updated_at": "2026-10-01T00:00:00Z"
}`

// --- tftypes builders ---

// objectValue builds a tftypes object of typ from vals: a missing key or nil
// is null, tftypes.UnknownValue is unknown, a tftypes.Value is used as is and
// anything else is wrapped with the attribute's type.
func objectValue(typ tftypes.Object, vals map[string]any) tftypes.Value {
	m := make(map[string]tftypes.Value, len(typ.AttributeTypes))
	for name, at := range typ.AttributeTypes {
		v := vals[name]
		if tv, ok := v.(tftypes.Value); ok {
			m[name] = tv
			continue
		}
		m[name] = tftypes.NewValue(at, v) // nil is null, tftypes.UnknownValue unknown
	}
	return tftypes.NewValue(typ, m)
}

// keyValue builds a resource value from top-level and provider_config values.
func keyValue(t *testing.T, top, pc map[string]any) tftypes.Value {
	t.Helper()
	objType := schematest.ResourceObjectType(t, &ExternalKeyResource{})
	all := map[string]any{}
	for k, v := range top {
		all[k] = v
	}
	all["provider_config"] = objectValue(objType.AttributeTypes["provider_config"].(tftypes.Object), pc)
	return objectValue(objType, all)
}

func awsStateTop(attachment string) map[string]any {
	return map[string]any{
		"id": testKeyID, "display_name": "prod-us-key", "geo": "us", "attachment": attachment,
		"created_at": "2024-10-30T23:58:27.427722Z", "updated_at": "2024-10-30T23:58:27.427722Z",
	}
}

func awsStatePC() map[string]any {
	return map[string]any{"type": "aws", "kms_arn": testKMSARN, "region": "us-east-1"}
}

func newKeyServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body map[string]any)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		w.Header().Set("content-type", "application/json")
		handler(w, r, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- mapping ---

func TestMapExternalKeyToModel_aws(t *testing.T) {
	var key externalKeyAPIResponse
	if err := json.Unmarshal([]byte(awsKeyFixture("attached")), &key); err != nil {
		t.Fatalf("parse: %v", err)
	}
	data, diags := mapExternalKeyToModel(context.Background(), &key)
	if diags.HasError() {
		t.Fatalf("map: %v", diags)
	}
	// The mapped model must fit the resource schema.
	state := schematest.NullState(t, &ExternalKeyResource{})
	if diags := state.Set(context.Background(), &data); diags.HasError() {
		t.Fatalf("state.Set against resource schema: %v", diags)
	}

	if data.ID.ValueString() != testKeyID || data.DisplayName.ValueString() != "prod-us-key" ||
		data.Geo.ValueString() != "us" || data.Attachment.ValueString() != "attached" {
		t.Errorf("top-level attributes = %+v", data)
	}
	pc := data.ProviderConfig.Attributes()
	if !pc["type"].Equal(types.StringValue("aws")) || !pc["kms_arn"].Equal(types.StringValue(testKMSARN)) ||
		!pc["region"].Equal(types.StringValue("us-east-1")) {
		t.Errorf("provider_config = %v", pc)
	}
	for _, name := range []string{"key_name", "vault_uri", "tenant_id", "client_id"} {
		if !pc[name].IsNull() {
			t.Errorf("provider_config.%s = %v, want null", name, pc[name])
		}
	}
	if _, ok := pc["role_arn"]; ok {
		t.Error("the deprecated role_arn must not be mapped")
	}
}

func TestMapExternalKeyToModel_azureNulls(t *testing.T) {
	var key externalKeyAPIResponse
	if err := json.Unmarshal([]byte(azureKeyFixture), &key); err != nil {
		t.Fatalf("parse: %v", err)
	}
	data, diags := mapExternalKeyToModel(context.Background(), &key)
	if diags.HasError() {
		t.Fatalf("map: %v", diags)
	}
	if !data.DisplayName.IsNull() {
		t.Errorf("display_name = %v, want null", data.DisplayName)
	}
	pc := data.ProviderConfig.Attributes()
	if !pc["vault_uri"].Equal(types.StringValue("https://example.vault.azure.net")) || !pc["client_id"].IsNull() || !pc["region"].IsNull() {
		t.Errorf("provider_config = %v", pc)
	}
}

func TestExternalKeyPath_escapesARN(t *testing.T) {
	if got := externalKeyPath(testKeyID); got != "/v1/organizations/external_keys/"+testKeyID {
		t.Errorf("ekey path = %q", got)
	}
	want := "/v1/organizations/external_keys/arn:aws:kms:us-east-1:111122223333:key%2Fabcd1234-5678-90ab-cdef-000011112222"
	if got := externalKeyPath(testKMSARN); got != want {
		t.Errorf("ARN path = %q, want %q", got, want)
	}
}

// --- Create ---

func TestExternalKeyResource_Create(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	var gotBody map[string]any
	srv := newKeyServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		gotMethod, gotPath, gotQuery, gotBody = r.Method, r.URL.Path, r.URL.RawQuery, body
		_, _ = io.WriteString(w, awsKeyFixture("unattached"))
	})

	r := &ExternalKeyResource{client: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)
	plan := tfsdk.Plan{Schema: s, Raw: keyValue(t,
		map[string]any{
			"id": tftypes.UnknownValue, "display_name": "prod-us-key", "geo": tftypes.UnknownValue,
			"attachment": tftypes.UnknownValue, "created_at": tftypes.UnknownValue, "updated_at": tftypes.UnknownValue,
		},
		map[string]any{"type": "aws", "kms_arn": testKMSARN, "region": tftypes.UnknownValue},
	)}

	resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	if gotMethod != http.MethodPost || gotPath != "/v1/organizations/external_keys" || gotQuery != "" {
		t.Errorf("request = %s %s?%s", gotMethod, gotPath, gotQuery)
	}
	want := map[string]any{
		"display_name":    "prod-us-key",
		"provider_config": map[string]any{"type": "aws", "kms_arn": testKMSARN},
	}
	if b, _ := json.Marshal(gotBody); string(b) != mustJSON(t, want) {
		t.Errorf("body = %s, want %s (unknown geo and region are omitted)", b, mustJSON(t, want))
	}

	var data ExternalKeyModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if data.ID.ValueString() != testKeyID || data.Geo.ValueString() != "us" || data.Attachment.ValueString() != "unattached" {
		t.Errorf("state = %+v", data)
	}
	if got := data.ProviderConfig.Attributes()["region"]; !got.Equal(types.StringValue("us-east-1")) {
		t.Errorf("derived region = %v", got)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// --- Read ---

func TestExternalKeyResource_Read_escapesARNAndMaps(t *testing.T) {
	var gotRawPath string
	srv := newKeyServer(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		gotRawPath = r.URL.EscapedPath()
		_, _ = io.WriteString(w, strings.Replace(awsKeyFixture("attached"), testKeyID, testKMSARN, 1))
	})

	r := &ExternalKeyResource{client: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)
	state := tfsdk.State{Schema: s, Raw: keyValue(t, map[string]any{"id": testKMSARN}, nil)}

	resp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	if want := "/v1/organizations/external_keys/arn:aws:kms:us-east-1:111122223333:key%2Fabcd1234-5678-90ab-cdef-000011112222"; gotRawPath != want {
		t.Errorf("path = %q, want %q", gotRawPath, want)
	}

	var data ExternalKeyModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if data.ID.ValueString() != testKMSARN || data.Attachment.ValueString() != "attached" {
		t.Errorf("state = %+v", data)
	}
}

func TestExternalKeyResource_Read_notFoundRemovesFromState(t *testing.T) {
	srv := newKeyServer(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"External key not found"}}`)
	})

	r := &ExternalKeyResource{client: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)
	state := tfsdk.State{Schema: s, Raw: keyValue(t, awsStateTop("unattached"), awsStatePC())}

	resp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("expected the resource to be removed from state after a 404")
	}
}

// --- Update ---

func TestExternalKeyResource_Update_sendsOnlyChangedFields(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := newKeyServer(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		gotPath, gotBody = r.URL.Path, body
		_, _ = io.WriteString(w, strings.Replace(awsKeyFixture("attached"), `"prod-us-key"`, `"renamed"`, 1))
	})

	r := &ExternalKeyResource{client: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)
	state := tfsdk.State{Schema: s, Raw: keyValue(t, awsStateTop("attached"), awsStatePC())}
	planTop := awsStateTop("attached")
	planTop["display_name"] = "renamed"
	planTop["attachment"] = tftypes.UnknownValue
	planTop["updated_at"] = tftypes.UnknownValue
	plan := tfsdk.Plan{Schema: s, Raw: keyValue(t, planTop, awsStatePC())}

	resp := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}
	if gotPath != "/v1/organizations/external_keys/"+testKeyID {
		t.Errorf("path = %q", gotPath)
	}
	if want := `{"display_name":"renamed"}`; mustJSON(t, gotBody) != want {
		t.Errorf("body = %s, want %s: an attached key must not receive geo or provider_config", mustJSON(t, gotBody), want)
	}
	var data ExternalKeyModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if data.DisplayName.ValueString() != "renamed" {
		t.Errorf("display_name = %v", data.DisplayName)
	}
}

func TestBuildExternalKeyUpdateBody(t *testing.T) {
	ctx := context.Background()
	str, null, unknown := types.StringValue, types.StringNull(), types.StringUnknown()

	pcObj := func(t *testing.T, m externalKeyProviderConfigModel) types.Object {
		t.Helper()
		obj, diags := types.ObjectValueFrom(ctx, providerConfigAttrTypes, m)
		if diags.HasError() {
			t.Fatal(diags)
		}
		return obj
	}
	awsPC := externalKeyProviderConfigModel{Type: str("aws"), KMSARN: str(testKMSARN), Region: str("us-east-1"),
		KeyName: null, VaultURI: null, TenantID: null, ClientID: null}
	azurePC := externalKeyProviderConfigModel{Type: str("azure"), KMSARN: null, Region: null, KeyName: str("k"),
		VaultURI: str("https://v.vault.azure.net"), TenantID: str("tenant"), ClientID: str("app")}

	base := func(t *testing.T, display types.String, pc externalKeyProviderConfigModel) ExternalKeyModel {
		return ExternalKeyModel{ID: str(testKeyID), DisplayName: display, Geo: str("us"), ProviderConfig: pcObj(t, pc)}
	}

	newARN := awsPC
	newARN.KMSARN = str("arn:aws:kms:us-west-2:111122223333:key/other")
	newARN.Region = unknown
	noClient := azurePC
	noClient.ClientID = null
	gcpPC := externalKeyProviderConfigModel{Type: str("gcp"), KMSARN: null, Region: null,
		KeyName: str("projects/p/locations/l/keyRings/r/cryptoKeys/k"), VaultURI: null, TenantID: null, ClientID: null}

	cases := []struct {
		name        string
		plan, state func(t *testing.T) ExternalKeyModel
		want        string
	}{
		{
			"no change sends an empty body",
			func(t *testing.T) ExternalKeyModel { return base(t, str("a"), awsPC) },
			func(t *testing.T) ExternalKeyModel { return base(t, str("a"), awsPC) },
			`{}`,
		},
		{
			"display_name removed is cleared with null",
			func(t *testing.T) ExternalKeyModel { return base(t, null, awsPC) },
			func(t *testing.T) ExternalKeyModel { return base(t, str("a"), awsPC) },
			`{"display_name":null}`,
		},
		{
			"new kms_arn resends provider_config without the unknown region",
			func(t *testing.T) ExternalKeyModel { return base(t, str("a"), newARN) },
			func(t *testing.T) ExternalKeyModel { return base(t, str("a"), awsPC) },
			`{"provider_config":{"kms_arn":"arn:aws:kms:us-west-2:111122223333:key/other","type":"aws"}}`,
		},
		{
			"client_id removed is cleared with null",
			func(t *testing.T) ExternalKeyModel { return base(t, str("a"), noClient) },
			func(t *testing.T) ExternalKeyModel { return base(t, str("a"), azurePC) },
			`{"provider_config":{"client_id":null,"key_name":"k","tenant_id":"tenant","type":"azure","vault_uri":"https://v.vault.azure.net"}}`,
		},
		{
			"provider switch on an unattached key",
			func(t *testing.T) ExternalKeyModel { return base(t, str("a"), noClient) },
			func(t *testing.T) ExternalKeyModel { return base(t, str("a"), awsPC) },
			`{"provider_config":{"key_name":"k","tenant_id":"tenant","type":"azure","vault_uri":"https://v.vault.azure.net"}}`,
		},
		{
			// client_id belongs to the azure member only: no null for it in a gcp body.
			"switch from azure with client_id to gcp",
			func(t *testing.T) ExternalKeyModel { return base(t, str("a"), gcpPC) },
			func(t *testing.T) ExternalKeyModel { return base(t, str("a"), azurePC) },
			`{"provider_config":{"key_name":"projects/p/locations/l/keyRings/r/cryptoKeys/k","type":"gcp"}}`,
		},
		{
			"switch from azure with client_id to aws",
			func(t *testing.T) ExternalKeyModel { return base(t, str("a"), newARN) },
			func(t *testing.T) ExternalKeyModel { return base(t, str("a"), azurePC) },
			`{"provider_config":{"kms_arn":"arn:aws:kms:us-west-2:111122223333:key/other","type":"aws"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, diags := buildExternalKeyUpdateBody(ctx, tc.plan(t), tc.state(t))
			if diags.HasError() {
				t.Fatal(diags)
			}
			if got := mustJSON(t, body); got != tc.want {
				t.Errorf("body = %s, want %s", got, tc.want)
			}
		})
	}
}

// --- Delete ---

func TestExternalKeyResource_Delete(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		attachment  string
		wantSummary string
	}{
		{"deleted", http.StatusOK, "", ""},
		{"already gone", http.StatusNotFound, "", ""},
		{"refused while attached", http.StatusBadRequest, "attached", "External key is attached to a workspace"},
		{"other failure", http.StatusBadRequest, "unattached", "Client Error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var calls []string
			srv := newKeyServer(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
				mu.Lock()
				calls = append(calls, r.Method+" "+r.URL.Path)
				mu.Unlock()
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, awsKeyFixture(tc.attachment))
					return
				}
				w.WriteHeader(tc.status)
				if tc.status == http.StatusOK {
					_, _ = io.WriteString(w, `{"id":"`+testKeyID+`","type":"external_key_deleted"}`)
					return
				}
				_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"refused"}}`)
			})

			r := &ExternalKeyResource{client: admintest.NewClient(t, srv)}
			s := schematest.ResourceSchema(t, r)
			state := tfsdk.State{Schema: s, Raw: keyValue(t, awsStateTop("unattached"), awsStatePC())}

			var resp resource.DeleteResponse
			r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)

			if calls[0] != "DELETE /v1/organizations/external_keys/"+testKeyID {
				t.Errorf("first call = %q", calls[0])
			}
			if tc.wantSummary == "" {
				if resp.Diagnostics.HasError() {
					t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
				}
				return
			}
			if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != tc.wantSummary {
				t.Fatalf("diagnostics = %v, want summary %q", resp.Diagnostics, tc.wantSummary)
			}
			if tc.attachment == "attached" && !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "removed") {
				t.Errorf("the attached diagnostic should explain how to stop managing the key: %s", resp.Diagnostics.Errors()[0].Detail())
			}
		})
	}
}

// --- ModifyPlan ---

func TestExternalKeyResource_ModifyPlan(t *testing.T) {
	r := &ExternalKeyResource{}
	s := schematest.ResourceSchema(t, r)

	planWith := func(top map[string]any, pc map[string]any) tfsdk.Plan {
		return tfsdk.Plan{Schema: s, Raw: keyValue(t, top, pc)}
	}
	newARNPC := map[string]any{"type": "aws", "kms_arn": "arn:aws:kms:us-west-2:111122223333:key/other", "region": tftypes.UnknownValue}
	unknownARNPC := map[string]any{"type": "aws", "kms_arn": tftypes.UnknownValue, "region": tftypes.UnknownValue}
	gcpPC := map[string]any{"type": "gcp", "key_name": "projects/p/locations/l/keyRings/r/cryptoKeys/k"}
	renamed := awsStateTop("attached")
	renamed["display_name"] = "renamed"
	euGeo := awsStateTop("attached")
	euGeo["geo"] = "eu"
	unknownGeo := awsStateTop("attached")
	unknownGeo["geo"] = tftypes.UnknownValue
	objType := schematest.ResourceObjectType(t, r)
	unknownPCTop := awsStateTop("attached")
	unknownPCTop["provider_config"] = tftypes.NewValue(objType.AttributeTypes["provider_config"], tftypes.UnknownValue)
	unknownPCValue := objectValue(objType, unknownPCTop)

	cases := []struct {
		name         string
		attachment   string
		plan         *tfsdk.Plan // nil means destroy
		wantError    string
		wantWarning  bool
		wantNoChange bool
	}{
		{name: "attached, kms_arn change", attachment: "attached", plan: ptr(planWith(awsStateTop("attached"), newARNPC)), wantError: "provider_config.kms_arn"},
		{name: "attached, provider switch", attachment: "attached", plan: ptr(planWith(awsStateTop("attached"), gcpPC)), wantError: "provider_config.type"},
		{name: "attached, geo change", attachment: "attached", plan: ptr(planWith(euGeo, awsStatePC())), wantError: "geo"},
		{name: "attached, display_name only", attachment: "attached", plan: ptr(planWith(renamed, awsStatePC()))},
		// The value is known only at apply: a warning says the update fails
		// then if it differs.
		{name: "attached, kms_arn unknown at plan", attachment: "attached", plan: ptr(planWith(awsStateTop("attached"), unknownARNPC)), wantWarning: true},
		{name: "attached, geo unknown at plan", attachment: "attached", plan: ptr(planWith(unknownGeo, awsStatePC())), wantWarning: true},
		{name: "attached, provider_config unknown at plan", attachment: "attached", plan: ptr(tfsdk.Plan{Schema: s, Raw: unknownPCValue}), wantWarning: true},
		{name: "unattached, kms_arn unknown at plan", attachment: "unattached", plan: ptr(planWith(awsStateTop("unattached"), unknownARNPC))},
		{name: "unattached, kms_arn change", attachment: "unattached", plan: ptr(planWith(awsStateTop("unattached"), newARNPC))},
		{name: "destroy attached", attachment: "attached", wantWarning: true},
		{name: "destroy unattached", attachment: "unattached"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := tfsdk.State{Schema: s, Raw: keyValue(t, awsStateTop(tc.attachment), awsStatePC())}
			plan := tfsdk.Plan{Schema: s, Raw: tftypes.NewValue(state.Raw.Type(), nil)}
			if tc.plan != nil {
				plan = *tc.plan
			}
			resp := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{State: state, Plan: plan}, &resp)

			switch {
			case tc.wantError != "":
				if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), tc.wantError) {
					t.Fatalf("diagnostics = %v, want an error naming %s", resp.Diagnostics, tc.wantError)
				}
			case tc.wantWarning:
				if resp.Diagnostics.HasError() || resp.Diagnostics.WarningsCount() != 1 {
					t.Fatalf("diagnostics = %v, want exactly one warning", resp.Diagnostics)
				}
			default:
				if len(resp.Diagnostics) != 0 {
					t.Fatalf("diagnostics = %v, want none", resp.Diagnostics)
				}
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

// --- vault_uri spelling ---

// The Azure portal shows the Key Vault URI with a trailing slash. If the API
// returns it without one (or in another letter case), the configured spelling
// stays in state on create and on read, so the apply is consistent and the
// next plan shows no diff. Another URI is taken from the API as it is.
func TestExternalKeyResource_keepsVaultURISpelling(t *testing.T) {
	const apiURI = "https://example.vault.azure.net"
	azurePC := func(uri string) map[string]any {
		return map[string]any{"type": "azure", "key_name": "anthropic-cmek", "tenant_id": "00000000-0000-0000-0000-000000000000", "vault_uri": uri}
	}
	azureTop := map[string]any{
		"id": "ekey_01AZURE", "geo": "us", "attachment": "unattached",
		"created_at": "2026-10-01T00:00:00Z", "updated_at": "2026-10-01T00:00:00Z",
	}
	srv := newKeyServer(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		_, _ = io.WriteString(w, azureKeyFixture)
	})
	r := &ExternalKeyResource{client: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)

	vaultURI := func(t *testing.T, st tfsdk.State) string {
		t.Helper()
		var data ExternalKeyModel
		if diags := st.Get(context.Background(), &data); diags.HasError() {
			t.Fatalf("state: %v", diags)
		}
		return data.ProviderConfig.Attributes()["vault_uri"].(types.String).ValueString()
	}

	t.Run("create", func(t *testing.T) {
		planTop := map[string]any{
			"id": tftypes.UnknownValue, "geo": tftypes.UnknownValue, "attachment": tftypes.UnknownValue,
			"created_at": tftypes.UnknownValue, "updated_at": tftypes.UnknownValue,
		}
		plan := tfsdk.Plan{Schema: s, Raw: keyValue(t, planTop, azurePC(apiURI+"/"))}
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("Create: %v", resp.Diagnostics)
		}
		if got := vaultURI(t, resp.State); got != apiURI+"/" {
			t.Errorf("vault_uri = %q, want the planned %q", got, apiURI+"/")
		}
	})

	for name, tc := range map[string]struct{ stored, want string }{
		"read keeps the stored spelling": {stored: "https://EXAMPLE.vault.azure.net/", want: "https://EXAMPLE.vault.azure.net/"},
		"read takes another URI":         {stored: "https://other.vault.azure.net/", want: apiURI},
	} {
		t.Run(name, func(t *testing.T) {
			state := tfsdk.State{Schema: s, Raw: keyValue(t, azureTop, azurePC(tc.stored))}
			resp := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Read: %v", resp.Diagnostics)
			}
			if got := vaultURI(t, resp.State); got != tc.want {
				t.Errorf("vault_uri = %q, want %q", got, tc.want)
			}
		})
	}
}

// An empty string would be sent as is and read back as null (or a derived
// region): every optional provider_config string rejects it at plan time.
func TestProviderConfigAttributes_rejectEmptyString(t *testing.T) {
	r := &ExternalKeyResource{}
	s := schematest.ResourceSchema(t, r)
	pcAttr, ok := s.Attributes["provider_config"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("provider_config is %T", s.Attributes["provider_config"])
	}
	for _, name := range []string{"kms_arn", "region", "key_name", "vault_uri", "tenant_id", "client_id"} {
		t.Run(name, func(t *testing.T) {
			attrSchema, ok := pcAttr.Attributes[name].(schema.StringAttribute)
			if !ok {
				t.Fatalf("%s is %T", name, pcAttr.Attributes[name])
			}
			resp := &validator.StringResponse{}
			for _, v := range attrSchema.Validators {
				v.ValidateString(context.Background(), validator.StringRequest{
					Path: path.Root("provider_config").AtName(name), ConfigValue: types.StringValue(""),
				}, resp)
			}
			if !resp.Diagnostics.HasError() {
				t.Errorf(`%s = "" passes validation`, name)
			}
		})
	}
}

// --- provider_config validator ---

func TestProviderConfigValidator(t *testing.T) {
	str, null, unknown := types.StringValue, types.StringNull(), types.StringUnknown()
	pc := func(typ types.String, fields map[string]types.String) types.Object {
		vals := map[string]attr.Value{"type": typ}
		for name := range providerConfigAttrTypes {
			if name == "type" {
				continue
			}
			vals[name] = null
			if v, ok := fields[name]; ok {
				vals[name] = v
			}
		}
		return types.ObjectValueMust(providerConfigAttrTypes, vals)
	}

	cases := []struct {
		name    string
		value   types.Object
		wantErr []string // attribute names with an error
	}{
		{"aws ok", pc(str("aws"), map[string]types.String{"kms_arn": str(testKMSARN)}), nil},
		{"aws with region ok", pc(str("aws"), map[string]types.String{"kms_arn": str(testKMSARN), "region": str("us-east-1")}), nil},
		{"aws without kms_arn", pc(str("aws"), nil), []string{"kms_arn"}},
		{"aws with unknown kms_arn", pc(str("aws"), map[string]types.String{"kms_arn": unknown}), nil},
		{"aws with azure fields", pc(str("aws"), map[string]types.String{"kms_arn": str(testKMSARN), "vault_uri": str("u"), "client_id": str("c")}), []string{"vault_uri", "client_id"}},
		{"gcp ok", pc(str("gcp"), map[string]types.String{"key_name": str("projects/p")}), nil},
		{"gcp with kms_arn and region", pc(str("gcp"), map[string]types.String{"key_name": str("projects/p"), "kms_arn": str(testKMSARN), "region": str("r")}), []string{"kms_arn", "region"}},
		{"azure missing everything", pc(str("azure"), nil), []string{"key_name", "vault_uri", "tenant_id"}},
		{"azure ok with client_id", pc(str("azure"), map[string]types.String{"key_name": str("k"), "vault_uri": str("u"), "tenant_id": str("t"), "client_id": str("c")}), nil},
		{"unknown type skips", pc(unknown, map[string]types.String{"vault_uri": str("u")}), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &validator.ObjectResponse{}
			providerConfigValidator{}.ValidateObject(context.Background(), validator.ObjectRequest{
				Path: path.Root("provider_config"), ConfigValue: tc.value,
			}, resp)
			var got []string
			for _, d := range resp.Diagnostics.Errors() {
				if wp, ok := d.(interface{ Path() path.Path }); ok {
					got = append(got, strings.TrimPrefix(wp.Path().String(), "provider_config."))
				}
			}
			if strings.Join(got, ",") != strings.Join(tc.wantErr, ",") {
				t.Errorf("errors on %v, want %v (%v)", got, tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

// --- region plan modifier ---

func TestRegionFollowsKMSARN(t *testing.T) {
	r := &ExternalKeyResource{}
	s := schematest.ResourceSchema(t, r)
	regionPath := path.Root("provider_config").AtName("region")
	otherARN := "arn:aws:kms:us-west-2:111122223333:key/other"

	cases := []struct {
		name   string
		config types.String
		planPC map[string]any
		state  bool // whether a prior state (aws, testKMSARN, us-east-1) exists
		want   types.String
	}{
		{"configured value wins", types.StringValue("eu-west-1"), map[string]any{"type": "aws", "kms_arn": testKMSARN, "region": "eu-west-1"}, true, types.StringValue("eu-west-1")},
		{"create on aws stays unknown", types.StringNull(), map[string]any{"type": "aws", "kms_arn": testKMSARN, "region": tftypes.UnknownValue}, false, types.StringUnknown()},
		{"gcp is null", types.StringNull(), map[string]any{"type": "gcp", "key_name": "projects/p", "region": tftypes.UnknownValue}, false, types.StringNull()},
		{"unchanged kms_arn keeps state", types.StringNull(), map[string]any{"type": "aws", "kms_arn": testKMSARN, "region": tftypes.UnknownValue}, true, types.StringValue("us-east-1")},
		{"new kms_arn stays unknown", types.StringNull(), map[string]any{"type": "aws", "kms_arn": otherARN, "region": tftypes.UnknownValue}, true, types.StringUnknown()},
		{"unknown type stays unknown", types.StringNull(), map[string]any{"type": tftypes.UnknownValue, "kms_arn": testKMSARN, "region": tftypes.UnknownValue}, true, types.StringUnknown()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := tfsdk.Plan{Schema: s, Raw: keyValue(t, map[string]any{"display_name": "a"}, tc.planPC)}
			state := tfsdk.State{Schema: s, Raw: tftypes.NewValue(plan.Raw.Type(), nil)}
			stateValue := types.StringNull()
			if tc.state {
				state = tfsdk.State{Schema: s, Raw: keyValue(t, awsStateTop("unattached"), awsStatePC())}
				stateValue = types.StringValue("us-east-1")
			}
			var planValue types.String
			if diags := plan.GetAttribute(context.Background(), regionPath, &planValue); diags.HasError() {
				t.Fatal(diags)
			}

			resp := &planmodifier.StringResponse{PlanValue: planValue}
			regionFollowsKMSARN{}.PlanModifyString(context.Background(), planmodifier.StringRequest{
				Path: regionPath, Plan: plan, State: state,
				ConfigValue: tc.config, PlanValue: planValue, StateValue: stateValue,
			}, resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if !resp.PlanValue.Equal(tc.want) {
				t.Errorf("region = %v, want %v", resp.PlanValue, tc.want)
			}
		})
	}
}

// --- ImportState ---

func TestExternalKeyResource_ImportState(t *testing.T) {
	for _, id := range []string{testKeyID, testKMSARN} {
		r := &ExternalKeyResource{}
		resp := resource.ImportStateResponse{State: schematest.NullState(t, r)}
		r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("ImportState(%q): %v", id, resp.Diagnostics)
		}
		var got string
		if diags := resp.State.GetAttribute(context.Background(), path.Root("id"), &got); diags.HasError() || got != id {
			t.Errorf("imported id = %q, want %q (%v)", got, id, diags)
		}
	}
}

// --- id on Claude Platform on AWS ---

// On Claude Platform on AWS the ID is the KMS key ARN. When kms_arn changes,
// whether the API keeps the old ARN as the ID is not documented, so the ID is
// planned unknown rather than promised to dependents.
func TestIDFollowsKMSARN(t *testing.T) {
	r := &ExternalKeyResource{}
	s := schematest.ResourceSchema(t, r)
	const otherARN = "arn:aws:kms:us-west-2:111122223333:key/other"

	arnTop := func(id string) map[string]any {
		top := awsStateTop("unattached")
		top["id"] = id
		return top
	}
	pcWith := func(arn any) map[string]any {
		return map[string]any{"type": "aws", "kms_arn": arn, "region": tftypes.UnknownValue}
	}

	cases := []struct {
		name        string
		stateID     string
		planARN     any
		wantUnknown bool
	}{
		{"ARN id, kms_arn changes", testKMSARN, otherARN, true},
		{"ARN id, kms_arn unknown", testKMSARN, tftypes.UnknownValue, true},
		{"ARN id, kms_arn unchanged", testKMSARN, testKMSARN, false},
		{"ekey id, kms_arn changes", testKeyID, otherARN, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := tfsdk.State{Schema: s, Raw: keyValue(t, arnTop(tc.stateID), map[string]any{"type": "aws", "kms_arn": testKMSARN, "region": "us-east-1"})}
			plan := tfsdk.Plan{Schema: s, Raw: keyValue(t, arnTop(tc.stateID), pcWith(tc.planARN))}
			req := planmodifier.StringRequest{
				Path: path.Root("id"), State: state, Plan: plan,
				StateValue: types.StringValue(tc.stateID), PlanValue: types.StringValue(tc.stateID), ConfigValue: types.StringNull(),
			}
			resp := planmodifier.StringResponse{PlanValue: req.PlanValue}
			idFollowsKMSARN{}.PlanModifyString(context.Background(), req, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if resp.PlanValue.IsUnknown() != tc.wantUnknown {
				t.Errorf("id planned %v, want unknown %v", resp.PlanValue, tc.wantUnknown)
			}
		})
	}

	t.Run("create", func(t *testing.T) {
		req := planmodifier.StringRequest{
			State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(schematest.ResourceObjectType(t, r), nil)},
			Plan:  tfsdk.Plan{Schema: s, Raw: keyValue(t, arnTop(testKMSARN), pcWith(otherARN))},
			Path:  path.Root("id"), StateValue: types.StringNull(), PlanValue: types.StringUnknown(), ConfigValue: types.StringNull(),
		}
		resp := planmodifier.StringResponse{PlanValue: req.PlanValue}
		idFollowsKMSARN{}.PlanModifyString(context.Background(), req, &resp)
		if !resp.PlanValue.IsUnknown() || resp.Diagnostics.HasError() {
			t.Errorf("create: planned %v (%v), want it left unknown", resp.PlanValue, resp.Diagnostics)
		}
	})
}

// --- display_name cleared ---

// A display_name removed from the configuration is sent as null. If the API
// keeps the name, Update says so instead of leaving Terraform to report an
// inconsistent result for the planned null.
func TestExternalKeyResource_Update_displayNameNotCleared(t *testing.T) {
	for name, tc := range map[string]struct {
		response string
		wantErr  bool
	}{
		"cleared": {response: strings.Replace(awsKeyFixture("unattached"), `"prod-us-key"`, `null`, 1)},
		"kept":    {response: awsKeyFixture("unattached"), wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			var gotBody map[string]any
			srv := newKeyServer(t, func(w http.ResponseWriter, _ *http.Request, body map[string]any) {
				gotBody = body
				_, _ = io.WriteString(w, tc.response)
			})
			r := &ExternalKeyResource{client: admintest.NewClient(t, srv)}
			s := schematest.ResourceSchema(t, r)
			state := tfsdk.State{Schema: s, Raw: keyValue(t, awsStateTop("unattached"), awsStatePC())}
			planTop := awsStateTop("unattached")
			planTop["display_name"] = nil
			planTop["attachment"] = tftypes.UnknownValue
			planTop["updated_at"] = tftypes.UnknownValue
			plan := tfsdk.Plan{Schema: s, Raw: keyValue(t, planTop, awsStatePC())}

			resp := resource.UpdateResponse{State: state}
			r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &resp)
			if want := `{"display_name":null}`; mustJSON(t, gotBody) != want {
				t.Errorf("body = %s, want %s", mustJSON(t, gotBody), want)
			}
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("diagnostics = %v, want error %v", resp.Diagnostics, tc.wantErr)
			}
			if tc.wantErr && !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "prod-us-key") {
				t.Errorf("detail = %q, want the kept name", resp.Diagnostics.Errors()[0].Detail())
			}
		})
	}
}

// An update planned from a value unknown at plan that resolves to the stored
// one has nothing to send: the key is read instead of POSTing an empty body,
// which the API may refuse.
func TestExternalKeyResource_Update_nothingToSendReads(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	var bodies []int64
	srv := newKeyServer(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		bodies = append(bodies, r.ContentLength)
		mu.Unlock()
		_, _ = io.WriteString(w, awsKeyFixture("attached"))
	})

	r := &ExternalKeyResource{client: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)
	state := tfsdk.State{Schema: s, Raw: keyValue(t, awsStateTop("unattached"), awsStatePC())}
	planTop := awsStateTop("unattached")
	planTop["attachment"] = tftypes.UnknownValue
	planTop["updated_at"] = tftypes.UnknownValue
	plan := tfsdk.Plan{Schema: s, Raw: keyValue(t, planTop, awsStatePC())}

	resp := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}
	if want := []string{"GET /v1/organizations/external_keys/" + testKeyID}; strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if bodies[0] > 0 {
		t.Errorf("the read sent a body of %d bytes", bodies[0])
	}
	var data ExternalKeyModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if data.Attachment.ValueString() != "attached" {
		t.Errorf("attachment = %v, want the value read back", data.Attachment)
	}
}
