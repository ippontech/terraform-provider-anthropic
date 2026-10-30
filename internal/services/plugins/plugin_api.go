// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package plugins implements the Claude Enterprise Plugins API
// (https://platform.claude.com/docs/en/manage-claude/plugins-api): plugins,
// their versions, installation settings and marketplaces. The endpoints are
// organization endpoints, so they go through internal/admin with a Claude
// Enterprise scoped Admin API key (`read:plugins`, `write:plugins`); the pinned
// SDK does not have them.
package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

const (
	// pluginsBeta is the `anthropic-beta` value every Plugins API request must
	// carry: without it the endpoints answer 404, as if they did not exist.
	pluginsBeta = "ce-plugins-2026-09-01"

	pluginsPath            = "/v1/organizations/plugins"
	pluginMarketplacesPath = "/v1/organizations/plugin_marketplaces"
)

// pluginsRequest sends one Plugins API request. Every call carries the beta
// header and, like the SDK's own requests to these endpoints, the `beta=true`
// query flag. An upload (a raw multipart body) gets a timeout sized for it.
func pluginsRequest(ctx context.Context, client *admin.Client, method, path string, query url.Values, body any) ([]byte, error) {
	q := url.Values{}
	for k, v := range query {
		q[k] = v
	}
	q.Set("beta", "true")
	if raw, ok := body.(admin.RawBody); ok {
		client = withUploadTimeout(client, len(raw.Data))
	}
	return client.DoRequest(ctx, method, path+"?"+q.Encode(), body, admin.WithBeta(pluginsBeta))
}

// pluginPath returns the path of one plugin, or of one of its sub-resources
// when more segments are given.
func pluginPath(pluginID string, segments ...string) string {
	p := pluginsPath + "/" + url.PathEscape(pluginID)
	for _, s := range segments {
		p += "/" + url.PathEscape(s)
	}
	return p
}

// --- API objects ---

// pluginAPIObject is a plugin as the API returns it. Its display fields
// (display_name, description, manifest_version, content_scan, components,
// reach) describe the served version.
type pluginAPIObject struct {
	ID                                          string             `json:"id"`
	Name                                        string             `json:"name"`
	DisplayName                                 string             `json:"display_name"`
	Description                                 string             `json:"description"`
	ManifestVersion                             string             `json:"manifest_version"`
	ServedVersionID                             string             `json:"served_version_id"`
	ServedVersionPinned                         bool               `json:"served_version_pinned"`
	LatestVersionID                             string             `json:"latest_version_id"`
	Owner                                       pluginOwner        `json:"owner"`
	MarketplaceID                               string             `json:"marketplace_id"`
	CreatedBy                                   *pluginActor       `json:"created_by"`
	OrganizationInstallationPreference          string             `json:"organization_installation_preference"`
	OrganizationInstallationPreferenceInherited *bool              `json:"organization_installation_preference_inherited"`
	ContentScan                                 *pluginContentScan `json:"content_scan"`
	Components                                  []pluginComponent  `json:"components"`
	Reach                                       string             `json:"reach"`
	CreatedAt                                   string             `json:"created_at"`
	UpdatedAt                                   string             `json:"updated_at"`
}

// pluginVersionAPIObject is one immutable version of a plugin.
type pluginVersionAPIObject struct {
	ID              string             `json:"id"`
	PluginID        string             `json:"plugin_id"`
	DisplayName     string             `json:"display_name"`
	Description     string             `json:"description"`
	ManifestVersion string             `json:"manifest_version"`
	ReleaseNotes    string             `json:"release_notes"`
	CreatedBy       *pluginActor       `json:"created_by"`
	ContentScan     *pluginContentScan `json:"content_scan"`
	Components      []pluginComponent  `json:"components"`
	Reach           string             `json:"reach"`
	CreatedAt       string             `json:"created_at"`
}

// pluginOwner is `{"type": "organization"}` or `{"type": "user", "user_id": ...}`.
type pluginOwner struct {
	Type   string `json:"type"`
	UserID string `json:"user_id"`
}

// pluginActor is who created a plugin or version: a `user_actor` (user_id,
// email_address) or an `api_actor` (api_key_id). Other types may appear.
type pluginActor struct {
	Type         string `json:"type"`
	UserID       string `json:"user_id"`
	EmailAddress string `json:"email_address"`
	APIKeyID     string `json:"api_key_id"`
}

type pluginContentScan struct {
	Status     string `json:"status"`
	Assessment string `json:"assessment"`
	Reason     string `json:"reason"`
}

type pluginComponent struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// pluginInstallationSettingAPIObject is the setting an organization-owned
// plugin holds for one target. It has no ID: the plugin and the target
// address it.
type pluginInstallationSettingAPIObject struct {
	PluginID               string       `json:"plugin_id"`
	Target                 pluginTarget `json:"target"`
	InstallationPreference string       `json:"installation_preference"`
	CreatedAt              string       `json:"created_at"`
	UpdatedAt              string       `json:"updated_at"`
}

// pluginTarget is who an installation setting or a share is for: the whole
// `organization`, one `rbac_group`, or one `organization_member` (shares only).
type pluginTarget struct {
	Type        string `json:"type"`
	RBACGroupID string `json:"rbac_group_id"`
	UserID      string `json:"user_id"`
}

// pathSegment returns the target as the installation-settings path names it:
// the literal `organization`, or the group's ID.
func (t pluginTarget) pathSegment() string {
	if t.Type == "rbac_group" {
		return t.RBACGroupID
	}
	return t.Type
}

// pluginMarketplaceAPIObject is a container of plugins: one of the
// organization's, or a member's personal one.
type pluginMarketplaceAPIObject struct {
	ID                            string      `json:"id"`
	Name                          string      `json:"name"`
	Owner                         pluginOwner `json:"owner"`
	Source                        string      `json:"source"`
	SyncStatus                    string      `json:"sync_status"`
	LastSyncEndedAt               string      `json:"last_sync_ended_at"`
	LastSyncReadSHA               string      `json:"last_sync_read_sha"`
	DefaultInstallationPreference string      `json:"default_installation_preference"`
	CreatedAt                     string      `json:"created_at"`
}

// installationPreferences are the values an installation setting or a
// marketplace default can hold, from the most permissive.
var installationPreferences = []string{"required", "auto_install", "available", "not_available"}

// --- Pagination ---

// pluginsPage is the envelope of every Plugins API list: an opaque `next_page`
// cursor, passed back as `page`, until it is null.
type pluginsPage[T any] struct {
	Data     []T    `json:"data"`
	NextPage string `json:"next_page"`
}

// maxPluginsListPages bounds a list walk: at the limits the provider asks for,
// far more items than an organization can hold.
const maxPluginsListPages = 10000

// listPluginsPages pages through a Plugins API list, calling visit with each
// item until it returns false. A page can be short, or empty, while
// `next_page` is still set, so only a null cursor ends the walk.
func listPluginsPages[T any](ctx context.Context, client *admin.Client, path string, query url.Values, limit int, visit func(T) bool) error {
	return listPluginsPagesUpTo(ctx, client, path, query, limit, maxPluginsListPages, visit)
}

// listPluginsPagesUpTo is listPluginsPages with its page bound as a
// parameter. A cursor already sent, or a walk past maxPages, is an error
// rather than a loop that only a context deadline would stop: empty pages
// with a cursor are allowed, so an API that keeps returning the same cursor,
// or cycles through a few, would otherwise hang plan and refresh.
func listPluginsPagesUpTo[T any](ctx context.Context, client *admin.Client, path string, query url.Values, limit, maxPages int, visit func(T) bool) error {
	q := url.Values{}
	for k, v := range query {
		q[k] = v
	}
	q.Set("limit", strconv.Itoa(limit))
	sent := map[string]bool{}
	for pages := 1; ; pages++ {
		respBody, err := pluginsRequest(ctx, client, http.MethodGet, path, q, nil)
		if err != nil {
			return err
		}
		var page pluginsPage[T]
		if err := json.Unmarshal(respBody, &page); err != nil {
			return fmt.Errorf("unable to parse the list response: %w", err)
		}
		for _, item := range page.Data {
			if !visit(item) {
				return nil
			}
		}
		switch {
		case page.NextPage == "":
			return nil
		case sent[page.NextPage]:
			return fmt.Errorf("the API returned the next_page cursor %q again, so the list of %s cannot be read to its end", page.NextPage, path)
		case pages >= maxPages:
			return fmt.Errorf("the list of %s still had a next_page cursor after %d pages; stopping", path, maxPages)
		}
		sent[page.NextPage] = true
		q.Set("page", page.NextPage)
	}
}

// --- Error details ---

// pluginErrorDetails is the `error.details` object the Plugins API adds when
// one status code has several causes a client handles differently.
type pluginErrorDetails struct {
	ErrorCode       string `json:"error_code"`
	PluginID        string `json:"plugin_id"`
	PluginVersionID string `json:"plugin_version_id"`
	SkillName       string `json:"skill_name"`
}

// pluginErrorOf returns the status code and error details of an API error, or
// a zero status when err is not one. Details stay zero when the API sent none.
func pluginErrorOf(err error) (int, pluginErrorDetails) {
	var apiErr *admin.APIError
	if !errors.As(err, &apiErr) {
		return 0, pluginErrorDetails{}
	}
	var details pluginErrorDetails
	if len(apiErr.Details) > 0 {
		_ = json.Unmarshal(apiErr.Details, &details)
	}
	return apiErr.StatusCode, details
}

// --- State mapping helpers shared by the resources and data sources ---

var pluginContentScanAttrTypes = map[string]attr.Type{
	"status":     types.StringType,
	"assessment": types.StringType,
	"reason":     types.StringType,
}

var pluginComponentAttrTypes = map[string]attr.Type{
	"type":        types.StringType,
	"name":        types.StringType,
	"description": types.StringType,
}

var pluginActorAttrTypes = map[string]attr.Type{
	"type":          types.StringType,
	"user_id":       types.StringType,
	"email_address": types.StringType,
	"api_key_id":    types.StringType,
}

// contentScanValue maps a content scan, null when the version was never scanned.
func contentScanValue(scan *pluginContentScan) (types.Object, diag.Diagnostics) {
	if scan == nil {
		return types.ObjectNull(pluginContentScanAttrTypes), nil
	}
	return types.ObjectValue(pluginContentScanAttrTypes, map[string]attr.Value{
		"status":     tfvalue.StringOrNull(scan.Status),
		"assessment": tfvalue.StringOrNull(scan.Assessment),
		"reason":     tfvalue.StringOrNull(scan.Reason),
	})
}

// componentsValue maps a component list: null when the API did not record
// one (JSON null), an empty list when the version declares no component.
func componentsValue(components []pluginComponent) (types.List, diag.Diagnostics) {
	elemType := types.ObjectType{AttrTypes: pluginComponentAttrTypes}
	if components == nil {
		return types.ListNull(elemType), nil
	}
	var diags diag.Diagnostics
	elems := make([]attr.Value, 0, len(components))
	for _, c := range components {
		obj, d := types.ObjectValue(pluginComponentAttrTypes, map[string]attr.Value{
			"type":        types.StringValue(c.Type),
			"name":        types.StringValue(c.Name),
			"description": tfvalue.StringOrNull(c.Description),
		})
		diags.Append(d...)
		elems = append(elems, obj)
	}
	if diags.HasError() {
		return types.ListNull(elemType), diags
	}
	list, d := types.ListValue(elemType, elems)
	diags.Append(d...)
	return list, diags
}

// actorValue maps a created_by actor, null when no creator is recorded.
func actorValue(actor *pluginActor) (types.Object, diag.Diagnostics) {
	if actor == nil {
		return types.ObjectNull(pluginActorAttrTypes), nil
	}
	return types.ObjectValue(pluginActorAttrTypes, map[string]attr.Value{
		"type":          types.StringValue(actor.Type),
		"user_id":       tfvalue.StringOrNull(actor.UserID),
		"email_address": tfvalue.StringOrNull(actor.EmailAddress),
		"api_key_id":    tfvalue.StringOrNull(actor.APIKeyID),
	})
}
