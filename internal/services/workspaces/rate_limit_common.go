// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// The pagination of the workspace rate-limit list endpoint and the group
// object every rate-limit entry carries. The API added group next to
// group_type, which it deprecates (always equal to group.type) and removes in
// API version 2026-10-21; internal/admin pins 2023-06-01, which returns both.

// rateLimitPage is one page of the rate-limit list endpoint. It paginates with
// an opaque page/next_page cursor, not the after_id/has_more pair the other
// Admin API lists use.
type rateLimitPage[T any] struct {
	Data     []T     `json:"data"`
	NextPage *string `json:"next_page"`
}

// listRateLimitPages fetches every page of the rate-limit list at path.
// filters carries the endpoint's filters; the page size and the cursor are set
// here, and next_page is followed until the API returns null. A next_page
// already sent during this listing is an error: following it would loop.
func listRateLimitPages[T any](ctx context.Context, client *admin.Client, path string, filters url.Values) ([]T, error) {
	var all []T
	cursor := ""
	sent := map[string]bool{}
	for {
		sent[cursor] = true
		query := maps.Clone(filters)
		if query == nil {
			query = url.Values{}
		}
		query.Set("limit", "1000")
		if cursor != "" {
			query.Set("page", cursor)
		}

		body, err := client.DoRequest(ctx, "GET", path+"?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}

		var page rateLimitPage[T]
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parse rate limits response: %w", err)
		}
		all = append(all, page.Data...)

		if page.NextPage == nil || *page.NextPage == "" {
			return all, nil
		}
		if sent[*page.NextPage] {
			return nil, fmt.Errorf("rate limits list returned next_page %q, a cursor already sent: stopping instead of looping", *page.NextPage)
		}
		cursor = *page.NextPage
	}
}

type rateLimitGroupAPI struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
}

var rateLimitGroupAttrTypes = map[string]attr.Type{
	"id":           types.StringType,
	"type":         types.StringType,
	"display_name": types.StringType,
}

// rateLimitGroupTypes are the values of group.type, and of the group_type
// filter the list endpoint accepts.
const rateLimitGroupTypes = "`model_group`, `batch`, `token_count`, `files`, `skills`, `web_search`"

const rateLimitGroupDescription = "The rate-limit group the entry applies to: a model family (`model_group`) or an API surface."

const rateLimitGroupTypeDeprecation = "Use group.type instead. group_type always equals group.type, " +
	"and Admin API version 2026-10-21 removes it."

const rateLimitGroupTypeDescription = "Use `group.type` instead. Rate-limit group type, always equal to `group.type`. " +
	"Admin API version 2026-10-21 removes this field."

// rateLimitGroupValue maps the API group object. A response without one (an
// older payload) maps to null rather than to an object of empty strings.
func rateLimitGroupValue(g *rateLimitGroupAPI) (types.Object, diag.Diagnostics) {
	if g == nil {
		return types.ObjectNull(rateLimitGroupAttrTypes), nil
	}
	return types.ObjectValue(rateLimitGroupAttrTypes, map[string]attr.Value{
		"id":           tfvalue.StringOrNull(g.ID),
		"type":         tfvalue.StringOrNull(g.Type),
		"display_name": tfvalue.StringOrNull(g.DisplayName),
	})
}

// rateLimitModelsValue maps an entry's models, null for non-model groups.
func rateLimitModelsValue(ctx context.Context, models []string) (types.List, diag.Diagnostics) {
	if len(models) == 0 {
		return types.ListNull(types.StringType), nil
	}
	return types.ListValueFrom(ctx, types.StringType, models)
}

func rateLimitGroupDataSourceAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Computed:            true,
		MarkdownDescription: rateLimitGroupDescription,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Identifier of the rate-limit group (`rlg_...`), the same in every organization. " +
					"Not the entry's own ID.",
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Group type: one of " + rateLimitGroupTypes + ".",
			},
			"display_name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Human-readable name of a model group (e.g. `Claude Sonnet 4.x`), for display only. Null for other group types.",
			},
		},
	}
}
