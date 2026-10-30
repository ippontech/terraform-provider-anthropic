// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tfvalue

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func stringMap(t *testing.T, m map[string]string) types.Map {
	t.Helper()
	elems := make(map[string]attr.Value, len(m))
	for k, v := range m {
		elems[k] = types.StringValue(v)
	}
	v, d := types.MapValue(types.StringType, elems)
	if d.HasError() {
		t.Fatalf("MapValue: %v", d)
	}
	return v
}

func TestMetadataPatch(t *testing.T) {
	null := types.MapNull(types.StringType)
	unknown := types.MapUnknown(types.StringType)

	tests := []struct {
		name        string
		plan, state types.Map
		want        map[string]any
	}{
		{
			name:  "unchanged keys are resent",
			plan:  stringMap(t, map[string]string{"a": "1"}),
			state: stringMap(t, map[string]string{"a": "1"}),
			want:  map[string]any{"a": "1"},
		},
		{
			name:  "added and changed keys are upserted",
			plan:  stringMap(t, map[string]string{"a": "2", "b": "3"}),
			state: stringMap(t, map[string]string{"a": "1"}),
			want:  map[string]any{"a": "2", "b": "3"},
		},
		{
			name:  "removed key is sent as null",
			plan:  stringMap(t, map[string]string{"a": "1"}),
			state: stringMap(t, map[string]string{"a": "1", "b": "2"}),
			want:  map[string]any{"a": "1", "b": nil},
		},
		{
			name:  "null plan deletes every prior key",
			plan:  null,
			state: stringMap(t, map[string]string{"a": "1", "b": "2"}),
			want:  map[string]any{"a": nil, "b": nil},
		},
		{
			name:  "empty plan deletes every prior key",
			plan:  stringMap(t, map[string]string{}),
			state: stringMap(t, map[string]string{"a": "1"}),
			want:  map[string]any{"a": nil},
		},
		{
			name:  "null plan and null state change nothing",
			plan:  null,
			state: null,
			want:  map[string]any{},
		},
		{
			name:  "first metadata on a resource without any",
			plan:  stringMap(t, map[string]string{"a": "1"}),
			state: null,
			want:  map[string]any{"a": "1"},
		},
		{
			name:  "unknown values are ignored",
			plan:  unknown,
			state: unknown,
			want:  map[string]any{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, diags := MetadataPatch(context.Background(), tc.plan, tc.state)
			if diags.HasError() {
				t.Fatalf("diags: %v", diags)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("MetadataPatch = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestMetadataAfterEmptyResponse(t *testing.T) {
	null := types.MapNull(types.StringType)
	empty := stringMap(t, map[string]string{})
	for name, tc := range map[string]struct{ current, want types.Map }{
		"null stays null":             {null, null},
		"unknown becomes null":        {types.MapUnknown(types.StringType), null},
		"keys removed out of band":    {stringMap(t, map[string]string{"team": "a"}), null},
		"metadata = {} is kept as {}": {empty, empty},
	} {
		t.Run(name, func(t *testing.T) {
			if got := MetadataAfterEmptyResponse(tc.current); !got.Equal(tc.want) {
				t.Errorf("MetadataAfterEmptyResponse(%v) = %v, want %v", tc.current, got, tc.want)
			}
		})
	}
}
