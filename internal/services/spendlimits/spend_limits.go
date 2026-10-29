// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package spendlimits holds the spend limit resource. It goes through the
// Admin API client (`pd.AdminClient`) because the pinned SDK v1.67.0 predates
// `client.Beta.Organization.SpendLimits` (added in v1.77.0).
package spendlimits

import (
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	spendLimitsPath = "/v1/organizations/spend_limits"

	periodMonthly = "monthly"
)

// Scope types the set endpoint accepts: `user` for a Claude Enterprise
// organization, `organization` and `workspace` for a Claude Console one. Any
// other combination returns 400.
const (
	scopeUser         = "user"
	scopeOrganization = "organization"
	scopeWorkspace    = "workspace"
)

var (
	writableScopeTypes = []string{scopeOrganization, scopeUser, scopeWorkspace}

	periods = []string{"daily", periodMonthly, "weekly"}
)

// spendLimitsURL appends the query to a spend limit path, always with
// `beta=true`, as the SDK (v1.77.0+) sends on every spend limit call. No
// `anthropic-beta` header: of these endpoints only the list (`GET
// /v1/organizations/spend_limits`, not used here) requires one,
// `spend-limit-reads-2026-09-26`; the get, set and delete references require
// none, and SDK v1.84.0 sends none on them.
func spendLimitsURL(path string, query url.Values) string {
	if query == nil {
		query = url.Values{}
	}
	query.Set("beta", "true")
	return path + "?" + query.Encode()
}

func spendLimitPath(id string) string {
	return spendLimitsPath + "/" + url.PathEscape(id)
}

// --- API types ---

// spendLimitScopeAPI is the scope union of the API, tagged on `type`. Each
// variant carries at most one identifier; the others stay empty.
type spendLimitScopeAPI struct {
	Type        string `json:"type"`
	UserID      string `json:"user_id,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
}

// spendLimitAPIResponse is a BetaSpendLimit.
type spendLimitAPIResponse struct {
	ID        string             `json:"id"`
	Amount    *string            `json:"amount"`
	Currency  string             `json:"currency"`
	IsEnabled bool               `json:"is_enabled"`
	Period    string             `json:"period"`
	Scope     spendLimitScopeAPI `json:"scope"`
	CreatedAt string             `json:"created_at"`
	UpdatedAt string             `json:"updated_at"`
}

// spendLimitSetRequest is the body of POST /v1/organizations/spend_limits.
// Amount has no omitempty: a nil amount is sent as `null`, the explicit
// no-limit override.
type spendLimitSetRequest struct {
	Scope  spendLimitScopeAPI `json:"scope"`
	Period string             `json:"period"`
	Amount *string            `json:"amount"`
}

// --- State mapping ---

// stringPtrOrNull maps a nullable API string to a Terraform string.
func stringPtrOrNull(s *string) types.String {
	if s == nil {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

// enterpriseKeyNote is appended to every spend limit schema description.
const enterpriseKeyNote = "Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`): for a Claude Enterprise organization, a **scoped Admin API key** " +
	"created by the primary owner with `read:spend_limits` (reads) and `write:spend_limits` (writes)."
