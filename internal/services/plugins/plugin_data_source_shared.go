// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// The data sources map API objects straight to object values (one attribute
// map per kind of object), used both as a single data source's whole state
// and as the elements of a plural data source's list.

// pluginsDataSourceAuth is the credential sentence of every data source's
// description.
const pluginsDataSourceAuth = " Requires a Claude Enterprise Admin API key with the `read:plugins` scope " +
	"(`read:org_audit` and `read:compliance_org_data` work too) in `admin_api_key`."

// configurePluginsDataSource is the shared Configure of the data sources.
func configurePluginsDataSource(req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) *admin.Client {
	if req.ProviderData == nil {
		return nil
	}
	pd, ok := req.ProviderData.(*providerdata.ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *providerdata.ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return nil
	}
	if !providerrors.RequireAdminDataSourceClient(pd.AdminClient, &resp.Diagnostics) {
		return nil
	}
	return pd.AdminClient
}

// --- Plugin ---

var pluginAttrTypes = map[string]attr.Type{
	"id":                                   types.StringType,
	"name":                                 types.StringType,
	"display_name":                         types.StringType,
	"description":                          types.StringType,
	"manifest_version":                     types.StringType,
	"served_version_id":                    types.StringType,
	"served_version_pinned":                types.BoolType,
	"latest_version_id":                    types.StringType,
	"owner_type":                           types.StringType,
	"owner_user_id":                        types.StringType,
	"marketplace_id":                       types.StringType,
	"created_by":                           types.ObjectType{AttrTypes: pluginActorAttrTypes},
	"organization_installation_preference": types.StringType,
	"organization_installation_preference_inherited": types.BoolType,
	"content_scan": types.ObjectType{AttrTypes: pluginContentScanAttrTypes},
	"components":   types.ListType{ElemType: types.ObjectType{AttrTypes: pluginComponentAttrTypes}},
	"reach":        types.StringType,
	"created_at":   types.StringType,
	"updated_at":   types.StringType,
}

func pluginObjectValue(p *pluginAPIObject) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics
	contentScan, d := contentScanValue(p.ContentScan)
	diags.Append(d...)
	components, d := componentsValue(p.Components)
	diags.Append(d...)
	createdBy, d := actorValue(p.CreatedBy)
	diags.Append(d...)
	if diags.HasError() {
		return types.ObjectNull(pluginAttrTypes), diags
	}

	obj, d := types.ObjectValue(pluginAttrTypes, map[string]attr.Value{
		"id":                                   types.StringValue(p.ID),
		"name":                                 types.StringValue(p.Name),
		"display_name":                         tfvalue.StringOrNull(p.DisplayName),
		"description":                          tfvalue.StringOrNull(p.Description),
		"manifest_version":                     tfvalue.StringOrNull(p.ManifestVersion),
		"served_version_id":                    tfvalue.StringOrNull(p.ServedVersionID),
		"served_version_pinned":                types.BoolValue(p.ServedVersionPinned),
		"latest_version_id":                    tfvalue.StringOrNull(p.LatestVersionID),
		"owner_type":                           types.StringValue(p.Owner.Type),
		"owner_user_id":                        tfvalue.StringOrNull(p.Owner.UserID),
		"marketplace_id":                       types.StringValue(p.MarketplaceID),
		"created_by":                           createdBy,
		"organization_installation_preference": tfvalue.StringOrNull(p.OrganizationInstallationPreference),
		"organization_installation_preference_inherited": types.BoolPointerValue(p.OrganizationInstallationPreferenceInherited),
		"content_scan": contentScan,
		"components":   components,
		"reach":        tfvalue.StringOrNull(p.Reach),
		"created_at":   types.StringValue(p.CreatedAt),
		"updated_at":   tfvalue.StringOrNull(p.UpdatedAt),
	})
	diags.Append(d...)
	return obj, diags
}

// pluginDataSourceAttributes describes a plugin; id is the single data
// source's input, or a computed attribute of a list element.
func pluginDataSourceAttributes(id schema.StringAttribute) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":           id,
		"name":         computedString("The manifest's `name`: unique within its marketplace, not across the organization."),
		"display_name": computedString("The served version's manifest `displayName`, or null."),
		"description":  computedString("The served version's manifest `description`, or null."),
		"manifest_version": computedString("The served version's manifest `version`, normalized for display " +
			"(a leading `v` dropped), or null."),
		"served_version_id": computedString("The version members are served (prefixed `pluginver_`)."),
		"served_version_pinned": schema.BoolAttribute{
			Computed:            true,
			MarkdownDescription: "Whether the served version is pinned (chosen rather than following the newest version).",
		},
		"latest_version_id": computedString("The newest version, which differs from `served_version_id` while a stored version is not served."),
		"owner_type": computedString("`organization` for a plugin in one of the organization's marketplaces, " +
			"`user` for one in a member's personal marketplace."),
		"owner_user_id":  computedString("The member who owns the plugin, for `owner_type = \"user\"`."),
		"marketplace_id": computedString("The marketplace the plugin lives in (prefixed `marketplace_`)."),
		"created_by":     createdByDataSourceAttribute("Who created the plugin, null when no creator is recorded (plugins synchronized from Git)."),
		"organization_installation_preference": computedString("Organization-owned plugins: the organization-wide " +
			"installation setting, the plugin's own or its marketplace's default. Null for a member-owned plugin."),
		"organization_installation_preference_inherited": schema.BoolAttribute{
			Computed:            true,
			MarkdownDescription: "Whether `organization_installation_preference` comes from the marketplace's default. Null for a member-owned plugin.",
		},
		"content_scan": contentScanDataSourceAttribute("The served version's content scan, null when it was never scanned."),
		"components":   componentsDataSourceAttribute("The served version's components, null when not recorded."),
		"reach":        reachDataSourceAttribute("served version"),
		"created_at":   computedString("RFC 3339 timestamp of the plugin's creation."),
		"updated_at": computedString("RFC 3339 timestamp of the last served-version change or new version " +
			"(installation settings, shares and late scan verdicts do not move it)."),
	}
}

// --- Plugin version ---

var pluginVersionAttrTypes = map[string]attr.Type{
	"id":               types.StringType,
	"plugin_id":        types.StringType,
	"display_name":     types.StringType,
	"description":      types.StringType,
	"manifest_version": types.StringType,
	"release_notes":    types.StringType,
	"created_by":       types.ObjectType{AttrTypes: pluginActorAttrTypes},
	"content_scan":     types.ObjectType{AttrTypes: pluginContentScanAttrTypes},
	"components":       types.ListType{ElemType: types.ObjectType{AttrTypes: pluginComponentAttrTypes}},
	"reach":            types.StringType,
	"created_at":       types.StringType,
}

func pluginVersionObjectValue(v *pluginVersionAPIObject) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics
	contentScan, d := contentScanValue(v.ContentScan)
	diags.Append(d...)
	components, d := componentsValue(v.Components)
	diags.Append(d...)
	createdBy, d := actorValue(v.CreatedBy)
	diags.Append(d...)
	if diags.HasError() {
		return types.ObjectNull(pluginVersionAttrTypes), diags
	}

	obj, d := types.ObjectValue(pluginVersionAttrTypes, map[string]attr.Value{
		"id":               types.StringValue(v.ID),
		"plugin_id":        types.StringValue(v.PluginID),
		"display_name":     tfvalue.StringOrNull(v.DisplayName),
		"description":      tfvalue.StringOrNull(v.Description),
		"manifest_version": tfvalue.StringOrNull(v.ManifestVersion),
		"release_notes":    tfvalue.StringOrNull(v.ReleaseNotes),
		"created_by":       createdBy,
		"content_scan":     contentScan,
		"components":       components,
		"reach":            tfvalue.StringOrNull(v.Reach),
		"created_at":       types.StringValue(v.CreatedAt),
	})
	diags.Append(d...)
	return obj, diags
}

// --- Marketplace ---

var pluginMarketplaceAttrTypes = map[string]attr.Type{
	"id":                              types.StringType,
	"name":                            types.StringType,
	"owner_type":                      types.StringType,
	"owner_user_id":                   types.StringType,
	"source":                          types.StringType,
	"sync_status":                     types.StringType,
	"last_sync_ended_at":              types.StringType,
	"last_sync_read_sha":              types.StringType,
	"default_installation_preference": types.StringType,
	"created_at":                      types.StringType,
}

func pluginMarketplaceObjectValue(m *pluginMarketplaceAPIObject) (types.Object, diag.Diagnostics) {
	return types.ObjectValue(pluginMarketplaceAttrTypes, map[string]attr.Value{
		"id":                              types.StringValue(m.ID),
		"name":                            types.StringValue(m.Name),
		"owner_type":                      types.StringValue(m.Owner.Type),
		"owner_user_id":                   tfvalue.StringOrNull(m.Owner.UserID),
		"source":                          types.StringValue(m.Source),
		"sync_status":                     tfvalue.StringOrNull(m.SyncStatus),
		"last_sync_ended_at":              tfvalue.StringOrNull(m.LastSyncEndedAt),
		"last_sync_read_sha":              tfvalue.StringOrNull(m.LastSyncReadSHA),
		"default_installation_preference": tfvalue.StringOrNull(m.DefaultInstallationPreference),
		"created_at":                      types.StringValue(m.CreatedAt),
	})
}

func pluginMarketplaceDataSourceAttributes(id schema.StringAttribute) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":   id,
		"name": computedString("The marketplace's name, fixed for its lifetime."),
		"owner_type": computedString("`organization` for one of the organization's marketplaces, `user` for a " +
			"member's personal one."),
		"owner_user_id": computedString("The member who owns the marketplace, for `owner_type = \"user\"`."),
		"source": computedString("Where its plugins come from: `manual` (uploaded, in claude.ai or through the API) or " +
			"`github`, `gitlab`, `public_git` (synchronized from a repository)."),
		"sync_status": computedString("Outcome of the latest synchronization: `success`, `in_progress`, `failed_content`, " +
			"`failed_transient`, `failed_auth` or `failed_limits`; null until one was attempted (always for `manual`)."),
		"last_sync_ended_at": computedString("RFC 3339 timestamp of the latest synchronization attempt, whatever its outcome; null when not synchronized."),
		"last_sync_read_sha": computedString("The commit the latest synchronization read; null when not synchronized."),
		"default_installation_preference": computedString("Organization marketplaces: the organization-wide installation " +
			"setting of every plugin in it without its own (`not_available` if never set). Null for a personal marketplace."),
		"created_at": computedString("RFC 3339 timestamp of the marketplace's creation."),
	}
}

// --- Schema helpers ---

func computedString(description string) schema.StringAttribute {
	return schema.StringAttribute{Computed: true, MarkdownDescription: description}
}

func contentScanDataSourceAttribute(description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Computed:            true,
		MarkdownDescription: description + " Members are served a version only once its scan is `completed` with `pass` or `warn`.",
		Attributes: map[string]schema.Attribute{
			"status":     computedString("`processing` while the scan runs, `completed` when it finished, `errored` when it could not."),
			"assessment": computedString("Set once `status` is `completed`: `pass`, `warn`, `fail` or `unknown`."),
			"reason":     computedString("For `warn` and `fail`, the main concern (for example `credential-exposure`), otherwise null."),
		},
	}
}

func componentsDataSourceAttribute(description string) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		Computed:            true,
		MarkdownDescription: description,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"type":        computedString("`skill`, `mcp_server`, `command`, `agent`, `hook` or `cli`."),
				"name":        computedString("The component's name: an MCP server's key in the manifest, a hook's event, a CLI's executable."),
				"description": computedString("What the component declares about itself; always null for MCP servers, hooks and CLIs."),
			},
		},
	}
}

func reachDataSourceAttribute(what string) schema.StringAttribute {
	return computedString("How far the " + what + " reaches: `remote` (declares an MCP server or a CLI), `privileged` " +
		"(runs or takes effect on members' machines: hooks, settings, pre-approved tools) or `contained`; null when unclassified.")
}

func createdByDataSourceAttribute(description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Computed:            true,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"type":          computedString("`user_actor` (a member in claude.ai) or `api_actor` (an Admin API key); other types may appear."),
			"user_id":       computedString("The member's user ID, for a `user_actor`."),
			"email_address": computedString("The member's email address, for a `user_actor` (may be null)."),
			"api_key_id":    computedString("The Admin API key's ID, for an `api_actor`."),
		},
	}
}

// listOf builds a list of objects of the given attribute types, empty (never
// null) when there are no elements.
func listOf(attrTypes map[string]attr.Type, elems []attr.Value) (types.List, diag.Diagnostics) {
	if elems == nil {
		elems = []attr.Value{}
	}
	return types.ListValue(types.ObjectType{AttrTypes: attrTypes}, elems)
}
