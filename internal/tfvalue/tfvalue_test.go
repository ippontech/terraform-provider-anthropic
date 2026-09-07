// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tfvalue

import (
	"testing"
	"time"
)

func TestStringOrNull(t *testing.T) {
	if v := StringOrNull(""); !v.IsNull() {
		t.Errorf("StringOrNull(%q) = %v, want null", "", v)
	}
	if v := StringOrNull("user_01ABC"); v.ValueString() != "user_01ABC" {
		t.Errorf("StringOrNull(user_01ABC) = %v, want user_01ABC", v)
	}
}

func TestTimeOrNull(t *testing.T) {
	if v := TimeOrNull(time.Time{}); !v.IsNull() {
		t.Errorf("TimeOrNull(zero) = %v, want null", v)
	}
	ts := time.Date(2026, 9, 1, 10, 30, 0, 0, time.UTC)
	if v := TimeOrNull(ts); v.ValueString() != "2026-09-01T10:30:00Z" {
		t.Errorf("TimeOrNull(%v) = %v, want 2026-09-01T10:30:00Z", ts, v)
	}
}
