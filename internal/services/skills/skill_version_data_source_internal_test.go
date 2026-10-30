// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills

import (
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestSkillVersionDataSourceRead_betaShape(t *testing.T) {
	t.Parallel()
	srv := newBetaSkillsServer(t, map[string]http.HandlerFunc{
		"GET /v1/skills/" + fixtureSkillID + "/versions/" + fixtureSkillVersion: jsonResponse(betaSkillVersionJSON),
	})
	state := readDataSource(t, &SkillVersionDataSource{client: srv.client(t)}, map[string]tftypes.Value{
		"skill_id": tfString(fixtureSkillID),
		"version":  tfString(fixtureSkillVersion),
	})
	want := betaSkillVersionState()
	want["type"] = new("skill_version")
	assertStringAttrs(t, state, want)
}
