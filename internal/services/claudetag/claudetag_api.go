// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package claudetag implements the Claude Tag API
// (https://platform.claude.com/docs/en/manage-claude/claude-tag-api): the
// Claude Tag settings of Slack channels and Slack workspaces, and the Access
// bundles attached to them, for a Claude Enterprise organization. The
// endpoints are organization endpoints in beta that the pinned SDK (v1.84)
// does not have, so they go through internal/admin with a Claude Enterprise
// scoped Admin API key (`read:claude_tag`, `write:claude_tag`), as the
// Plugins API does.
package claudetag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

const (
	// claudeTagBeta is the `anthropic-beta` value every request must carry:
	// "A request without it returns 404, exactly as if the endpoint did not
	// exist" (guide, introduction).
	claudeTagBeta = "experimental-claude-tag-2026-10-21"

	claudeTagPath       = "/v1/organizations/claude_tag"
	slackChannelsPath   = claudeTagPath + "/slack_channels"
	slackWorkspacesPath = claudeTagPath + "/slack_workspaces"
	bundlesPath         = claudeTagPath + "/bundles"

	// listLimit is the page size of every list walk: "limit defaults to
	// 20, with a minimum of 1 and a maximum of 100" (guide, Pagination).
	listLimit = 100
)

// claudeTagRequest sends one Claude Tag API request with the beta header.
// internal/admin adds the key (x-api-key) and `anthropic-version`.
func claudeTagRequest(ctx context.Context, client *admin.Client, method, path string, query url.Values, body any) ([]byte, error) {
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return client.DoRequest(ctx, method, path, body, admin.WithBeta(claudeTagBeta))
}

// callJSON sends a request and decodes its answer into out, unless out is nil.
func callJSON(ctx context.Context, client *admin.Client, method, path string, query url.Values, body, out any) error {
	respBody, err := claudeTagRequest(ctx, client, method, path, query, body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("unable to parse the response: %w", err)
	}
	return nil
}

func slackChannelPath(id string) string   { return slackChannelsPath + "/" + url.PathEscape(id) }
func slackWorkspacePath(id string) string { return slackWorkspacesPath + "/" + url.PathEscape(id) }
func bundlePath(id string) string         { return bundlesPath + "/" + url.PathEscape(id) }
func bundleTargetsPath(bundleID string) string {
	return bundlePath(bundleID) + "/targets"
}
func bundleTargetPath(bundleID, targetID string) string {
	return bundleTargetsPath(bundleID) + "/" + url.PathEscape(targetID)
}

// inheritedQuery returns the query of a read: `include_inherited=true` to
// read what applies, nothing to read what is stored (the default; guide,
// Stored and inherited values).
func inheritedQuery(includeInherited bool) url.Values {
	q := url.Values{}
	if includeInherited {
		q.Set("include_inherited", "true")
	}
	return q
}

// --- API objects ---

// settingsAPIObject is the `settings` object of a channel or workspace: all
// five settings, null when the level does not set one (guide, Settings).
type settingsAPIObject struct {
	Enabled       *bool                 `json:"enabled"`
	Instructions  *string               `json:"instructions"`
	Model         *string               `json:"model"`
	EnvironmentID *string               `json:"environment_id"`
	GuestPolicy   *guestPolicyAPIObject `json:"guest_policy"`
}

// guestPolicyAPIObject is `{"type": "allow" | "restrict" | "channel_only"}`;
// the type may gain values (guide, Unrecognized values), kept as read.
type guestPolicyAPIObject struct {
	Type string `json:"type"`
}

// slackIDs is the `slack` object: Slack's own IDs.
type slackIDs struct {
	WorkspaceID string `json:"workspace_id"`
	ChannelID   string `json:"channel_id,omitempty"`
}

// slackChannelAPIObject is the channel object (guide, Slack channels).
type slackChannelAPIObject struct {
	ID        string            `json:"id"`
	Slack     slackIDs          `json:"slack"`
	Settings  settingsAPIObject `json:"settings"`
	CreatedAt string            `json:"created_at"`
	UpdatedAt string            `json:"updated_at"`
}

// slackWorkspaceAPIObject is the workspace object: the channel object
// without `slack.channel_id` (guide, Slack workspaces).
type slackWorkspaceAPIObject struct {
	ID        string            `json:"id"`
	Slack     slackIDs          `json:"slack"`
	Settings  settingsAPIObject `json:"settings"`
	CreatedAt string            `json:"created_at"`
	UpdatedAt string            `json:"updated_at"`
}

// bundleAPIObject is an Access bundle (guide, Bundles). The API returns its
// name and description, never what it grants.
type bundleAPIObject struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// bundleTargetAPIObject is one Bundle attached to one channel or workspace
// (guide, Bundle targets). It does not hold its Bundle's ID.
type bundleTargetAPIObject struct {
	Type             string `json:"type"`
	ID               string `json:"id"`
	SlackChannelID   string `json:"slack_channel_id"`
	SlackWorkspaceID string `json:"slack_workspace_id"`
	CreatedAt        string `json:"created_at"`
}

// --- Pagination ---

// listPage is the envelope of every list: an opaque `next_page` cursor,
// passed back unchanged as `page`, until it is null (guide, Pagination).
type listPage[T any] struct {
	Data     []T    `json:"data"`
	NextPage string `json:"next_page"`
}

// listAll walks a list to its end and returns every item. "A page can hold
// fewer than limit channels, or none, while next_page is still set", so only
// a null cursor ends the walk; the other query parameters stay the same on
// every page, since a cursor sent with the other value of include_inherited
// is a 400. A cursor that comes back unchanged would loop, so it is an error.
func listAll[T any](ctx context.Context, client *admin.Client, path string, query url.Values) ([]T, error) {
	q := url.Values{}
	maps.Copy(q, query)
	q.Set("limit", strconv.Itoa(listLimit))
	items := []T{}
	for {
		var page listPage[T]
		if err := callJSON(ctx, client, http.MethodGet, path, q, nil, &page); err != nil {
			return nil, err
		}
		items = append(items, page.Data...)
		if page.NextPage == "" {
			return items, nil
		}
		if page.NextPage == q.Get("page") {
			return nil, fmt.Errorf("the API returned the cursor it was sent (%q) as next_page, so the list cannot advance", page.NextPage)
		}
		q.Set("page", page.NextPage)
	}
}

// lookup sends a find-by-Slack-ID list request, which "does not page" and
// answers with the one object in `data`, or none when it is not set up
// (guide, Find a channel by its Slack IDs; List workspaces).
func lookup[T any](ctx context.Context, client *admin.Client, path string, query url.Values) (*T, error) {
	var page listPage[T]
	if err := callJSON(ctx, client, http.MethodGet, path, query, nil, &page); err != nil {
		return nil, err
	}
	if len(page.Data) == 0 {
		return nil, nil
	}
	return &page.Data[0], nil
}

// --- Errors ---

// errorDetails is the `error.details` object the API adds "when one status
// has several causes that you would handle differently" (guide, Error
// responses).
type errorDetails struct {
	ErrorCode      string `json:"error_code"`
	SlackChannelID string `json:"slack_channel_id"`
}

// apiErrorOf returns the status and error details of an API error, or a zero
// status when err is not one.
func apiErrorOf(err error) (int, errorDetails) {
	apiErr, ok := errors.AsType[*admin.APIError](err)
	if !ok {
		return 0, errorDetails{}
	}
	var details errorDetails
	if len(apiErr.Details) > 0 {
		_ = json.Unmarshal(apiErr.Details, &details)
	}
	return apiErr.StatusCode, details
}

// errorHints says what to do about each error_code of the guide's Error
// responses table; a code it does not list is reported as its status alone,
// as the guide asks for unrecognized codes.
var errorHints = map[string]string{
	"organization_level_scoped_api_key_required": "The Claude Tag API needs a Claude Enterprise scoped Admin API key that belongs to the " +
		"organization as a whole (sk-ant-api01-..., created by the primary owner in claude.ai > Organization settings > API), " +
		"set as admin_api_key. A key created in the Claude Console does not work.",
	"api_key_scope_missing": "The key lacks the scope this request needs: read:claude_tag for reads, write:claude_tag for changes. " +
		"Scopes are fixed at creation, so the primary owner creates a new key with both.",
	"claude_tag_writes_not_enabled": "The organization can read through the Claude Tag API but is not enabled for changes, " +
		"so no setting, channel or Bundle attachment can be changed through it.",
	"compliance_configuration_restricted": "The organization has a compliance configuration (such as zero data retention) " +
		"under which Claude Tag stores nothing new: setting a channel up, changing settings (even to clear them) and attaching " +
		"a Bundle are refused, while removing a channel and detaching a Bundle are not.",
	"environment_not_found": "environment_id names no environment the organization can use with Claude Tag: it must be an " +
		"active environment (env_...) of the Claude Enterprise organization.",
	"environment_archived":                       "environment_id names an archived environment.",
	"slack_channel_set_up_settings_refused":      setUpPartlyHint,
	"slack_channel_set_up_environment_not_found": setUpPartlyHint,
	"slack_channel_set_up_settings_conflict":     setUpPartlyHint,
	"slack_channel_enabled": "Claude Tag removes a channel only while Claude is off in it, and Claude is on in this one " +
		"(enabled resolves to true). Set enabled = false on this resource and apply, then destroy it. Removal deletes the " +
		"channel's sessions, memory and routines; to stop managing the channel without removing it, use a removed block " +
		"or terraform state rm instead.",
}

const setUpPartlyHint = "The request set the channel up, then its settings were refused, so the channel stays set up " +
	"with nothing stored. Correct the settings and apply again: the next apply finds the channel and adopts it."

// errorDetail is the detail of a failed call's diagnostic: the action, the
// API's error and, for a documented error_code, what to do about it.
func errorDetail(action string, err error) string {
	msg := fmt.Sprintf("Unable to %s: %s", action, err)
	_, details := apiErrorOf(err)
	var extra []string
	if details.ErrorCode != "" {
		extra = append(extra, "error_code "+details.ErrorCode)
	}
	if details.SlackChannelID != "" {
		extra = append(extra, "channel "+details.SlackChannelID)
	}
	if len(extra) > 0 {
		msg += " (" + strings.Join(extra, ", ") + ")"
	}
	if hint := errorHints[details.ErrorCode]; hint != "" {
		msg += "\n\n" + hint
	}
	return msg
}

// readFailure tells what a failed read of a managed object means. A 404
// without an error_code has two causes: the object is gone, or every
// endpoint reads as nonexistent (no beta header, or the API is not enabled
// for the organization). As the guide prescribes (IDs; Error responses), a
// request that names nothing, listing the workspaces with the same key and
// headers, tells them apart: a 200 means the object is gone, and gone is
// true. Any other answer says nothing about the object, so it is an error.
func readFailure(ctx context.Context, client *admin.Client, what string, err error) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	status, details := apiErrorOf(err)
	if status != http.StatusNotFound || details.ErrorCode != "" {
		diags.AddError("Client Error", errorDetail("read "+what, err))
		return false, diags
	}
	probe := url.Values{"limit": {"1"}}
	if _, probeErr := claudeTagRequest(ctx, client, http.MethodGet, slackWorkspacesPath, probe, nil); probeErr != nil {
		diags.AddError("Claude Tag API Unavailable", fmt.Sprintf(
			"Reading %s returned 404, and so did listing the Slack workspaces, a request that names nothing (%s). "+
				"The 404 therefore does not say the %s is gone: the Claude Tag API reads as nonexistent for this key. "+
				"Check that the organization is a Claude Enterprise one with the Claude Tag API enabled, and that "+
				"admin_api_key is its scoped Admin API key with the read:claude_tag scope.", what, probeErr, what))
		return false, diags
	}
	return true, nil
}
