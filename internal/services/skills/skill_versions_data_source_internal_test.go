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

type skillVersionListItem struct {
	ID          types.String `tfsdk:"id"`
	Version     types.String `tfsdk:"version"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Directory   types.String `tfsdk:"directory"`
	CreatedAt   types.String `tfsdk:"created_at"`
	Type        types.String `tfsdk:"type"`
}

func TestSkillVersionsDataSourceRead_betaShape(t *testing.T) {
	t.Parallel()
	srv := newBetaSkillsServer(t, map[string]http.HandlerFunc{
		"GET /v1/skills/" + fixtureSkillID + "/versions": jsonResponse(`{"data":[` + betaSkillVersionJSON + `],"has_more":false,"next_page":null}`),
	})
	state := readDataSource(t, &SkillVersionsDataSource{client: srv.client(t)}, map[string]tftypes.Value{
		"skill_id": tfString(fixtureSkillID),
	})
	var items []skillVersionListItem
	if d := state.GetAttribute(context.Background(), path.Root("versions"), &items); d.HasError() {
		t.Fatalf("versions: %v", d)
	}
	want := skillVersionListItem{
		ID:          types.StringValue(fixtureVersionID),
		Version:     types.StringValue(fixtureSkillVersion),
		Name:        types.StringValue("test-skill"),
		Description: types.StringValue("For testing."),
		Directory:   types.StringValue("test-skill"),
		CreatedAt:   types.StringValue(fixtureVersionCreate),
		Type:        types.StringValue("skill_version"),
	}
	if len(items) != 1 || items[0] != want {
		t.Fatalf("versions = %+v, want [%+v]", items, want)
	}
}
