// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package admin

import (
	"strings"
	"testing"
)

func TestNextAfterID(t *testing.T) {
	for _, tc := range []struct {
		name, afterID, lastID, want string
		wantErr                     bool
	}{
		{name: "first page", afterID: "", lastID: "id_1", want: "id_1"},
		{name: "next page", afterID: "id_1", lastID: "id_2", want: "id_2"},
		{name: "null last_id", afterID: "id_1", lastID: "", wantErr: true},
		{name: "null last_id on the first page", afterID: "", lastID: "", wantErr: true},
		{name: "the cursor just sent", afterID: "id_1", lastID: "id_1", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NextAfterID(tc.afterID, tc.lastID)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "has_more") || got != "" {
					t.Fatalf("NextAfterID(%q, %q) = %q, %v; want an error mentioning has_more", tc.afterID, tc.lastID, got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("NextAfterID(%q, %q) = %q, %v; want %q", tc.afterID, tc.lastID, got, err, tc.want)
			}
		})
	}
}
