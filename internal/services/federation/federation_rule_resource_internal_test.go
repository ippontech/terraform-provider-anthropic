// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// --- mapFederationRuleToState ---

func TestMapFederationRuleToState_BasicFields(t *testing.T) {
	createdAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	rule := &anthropic.BetaFederationRule{
		ID:                     "fdrl_01ABC",
		Name:                   "gha-deploy",
		Description:            "GitHub Actions deploy",
		IssuerID:               "fdis_01XYZ",
		IssuerName:             "GitHub Actions",
		OAuthScope:             "workspace:developer",
		WorkspaceID:            "wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm",
		WorkspaceIDs:           []string{"wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm"},
		AppliesToAllWorkspaces: false,
		TokenLifetimeSeconds:   3600,
		CreatedAt:              createdAt,
		UpdatedAt:              updatedAt,
		CreatedByActorID:       "user_01CREATOR",
		UpdatedByActorID:       "user_01UPDATER",
		Match: anthropic.BetaFederationRuleMatch{
			SubjectPrefix: "repo:my-org/my-repo:*",
		},
		Target: anthropic.BetaServiceAccountTarget{
			ServiceAccountID:   "svac_01SVC",
			ServiceAccountName: "deploy-bot",
		},
	}

	var data FederationRuleResourceModel
	diags := mapFederationRuleToState(context.Background(), rule, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if got := data.ID.ValueString(); got != "fdrl_01ABC" {
		t.Errorf("ID = %q, want fdrl_01ABC", got)
	}
	if got := data.Name.ValueString(); got != "gha-deploy" {
		t.Errorf("Name = %q, want gha-deploy", got)
	}
	if got := data.Description.ValueString(); got != "GitHub Actions deploy" {
		t.Errorf("Description = %q, want \"GitHub Actions deploy\"", got)
	}
	if got := data.IssuerID.ValueString(); got != "fdis_01XYZ" {
		t.Errorf("IssuerID = %q, want fdis_01XYZ", got)
	}
	if got := data.IssuerName.ValueString(); got != "GitHub Actions" {
		t.Errorf("IssuerName = %q, want \"GitHub Actions\"", got)
	}
	if got := data.OAuthScope.ValueString(); got != "workspace:developer" {
		t.Errorf("OAuthScope = %q, want workspace:developer", got)
	}
	if got := data.WorkspaceID.ValueString(); got != "wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm" {
		t.Errorf("WorkspaceID = %q, want wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm", got)
	}
	if data.AppliesToAllWorkspaces.ValueBool() {
		t.Errorf("AppliesToAllWorkspaces = true, want false")
	}
	if got := data.TokenLifetimeSeconds.ValueInt64(); got != 3600 {
		t.Errorf("TokenLifetimeSeconds = %d, want 3600", got)
	}
	if got := data.CreatedAt.ValueString(); got != "2024-01-15T10:00:00Z" {
		t.Errorf("CreatedAt = %q, want 2024-01-15T10:00:00Z", got)
	}
	if got := data.UpdatedAt.ValueString(); got != "2024-01-15T11:00:00Z" {
		t.Errorf("UpdatedAt = %q, want 2024-01-15T11:00:00Z", got)
	}
	if !data.ArchivedAt.IsNull() {
		t.Errorf("ArchivedAt = %q, want null", data.ArchivedAt.ValueString())
	}
	if !data.ArchivedByActorID.IsNull() {
		t.Errorf("ArchivedByActorID = %q, want null", data.ArchivedByActorID.ValueString())
	}

	var match federationRuleMatchModel
	if d := data.Match.As(context.Background(), &match, basetypes.ObjectAsOptions{}); d.HasError() {
		t.Fatalf("Match.As: %v", d)
	}
	if got := match.SubjectPrefix.ValueString(); got != "repo:my-org/my-repo:*" {
		t.Errorf("Match.SubjectPrefix = %q, want \"repo:my-org/my-repo:*\"", got)
	}

	var target federationRuleTargetModel
	if d := data.Target.As(context.Background(), &target, basetypes.ObjectAsOptions{}); d.HasError() {
		t.Fatalf("Target.As: %v", d)
	}
	if got := target.ServiceAccountID.ValueString(); got != "svac_01SVC" {
		t.Errorf("Target.ServiceAccountID = %q, want svac_01SVC", got)
	}
	if got := target.ServiceAccountName.ValueString(); got != "deploy-bot" {
		t.Errorf("Target.ServiceAccountName = %q, want deploy-bot", got)
	}

	var workspaceIDs []string
	if d := data.WorkspaceIDs.ElementsAs(context.Background(), &workspaceIDs, false); d.HasError() {
		t.Fatalf("WorkspaceIDs.ElementsAs: %v", d)
	}
	if len(workspaceIDs) != 1 || workspaceIDs[0] != "wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm" {
		t.Errorf("WorkspaceIDs = %v, want [wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm]", workspaceIDs)
	}
}

func TestMapFederationRuleToState_ArchivedAtNonZero(t *testing.T) {
	archivedAt := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	rule := &anthropic.BetaFederationRule{
		ID:                "fdrl_01ARCHIVED",
		ArchivedAt:        archivedAt,
		ArchivedByActorID: "user_01ARCHIVER",
		Target:            anthropic.BetaServiceAccountTarget{ServiceAccountID: "svac_01SVC"},
	}

	var data FederationRuleResourceModel
	diags := mapFederationRuleToState(context.Background(), rule, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if got := data.ArchivedAt.ValueString(); got != "2024-06-01T12:00:00Z" {
		t.Errorf("ArchivedAt = %q, want 2024-06-01T12:00:00Z", got)
	}
	if got := data.ArchivedByActorID.ValueString(); got != "user_01ARCHIVER" {
		t.Errorf("ArchivedByActorID = %q, want user_01ARCHIVER", got)
	}
}

func TestMapFederationRuleToState_EmptyWorkspaceIDAndDescriptionAreNull(t *testing.T) {
	rule := &anthropic.BetaFederationRule{
		ID:                     "fdrl_01ALL",
		AppliesToAllWorkspaces: true,
		WorkspaceID:            "",
		WorkspaceIDs:           []string{"wrkspc_A", "wrkspc_B"},
		Target:                 anthropic.BetaServiceAccountTarget{ServiceAccountID: "svac_01SVC"},
	}

	var data FederationRuleResourceModel
	diags := mapFederationRuleToState(context.Background(), rule, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if !data.WorkspaceID.IsNull() {
		t.Errorf("WorkspaceID = %q, want null", data.WorkspaceID.ValueString())
	}
	if !data.Description.IsNull() {
		t.Errorf("Description = %q, want null", data.Description.ValueString())
	}
	if !data.CreatedByActorID.IsNull() {
		t.Errorf("CreatedByActorID = %q, want null", data.CreatedByActorID.ValueString())
	}
	if !data.CreatedAt.IsNull() {
		t.Errorf("CreatedAt = %q, want null (zero time)", data.CreatedAt.ValueString())
	}
	if !data.AppliesToAllWorkspaces.ValueBool() {
		t.Errorf("AppliesToAllWorkspaces = false, want true")
	}
}

func TestMapFederationRuleToState_AttributesEmptyIsNull(t *testing.T) {
	rule := &anthropic.BetaFederationRule{
		ID:     "fdrl_01ATTR",
		Target: anthropic.BetaServiceAccountTarget{ServiceAccountID: "svac_01SVC"},
	}

	var data FederationRuleResourceModel
	diags := mapFederationRuleToState(context.Background(), rule, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if !data.Attributes.IsNull() {
		t.Errorf("Attributes = %v, want null", data.Attributes)
	}
}

// --- buildMatchParam / buildTargetParam ---

func newObjectFromModel[T any](t *testing.T, ctx context.Context, attrTypes map[string]attr.Type, model T) types.Object {
	t.Helper()
	obj, diags := types.ObjectValueFrom(ctx, attrTypes, model)
	if diags.HasError() {
		t.Fatalf("ObjectValueFrom: %v", diags)
	}
	return obj
}

func TestBuildMatchParam_AllFieldsSet(t *testing.T) {
	ctx := context.Background()
	claims, diags := types.MapValueFrom(ctx, types.StringType, map[string]string{"repository_owner": "my-org"})
	if diags.HasError() {
		t.Fatalf("MapValueFrom: %v", diags)
	}

	matchObj := newObjectFromModel(t, ctx, federationRuleMatchAttrTypes, federationRuleMatchModel{
		SubjectPrefix: types.StringValue("repo:my-org/my-repo:*"),
		Audience:      types.StringValue("https://anthropic.com"),
		Claims:        claims,
		Condition:     types.StringValue("claims.repository_owner == \"my-org\""),
	})

	params, d := buildMatchParam(ctx, matchObj)
	if d.HasError() {
		t.Fatalf("buildMatchParam: %v", d)
	}

	if got := params.SubjectPrefix.Value; got != "repo:my-org/my-repo:*" {
		t.Errorf("SubjectPrefix = %q, want \"repo:my-org/my-repo:*\"", got)
	}
	if got := params.Audience.Value; got != "https://anthropic.com" {
		t.Errorf("Audience = %q, want \"https://anthropic.com\"", got)
	}
	if got := params.Condition.Value; got != `claims.repository_owner == "my-org"` {
		t.Errorf("Condition = %q, want the CEL expression", got)
	}
	if got := params.Claims["repository_owner"]; got != "my-org" {
		t.Errorf("Claims[repository_owner] = %q, want my-org", got)
	}
}

func TestBuildMatchParam_OnlySubjectPrefix(t *testing.T) {
	ctx := context.Background()
	matchObj := newObjectFromModel(t, ctx, federationRuleMatchAttrTypes, federationRuleMatchModel{
		SubjectPrefix: types.StringValue("repo:my-org/*"),
		Audience:      types.StringNull(),
		Claims:        types.MapNull(types.StringType),
		Condition:     types.StringNull(),
	})

	params, d := buildMatchParam(ctx, matchObj)
	if d.HasError() {
		t.Fatalf("buildMatchParam: %v", d)
	}

	if params.Audience.Valid() {
		t.Errorf("Audience should not be set")
	}
	if params.Claims != nil {
		t.Errorf("Claims = %v, want nil", params.Claims)
	}
	if params.Condition.Valid() {
		t.Errorf("Condition should not be set")
	}
}

func TestBuildTargetParam(t *testing.T) {
	ctx := context.Background()
	targetObj := newObjectFromModel(t, ctx, federationRuleTargetAttrTypes, federationRuleTargetModel{
		ServiceAccountID:   types.StringValue("svac_01SVC"),
		ServiceAccountName: types.StringValue("ignored-on-write"),
	})

	params, d := buildTargetParam(ctx, targetObj)
	if d.HasError() {
		t.Fatalf("buildTargetParam: %v", d)
	}

	if got := params.ServiceAccountID; got != "svac_01SVC" {
		t.Errorf("ServiceAccountID = %q, want svac_01SVC", got)
	}
	// service_account_name is ignored on writes: it must never be sent.
	if params.ServiceAccountName.Valid() {
		t.Errorf("ServiceAccountName should not be set on the param, it is ignored on writes")
	}
}

// --- mapMatchResponseToObject / mapTargetResponseToObject ---

func TestMapMatchResponseToObject_EmptyFieldsAreNull(t *testing.T) {
	obj, diags := mapMatchResponseToObject(anthropic.BetaFederationRuleMatch{})
	if diags.HasError() {
		t.Fatalf("mapMatchResponseToObject: %v", diags)
	}

	var m federationRuleMatchModel
	if d := obj.As(context.Background(), &m, basetypes.ObjectAsOptions{}); d.HasError() {
		t.Fatalf("As: %v", d)
	}
	if !m.SubjectPrefix.IsNull() || !m.Audience.IsNull() || !m.Condition.IsNull() || !m.Claims.IsNull() {
		t.Errorf("expected all match fields to be null, got %+v", m)
	}
}

func TestMapTargetResponseToObject_EmptyNameIsNull(t *testing.T) {
	obj, diags := mapTargetResponseToObject(anthropic.BetaServiceAccountTarget{ServiceAccountID: "svac_01SVC"})
	if diags.HasError() {
		t.Fatalf("mapTargetResponseToObject: %v", diags)
	}

	var target federationRuleTargetModel
	if d := obj.As(context.Background(), &target, basetypes.ObjectAsOptions{}); d.HasError() {
		t.Fatalf("As: %v", d)
	}
	if !target.ServiceAccountName.IsNull() {
		t.Errorf("ServiceAccountName = %q, want null", target.ServiceAccountName.ValueString())
	}
}

// --- federationRuleConfigValidator: match ---

func matchObject(t *testing.T, subjectPrefix, audience, condition types.String, claims types.Map) types.Object {
	t.Helper()
	obj, diags := types.ObjectValue(federationRuleMatchAttrTypes, map[string]attr.Value{
		"subject_prefix": subjectPrefix,
		"audience":       audience,
		"claims":         claims,
		"condition":      condition,
	})
	if diags.HasError() {
		t.Fatalf("ObjectValue: %v", diags)
	}
	return obj
}

func targetObject(t *testing.T, serviceAccountID string) types.Object {
	t.Helper()
	obj, diags := types.ObjectValue(federationRuleTargetAttrTypes, map[string]attr.Value{
		"service_account_id":   types.StringValue(serviceAccountID),
		"service_account_name": types.StringNull(),
	})
	if diags.HasError() {
		t.Fatalf("ObjectValue: %v", diags)
	}
	return obj
}

func baseValidModel(t *testing.T) FederationRuleResourceModel {
	return FederationRuleResourceModel{
		Name:        types.StringValue("gha-deploy"),
		IssuerID:    types.StringValue("fdis_01XYZ"),
		OAuthScope:  types.StringValue("workspace:developer"),
		Target:      targetObject(t, "svac_01SVC"),
		WorkspaceID: types.StringValue("wrkspc_01ABC"),
	}
}

// validateResource exercises the pure validateFederationRuleConfig helper
// directly, rather than marshalling the model through a tfsdk.Config (which
// would require assembling a full tftypes.Value by hand). This is the same
// logic ValidateResource calls after decoding req.Config.
func validateResource(data FederationRuleResourceModel) diag.Diagnostics {
	return validateFederationRuleConfig(context.Background(), data)
}

func TestFederationRuleConfigValidator_MatchMissingAllThree(t *testing.T) {
	data := baseValidModel(t)
	data.Match = matchObject(t, types.StringNull(), types.StringValue("https://anthropic.com"), types.StringNull(), types.MapNull(types.StringType))

	resp := validateResource(data)
	if !resp.HasError() {
		t.Fatalf("expected an error when only audience is set")
	}
}

func TestFederationRuleConfigValidator_MatchWithSubjectPrefixIsValid(t *testing.T) {
	data := baseValidModel(t)
	data.Match = matchObject(t, types.StringValue("repo:my-org/*"), types.StringNull(), types.StringNull(), types.MapNull(types.StringType))

	resp := validateResource(data)
	if resp.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp)
	}
}

func TestFederationRuleConfigValidator_MatchWithClaimsIsValid(t *testing.T) {
	ctx := context.Background()
	claims, diags := types.MapValueFrom(ctx, types.StringType, map[string]string{"repository_owner": "my-org"})
	if diags.HasError() {
		t.Fatalf("MapValueFrom: %v", diags)
	}

	data := baseValidModel(t)
	data.Match = matchObject(t, types.StringNull(), types.StringNull(), types.StringNull(), claims)

	resp := validateResource(data)
	if resp.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp)
	}
}

func TestFederationRuleConfigValidator_MatchWithConditionIsValid(t *testing.T) {
	data := baseValidModel(t)
	data.Match = matchObject(t, types.StringNull(), types.StringNull(), types.StringValue("claims.repository_owner == \"my-org\""), types.MapNull(types.StringType))

	resp := validateResource(data)
	if resp.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp)
	}
}

func TestFederationRuleConfigValidator_MatchUnknownFieldSkipsCheck(t *testing.T) {
	data := baseValidModel(t)
	data.Match = matchObject(t, types.StringUnknown(), types.StringNull(), types.StringNull(), types.MapNull(types.StringType))

	resp := validateResource(data)
	if resp.HasError() {
		t.Fatalf("an unresolved match.subject_prefix must not be flagged yet: %v", resp)
	}
}

// --- federationRuleConfigValidator: workspace targeting ---

func TestFederationRuleConfigValidator_WorkspaceTargetingConflict(t *testing.T) {
	data := baseValidModel(t)
	data.Match = matchObject(t, types.StringValue("repo:my-org/*"), types.StringNull(), types.StringNull(), types.MapNull(types.StringType))
	data.WorkspaceID = types.StringValue("wrkspc_01ABC")
	data.AppliesToAllWorkspaces = types.BoolValue(true)

	resp := validateResource(data)
	if !resp.HasError() {
		t.Fatalf("expected an error when both workspace_id and applies_to_all_workspaces=true are set")
	}
}

func TestFederationRuleConfigValidator_WorkspaceTargetingMissing(t *testing.T) {
	data := baseValidModel(t)
	data.Match = matchObject(t, types.StringValue("repo:my-org/*"), types.StringNull(), types.StringNull(), types.MapNull(types.StringType))
	data.WorkspaceID = types.StringNull()
	data.AppliesToAllWorkspaces = types.BoolNull()

	resp := validateResource(data)
	if !resp.HasError() {
		t.Fatalf("expected an error when neither workspace_id nor applies_to_all_workspaces is set")
	}
}

func TestFederationRuleConfigValidator_WorkspaceTargetingAppliesToAllOnlyIsValid(t *testing.T) {
	data := baseValidModel(t)
	data.Match = matchObject(t, types.StringValue("repo:my-org/*"), types.StringNull(), types.StringNull(), types.MapNull(types.StringType))
	data.WorkspaceID = types.StringNull()
	data.AppliesToAllWorkspaces = types.BoolValue(true)

	resp := validateResource(data)
	if resp.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp)
	}
}

func TestFederationRuleConfigValidator_WorkspaceTargetingFalseIsNotASelection(t *testing.T) {
	data := baseValidModel(t)
	data.Match = matchObject(t, types.StringValue("repo:my-org/*"), types.StringNull(), types.StringNull(), types.MapNull(types.StringType))
	data.WorkspaceID = types.StringNull()
	data.AppliesToAllWorkspaces = types.BoolValue(false)

	resp := validateResource(data)
	if !resp.HasError() {
		t.Fatalf("expected an error: applies_to_all_workspaces=false is not a real selection")
	}
}

func TestFederationRuleConfigValidator_WorkspaceTargetingUnknownSkipsCheck(t *testing.T) {
	data := baseValidModel(t)
	data.Match = matchObject(t, types.StringValue("repo:my-org/*"), types.StringNull(), types.StringNull(), types.MapNull(types.StringType))
	data.WorkspaceID = types.StringUnknown()
	data.AppliesToAllWorkspaces = types.BoolNull()

	resp := validateResource(data)
	if resp.HasError() {
		t.Fatalf("an unresolved workspace_id must not be flagged yet: %v", resp)
	}
}

// --- 404 on read / archive on delete (SDK-level, matching the Read/Delete handling) ---

func newTestOAuthAnthropicClient(t *testing.T, srv *httptest.Server) *anthropic.Client {
	t.Helper()
	c := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAuthToken("test"))
	return &c
}

func TestFederationRuleGet_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/organizations/federation_rules/fdrl_missing" {
			http.Error(w, "unexpected path", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"federation rule not found"}}`)
	}))
	defer srv.Close()

	client := newTestOAuthAnthropicClient(t, srv)
	_, err := client.Beta.Organization.Federation.Rules.Get(context.Background(), "fdrl_missing", anthropic.BetaOrganizationFederationRuleGetParams{})

	var apierr *anthropic.Error
	if !errors.As(err, &apierr) {
		t.Fatalf("expected *anthropic.Error, got: %v", err)
	}
	if apierr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apierr.StatusCode)
	}
}

func TestFederationRuleArchive_SendsCorrectPath(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{
			"id": "fdrl_01ABC",
			"applies_to_all_workspaces": false,
			"archived_at": "2024-06-01T12:00:00Z",
			"archived_by_actor_id": "user_01ARCHIVER",
			"attributes": null,
			"created_at": "2024-01-15T10:00:00Z",
			"created_by_actor_id": "user_01CREATOR",
			"description": "",
			"issuer_id": "fdis_01XYZ",
			"issuer_name": "GitHub Actions",
			"match": {"subject_prefix": "repo:my-org/*"},
			"name": "gha-deploy",
			"oauth_scope": "workspace:developer",
			"target": {"service_account_id": "svac_01SVC", "type": "service_account"},
			"token_lifetime_seconds": 3600,
			"type": "federation_rule",
			"updated_at": "2024-01-15T11:00:00Z",
			"updated_by_actor_id": "user_01UPDATER",
			"workspace_id": "",
			"workspace_ids": []
		}`)
	}))
	defer srv.Close()

	client := newTestOAuthAnthropicClient(t, srv)
	rule, err := client.Beta.Organization.Federation.Rules.Archive(context.Background(), "fdrl_01ABC", anthropic.BetaOrganizationFederationRuleArchiveParams{})
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/organizations/federation_rules/fdrl_01ABC/archive" {
		t.Errorf("path = %q, want /v1/organizations/federation_rules/fdrl_01ABC/archive", gotPath)
	}
	if rule.ArchivedAt.IsZero() {
		t.Errorf("expected ArchivedAt to be set")
	}
}

// --- ImportState ---

// schemaType returns the resource schema's underlying tftypes.Object type, so
// a null-valued tfsdk.State can be built by hand for ImportState (which calls
// resp.State.SetAttribute, requiring an existing Raw value tree).
func schemaType(t *testing.T) tftypes.Type {
	t.Helper()
	var schemaResp resource.SchemaResponse
	(&FederationRuleResource{}).Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	tfType, ok := schemaResp.Schema.Type().(interface {
		TerraformType(context.Context) tftypes.Type
	})
	if !ok {
		t.Fatal("schema type does not implement TerraformType")
	}
	return tfType.TerraformType(context.Background())
}

func nullValuesForSchema(t *testing.T) map[string]tftypes.Value {
	t.Helper()
	schemaObjType := schemaType(t).(tftypes.Object)
	vals := make(map[string]tftypes.Value, len(schemaObjType.AttributeTypes))
	for name, typ := range schemaObjType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	return vals
}

func TestFederationRuleImportState(t *testing.T) {
	ctx := context.Background()
	r := &FederationRuleResource{}

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	rawVal := tftypes.NewValue(schemaType(t), nullValuesForSchema(t))
	state := tfsdk.State{Raw: rawVal, Schema: schemaResp.Schema}

	req := resource.ImportStateRequest{ID: "fdrl_01ABC"}
	var resp resource.ImportStateResponse
	resp.State = state

	r.ImportState(ctx, req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	var id types.String
	if d := resp.State.GetAttribute(ctx, path.Root("id"), &id); d.HasError() {
		t.Fatalf("GetAttribute: %v", d)
	}
	if id.ValueString() != "fdrl_01ABC" {
		t.Errorf("id = %q, want fdrl_01ABC", id.ValueString())
	}
}
