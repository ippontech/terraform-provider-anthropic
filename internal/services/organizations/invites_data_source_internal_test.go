// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations

import (
	"testing"
)

func TestInvitesDataSource_buildQuery(t *testing.T) {
	t.Run("email, cursor, and statuses set", func(t *testing.T) {
		q := buildInvitesQuery("invite_01A", "jane@example.com", []string{"pending", "expired"})
		if got := q.Get("beta"); got != "true" {
			t.Errorf("beta = %q, want true", got)
		}
		if got := q.Get("limit"); got != "1000" {
			t.Errorf("limit = %q, want 1000", got)
		}
		if got := q.Get("after_id"); got != "invite_01A" {
			t.Errorf("after_id = %q, want invite_01A", got)
		}
		if got := q.Get("email"); got != "jane@example.com" {
			t.Errorf("email = %q, want jane@example.com", got)
		}
		if got := q["statuses"]; len(got) != 2 || got[0] != "pending" || got[1] != "expired" {
			t.Errorf("statuses = %v, want [pending expired]", got)
		}
	})

	t.Run("no email, no cursor, no statuses", func(t *testing.T) {
		q := buildInvitesQuery("", "", nil)
		if got := q.Get("beta"); got != "true" {
			t.Errorf("beta = %q, want true", got)
		}
		if got := q.Get("limit"); got != "1000" {
			t.Errorf("limit = %q, want 1000", got)
		}
		if _, ok := q["after_id"]; ok {
			t.Errorf("after_id should be omitted when empty, got %q", q.Get("after_id"))
		}
		if _, ok := q["email"]; ok {
			t.Errorf("email should be omitted when empty, got %q", q.Get("email"))
		}
		if _, ok := q["statuses"]; ok {
			t.Errorf("statuses should be omitted when empty, got %v", q["statuses"])
		}
	})
}
