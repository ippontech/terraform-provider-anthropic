// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package tfvalue maps zero-valued Anthropic API response fields to null
// Terraform values — the convention every resource's state mapping follows,
// so refresh never writes "" or the zero time into state where the API means
// "absent".
package tfvalue

import (
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// StringOrNull maps the API's "" (the Go zero value the SDK decodes an absent
// string field to, e.g. archived_by_actor_id on a live resource) to null.
func StringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// TimeOrNull formats a timestamp as RFC 3339, mapping the zero time (an
// absent field, e.g. archived_at on a live resource) to null.
func TimeOrNull(t time.Time) types.String {
	if t.IsZero() {
		return types.StringNull()
	}
	return types.StringValue(t.Format(time.RFC3339))
}
