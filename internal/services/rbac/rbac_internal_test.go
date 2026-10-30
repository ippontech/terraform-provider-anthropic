// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package rbac

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
)

// --- Pagination ---

func TestListAll_followsCursorUntilHasMoreIsFalse(t *testing.T) {
	t.Parallel()

	srv, rec := stub(t, func(req recordedRequest) (int, string) {
		switch req.Query["page"] {
		case nil:
			return http.StatusOK, `{"data":[{"id":"rbac_group_a"}],"has_more":true,"next_page":"cur_2"}`
		default:
			// The reference's own example pairs has_more=false with a non-null
			// next_page: has_more must win.
			return http.StatusOK, `{"data":[{"id":"rbac_group_b"}],"has_more":false,"next_page":"cur_3"}`
		}
	})

	groups, err := listAll[rbacGroupAPIResponse](context.Background(), admintest.NewClient(t, srv), rbacGroupsPath)
	if err != nil {
		t.Fatalf("listAll: %v", err)
	}
	if len(groups) != 2 || groups[0].ID != "rbac_group_a" || groups[1].ID != "rbac_group_b" {
		t.Fatalf("groups = %+v, want rbac_group_a then rbac_group_b", groups)
	}

	reqs := rec.all()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	for _, req := range reqs {
		assertBetaQuery(t, req)
		if req.Method != http.MethodGet || req.Path != rbacGroupsPath {
			t.Errorf("request = %s %s, want GET %s", req.Method, req.Path, rbacGroupsPath)
		}
		if got := req.Query["limit"]; len(got) != 1 || got[0] != "1000" {
			t.Errorf("limit = %v, want [1000]", got)
		}
	}
	if _, ok := reqs[0].Query["page"]; ok {
		t.Errorf("first request must not send page, got %v", reqs[0].Query["page"])
	}
	if got := reqs[1].Query["page"]; len(got) != 1 || got[0] != "cur_2" {
		t.Errorf("second request page = %v, want [cur_2]", got)
	}
}

// The reference lists has_more on both RBAC lists, but a page that left it
// out decoded it as false and ended the walk while next_page still held a
// cursor: a member past the first page dropped from state and was added again
// at every apply. A missing has_more defers to the cursor; an explicit false
// still wins over a stray one (previous test).
func TestListAll_missingHasMoreFollowsTheCursor(t *testing.T) {
	t.Parallel()

	srv, rec := stub(t, func(req recordedRequest) (int, string) {
		if req.Query["page"] == nil {
			return http.StatusOK, `{"data":[{"id":"rbac_group_a"}],"next_page":"cur_2"}`
		}
		return http.StatusOK, `{"data":[{"id":"rbac_group_b"}],"next_page":null}`
	})

	groups, err := listAll[rbacGroupAPIResponse](context.Background(), admintest.NewClient(t, srv), rbacGroupsPath)
	if err != nil {
		t.Fatalf("listAll: %v", err)
	}
	if len(groups) != 2 || len(rec.all()) != 2 {
		t.Errorf("groups = %+v after %d requests, want both pages", groups, len(rec.all()))
	}
}

func TestListAll_stopsOnMissingOrRepeatedCursor(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"null next_page":     `{"data":[],"has_more":true,"next_page":null}`,
		"empty next_page":    `{"data":[],"has_more":true,"next_page":""}`,
		"repeated next_page": `{"data":[],"has_more":true,"next_page":"same"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			srv, rec := stub(t, func(recordedRequest) (int, string) {
				if calls.Add(1) > 3 {
					// Fail the request rather than loop forever.
					return http.StatusBadRequest, `{}`
				}
				return http.StatusOK, body
			})
			if _, err := listAll[rbacGroupAPIResponse](context.Background(), admintest.NewClient(t, srv), rbacGroupsPath); err != nil {
				t.Fatalf("listAll: %v", err)
			}
			want := 1
			if name == "repeated next_page" {
				want = 2 // the first request has no cursor, the second repeats "same"
			}
			if n := len(rec.all()); n != want {
				t.Errorf("requests = %d, want %d", n, want)
			}
		})
	}
}

func TestListAll_surfacesAPIError(t *testing.T) {
	t.Parallel()

	srv, _ := stub(t, func(recordedRequest) (int, string) {
		return http.StatusForbidden, `{"type":"error","error":{"type":"permission_error","message":"missing scope read:rbac_groups"}}`
	})
	_, err := listAll[rbacGroupAPIResponse](context.Background(), admintest.NewClient(t, srv), rbacGroupsPath)
	if err == nil {
		t.Fatal("expected an error")
	}
}

// --- Group mapping and the 2023-06-01 field aliases ---

func decodeGroup(t *testing.T, body string) rbacGroupAPIResponse {
	t.Helper()
	var g rbacGroupAPIResponse
	if err := json.Unmarshal([]byte(body), &g); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return g
}

func roleIDsOf(t *testing.T, m rbacGroupModel) []string {
	t.Helper()
	if m.RoleIDs.IsNull() {
		return nil
	}
	var ids []string
	if diags := m.RoleIDs.ElementsAs(context.Background(), &ids, false); diags.HasError() {
		t.Fatalf("role_ids: %v", diags)
	}
	return ids
}

func TestMapRBACGroup_roleIDs(t *testing.T) {
	t.Parallel()

	const base = `"id":"rbac_group_01","name":"Engineering","source_type":"direct","created_at":"2026-07-09T18:00:00Z","updated_at":"2026-07-10T08:00:00Z"`

	cases := []struct {
		name     string
		body     string
		wantNull bool
		want     []string
	}{
		{
			// The shape 2023-06-01 returns: both fields, same value.
			name: "role_ids and deprecated roles",
			body: `{` + base + `,"role_ids":["rbac_role_a"],"roles":["rbac_role_a"]}`,
			want: []string{"rbac_role_a"},
		},
		{
			name: "role_ids wins over a diverging roles",
			body: `{` + base + `,"role_ids":["rbac_role_new"],"roles":["rbac_role_old"]}`,
			want: []string{"rbac_role_new"},
		},
		{
			name: "role_ids absent falls back to roles",
			body: `{` + base + `,"roles":["rbac_role_a","rbac_role_b"]}`,
			want: []string{"rbac_role_a", "rbac_role_b"},
		},
		{
			// The 2026-10-21 shape.
			name: "role_ids only",
			body: `{` + base + `,"role_ids":["rbac_role_a"]}`,
			want: []string{"rbac_role_a"},
		},
		{
			name: "empty list stays an empty set",
			body: `{` + base + `,"role_ids":[],"roles":[]}`,
			want: []string{},
		},
		{
			// null = role data temporarily unavailable; roles is not consulted.
			name:     "null role_ids is null, not a fallback",
			body:     `{` + base + `,"role_ids":null,"roles":["rbac_role_a"]}`,
			wantNull: true,
		},
		{
			name:     "neither field",
			body:     `{` + base + `}`,
			wantNull: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, diags := mapRBACGroup(decodeGroup(t, tc.body))
			if diags.HasError() {
				t.Fatalf("mapRBACGroup: %v", diags)
			}
			if tc.wantNull {
				if !m.RoleIDs.IsNull() {
					t.Fatalf("role_ids = %v, want null", m.RoleIDs)
				}
				return
			}
			if m.RoleIDs.IsNull() {
				t.Fatal("role_ids is null, want a set")
			}
			got := roleIDsOf(t, m)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("role_ids = %v, want %v", got, tc.want)
			}
			if m.ID.ValueString() != "rbac_group_01" || m.Name.ValueString() != "Engineering" || m.SourceType.ValueString() != "direct" {
				t.Errorf("scalars = %v/%v/%v", m.ID, m.Name, m.SourceType)
			}
			if m.CreatedAt.ValueString() != "2026-07-09T18:00:00Z" || m.UpdatedAt.ValueString() != "2026-07-10T08:00:00Z" {
				t.Errorf("timestamps = %v/%v", m.CreatedAt, m.UpdatedAt)
			}
		})
	}
}

func TestMapRBACGroup_malformedRoleIDs(t *testing.T) {
	t.Parallel()

	_, diags := mapRBACGroup(decodeGroup(t, `{"id":"rbac_group_01","role_ids":{"not":"a list"}}`))
	if !diags.HasError() {
		t.Fatal("expected a parse error for a non-list role_ids")
	}
}
