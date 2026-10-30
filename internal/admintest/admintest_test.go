// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package admintest

import (
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

func TestListPage(t *testing.T) {
	type user struct {
		ID string `json:"id"`
	}
	for _, tc := range []struct {
		got, want string
	}{
		{ListPage([]user{{"u1"}, {"u2"}}, true, "u1", "u2"), `{"data":[{"id":"u1"},{"id":"u2"}],"has_more":true,"first_id":"u1","last_id":"u2"}`},
		{ListPage[user](nil, false, "", ""), `{"data":[],"has_more":false,"first_id":null,"last_id":null}`},
	} {
		if diff := sdktest.JSONDiff(tc.got, tc.want); diff != "" {
			t.Errorf("ListPage:\n%s", diff)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("ListPage accepted a value JSON cannot encode")
		}
	}()
	ListPage([]any{make(chan int)}, false, "", "")
}
