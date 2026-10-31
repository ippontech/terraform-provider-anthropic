// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type skillListItem struct {
	ID            types.String `tfsdk:"id"`
	DisplayTitle  types.String `tfsdk:"display_title"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
	LatestVersion types.String `tfsdk:"latest_version"`
	Source        types.String `tfsdk:"source"`
	Type          types.String `tfsdk:"type"`
}

// The list is read across pages (the next page keeps the beta header and
// ?beta=true) and each item is decoded from its beta-shaped raw JSON.
func TestSkillsDataSourceRead_betaShapeAcrossPages(t *testing.T) {
	t.Parallel()
	srv := newBetaSkillsServer(t, map[string]http.HandlerFunc{
		"GET /v1/skills": func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("source"); got != "custom" {
				t.Errorf("source query = %q, want custom", got)
			}
			if r.URL.Query().Get("page") == "page_2" {
				jsonResponse(`{"data":[{"id":"skill_2","created_at":"2025-10-04T00:00:00Z","display_title":"","latest_version":"1759178010641200",
					"source":"custom","type":"skill","updated_at":"2025-10-04T00:00:00Z"}],"has_more":false,"next_page":null}`)(w, r)
				return
			}
			jsonResponse(`{"data":[`+betaSkillJSON+`],"has_more":true,"next_page":"page_2"}`)(w, r)
		},
	})
	state := readDataSource(t, &SkillsDataSource{client: srv.client(t)}, map[string]tftypes.Value{
		"source_filter": tfString("custom"),
	})
	var items []skillListItem
	if d := state.GetAttribute(context.Background(), path.Root("skills"), &items); d.HasError() {
		t.Fatalf("skills: %v", d)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 skills over 2 pages, got %d (calls %q)", len(items), srv.Calls())
	}
	first := items[0]
	if first.ID.ValueString() != fixtureSkillID || first.DisplayTitle.ValueString() != "Test Skill" ||
		first.CreatedAt.ValueString() != fixtureSkillCreated || first.UpdatedAt.ValueString() != fixtureSkillUpdated ||
		first.LatestVersion.ValueString() != fixtureSkillVersion || first.Source.ValueString() != "custom" ||
		first.Type.ValueString() != "skill" {
		t.Errorf("first item = %+v", first)
	}
	if !items[1].DisplayTitle.IsNull() || items[1].LatestVersion.ValueString() != "1759178010641200" {
		t.Errorf("second item = %+v (empty display_title must read as null)", items[1])
	}
}
