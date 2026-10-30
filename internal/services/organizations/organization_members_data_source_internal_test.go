// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations

import (
	"testing"
)

func TestOrganizationMembersDataSource_buildQuery(t *testing.T) {
	t.Run("email and cursor set", func(t *testing.T) {
		q := buildOrganizationMembersQuery("user_01A", "jane@example.com")
		if got := q.Get("limit"); got != "1000" {
			t.Errorf("limit = %q, want 1000", got)
		}
		if got := q.Get("after_id"); got != "user_01A" {
			t.Errorf("after_id = %q, want user_01A", got)
		}
		if got := q.Get("email"); got != "jane@example.com" {
			t.Errorf("email = %q, want jane@example.com", got)
		}
	})

	t.Run("no email, no cursor", func(t *testing.T) {
		q := buildOrganizationMembersQuery("", "")
		if got := q.Get("limit"); got != "1000" {
			t.Errorf("limit = %q, want 1000", got)
		}
		if _, ok := q["after_id"]; ok {
			t.Errorf("after_id should be omitted when empty, got %q", q.Get("after_id"))
		}
		if _, ok := q["email"]; ok {
			t.Errorf("email should be omitted when empty, got %q", q.Get("email"))
		}
	})
}
