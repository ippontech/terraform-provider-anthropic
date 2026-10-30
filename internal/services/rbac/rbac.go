// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package rbac holds the Claude Enterprise RBAC resources and data sources:
// groups and group members. They go through the Admin API client
// (`pd.AdminClient`) because the pinned SDK v1.67.0 predates
// `client.Beta.Organization.RBACGroups` (added in v1.77.0).
package rbac

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

const (
	rbacGroupsPath = "/v1/organizations/rbac_groups"

	// listPageSize is the largest page the RBAC list endpoints accept.
	listPageSize = "1000"

	// sourceTypeSCIM marks a group provisioned by the identity provider: the
	// API rejects renaming or deleting it, and changing its membership, while
	// SCIM provisioning is on.
	sourceTypeSCIM = "scim"
)

// rbacURL appends the query to an RBAC path, always with `beta=true`: the SDK
// (v1.77.0+) sends it on every RBAC call, and the invite endpoints, which the
// same guide documents without it, turned out to require it.
func rbacURL(path string, query url.Values) string {
	if query == nil {
		query = url.Values{}
	}
	query.Set("beta", "true")
	return path + "?" + query.Encode()
}

func rbacGroupPath(groupID string) string {
	return rbacGroupsPath + "/" + url.PathEscape(groupID)
}

func rbacGroupMembersPath(groupID string) string {
	return rbacGroupPath(groupID) + "/members"
}

func rbacGroupMemberPath(groupID, userID string) string {
	return rbacGroupMembersPath(groupID) + "/" + url.PathEscape(userID)
}

// --- API response types ---

// rbacGroupAPIResponse is a BetaRBACGroup. Under `anthropic-version:
// 2023-06-01`, the version the provider sends, the API returns both
// `role_ids` and its deprecated alias `roles`; 2026-10-21 drops `roles`.
// Both are kept raw so an absent field can be told apart from a JSON null,
// which the API sends when role data was temporarily unavailable.
type rbacGroupAPIResponse struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	SourceType string          `json:"source_type"`
	RoleIDs    json.RawMessage `json:"role_ids"`
	Roles      json.RawMessage `json:"roles"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
}

// rbacGroupMemberAPIResponse is a BetaRBACGroupMember. The group ID it echoes
// (`rbac_group_id`, plus the deprecated `group_id` under 2023-06-01) is not
// decoded: callers already hold it as the request path.
type rbacGroupMemberAPIResponse struct {
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	CreatedAt string `json:"created_at"`
}

// --- Pagination ---

// cursorPage is the envelope of the RBAC list endpoints, which page with an
// opaque cursor (`page` / `next_page`) rather than the `after_id` the member
// and invite lists use. HasMore is a pointer so that a page without the field
// is told apart from `has_more: false`.
type cursorPage[T any] struct {
	Data     []T     `json:"data"`
	HasMore  *bool   `json:"has_more"`
	NextPage *string `json:"next_page"`
}

// listPages walks every page of an RBAC list endpoint, handing each page's
// items to visit until visit returns false or the pages run out. A page can
// hold fewer than `limit` items while `has_more` is true (the members list
// filters out users who left), so only `has_more` and the cursor end the
// loop: an explicit `has_more: false` wins over a stray cursor (the
// reference's own example pairs them), and a page without `has_more` defers
// to the cursor. A missing or repeated cursor also ends it rather than
// looping forever.
func listPages[T any](ctx context.Context, client *admin.Client, path string, visit func([]T) bool) error {
	cursor := ""
	for {
		query := url.Values{}
		query.Set("limit", listPageSize)
		if cursor != "" {
			query.Set("page", cursor)
		}

		body, err := client.DoRequest(ctx, "GET", rbacURL(path, query), nil)
		if err != nil {
			return err
		}

		var page cursorPage[T]
		if err := json.Unmarshal(body, &page); err != nil {
			return fmt.Errorf("parse list response: %w", err)
		}

		if !visit(page.Data) {
			return nil
		}
		if (page.HasMore != nil && !*page.HasMore) || page.NextPage == nil || *page.NextPage == "" || *page.NextPage == cursor {
			return nil
		}
		cursor = *page.NextPage
	}
}

// listAll returns every item of an RBAC list endpoint.
func listAll[T any](ctx context.Context, client *admin.Client, path string) ([]T, error) {
	var all []T
	err := listPages(ctx, client, path, func(items []T) bool {
		all = append(all, items...)
		return true
	})
	return all, err
}

// getJSON fetches one RBAC object.
func getJSON[T any](ctx context.Context, client *admin.Client, path string) (T, error) {
	var out T
	body, err := client.DoRequest(ctx, "GET", rbacURL(path, nil), nil)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("parse response: %w", err)
	}
	return out, nil
}

// --- State mapping ---

// rbacGroupModel holds the group attributes shared by the resource and both
// group data sources.
type rbacGroupModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	SourceType types.String `tfsdk:"source_type"`
	RoleIDs    types.Set    `tfsdk:"role_ids"`
	CreatedAt  types.String `tfsdk:"created_at"`
	UpdatedAt  types.String `tfsdk:"updated_at"`
}

var rbacGroupAttrTypes = map[string]attr.Type{
	"id":          types.StringType,
	"name":        types.StringType,
	"source_type": types.StringType,
	"role_ids":    types.SetType{ElemType: types.StringType},
	"created_at":  types.StringType,
	"updated_at":  types.StringType,
}

// mapRBACGroup maps an API group to its Terraform model. role_ids comes from
// `role_ids`, or from the deprecated `roles` when `role_ids` is absent; it is
// null when the API sent null (role data temporarily unavailable).
func mapRBACGroup(g rbacGroupAPIResponse) (rbacGroupModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	raw := g.RoleIDs
	if len(raw) == 0 {
		raw = g.Roles
	}
	roleIDs := types.SetNull(types.StringType)
	if len(raw) > 0 && string(raw) != "null" {
		var ids []string
		if err := json.Unmarshal(raw, &ids); err != nil {
			diags.AddError("Parse Error", fmt.Sprintf("Unable to parse the role IDs of RBAC group %s: %s", g.ID, err))
			return rbacGroupModel{}, diags
		}
		values := make([]attr.Value, len(ids))
		for i, id := range ids {
			values[i] = types.StringValue(id)
		}
		var d diag.Diagnostics
		roleIDs, d = types.SetValue(types.StringType, values)
		diags.Append(d...)
	}

	return rbacGroupModel{
		ID:         types.StringValue(g.ID),
		Name:       types.StringValue(g.Name),
		SourceType: tfvalue.StringOrNull(g.SourceType),
		RoleIDs:    roleIDs,
		CreatedAt:  tfvalue.StringOrNull(g.CreatedAt),
		UpdatedAt:  tfvalue.StringOrNull(g.UpdatedAt),
	}, diags
}

func (m rbacGroupModel) attrValues() map[string]attr.Value {
	return map[string]attr.Value{
		"id":          m.ID,
		"name":        m.Name,
		"source_type": m.SourceType,
		"role_ids":    m.RoleIDs,
		"created_at":  m.CreatedAt,
		"updated_at":  m.UpdatedAt,
	}
}

// enterpriseKeyNote is appended to every RBAC schema description.
const enterpriseKeyNote = "Claude Enterprise only: requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) set to a Claude Enterprise **scoped Admin API key**, " +
	"created by the primary owner; a Claude Console Admin API key is rejected."
