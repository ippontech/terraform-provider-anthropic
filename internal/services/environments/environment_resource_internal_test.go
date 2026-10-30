// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// list is a known list of strings.
func list(values ...string) types.List {
	elems := make([]attr.Value, len(values))
	for i, v := range values {
		elems[i] = types.StringValue(v)
	}
	return types.ListValueMust(types.StringType, elems)
}

var (
	nullList    = types.ListNull(types.StringType)
	unknownList = types.ListUnknown(types.StringType)
	// listWithUnknown cannot be read into []string.
	listWithUnknown = types.ListValueMust(types.StringType, []attr.Value{types.StringUnknown()})
)

func networking(typ string, mcp, packageManagers types.Bool, hosts types.List) types.Object {
	return types.ObjectValueMust(environmentNetworkingAttrTypes, map[string]attr.Value{
		"type":                   types.StringValue(typ),
		"allow_mcp_servers":      mcp,
		"allow_package_managers": packageManagers,
		"allowed_hosts":          hosts,
	})
}

// packagesObject builds a packages object; a manager it does not name is
// null.
func packagesObject(lists map[string]types.List) types.Object {
	values := map[string]attr.Value{}
	for name := range environmentPackagesAttrTypes {
		values[name] = nullList
		if l, ok := lists[name]; ok {
			values[name] = l
		}
	}
	return types.ObjectValueMust(environmentPackagesAttrTypes, values)
}

func configObject(net, pkgs types.Object) types.Object {
	return types.ObjectValueMust(environmentConfigAttrTypes, map[string]attr.Value{
		"type":       types.StringUnknown(),
		"networking": net,
		"packages":   pkgs,
	})
}

var (
	nullNetworking    = types.ObjectNull(environmentNetworkingAttrTypes)
	unknownNetworking = types.ObjectUnknown(environmentNetworkingAttrTypes)
	nullPackages      = types.ObjectNull(environmentPackagesAttrTypes)
	unknownPackages   = types.ObjectUnknown(environmentPackagesAttrTypes)
)

// TestBuildEnvironmentConfigParams pins the request body built from a
// planned config: null and unknown values are left out (the API keeps or
// defaults them), an unrestricted network sends its type alone, a limited
// network every known field, packages every known list, empty ones as [].
func TestBuildEnvironmentConfigParams(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		config types.Object
		want   string
	}{
		"null networking and packages": {
			config: configObject(nullNetworking, nullPackages),
			want:   `{"type":"cloud"}`,
		},
		"unknown networking and packages": {
			config: configObject(unknownNetworking, unknownPackages),
			want:   `{"type":"cloud"}`,
		},
		"unrestricted ignores the limited fields": {
			config: configObject(networking("unrestricted", types.BoolValue(true), types.BoolValue(true), list("h")), nullPackages),
			want:   `{"type":"cloud","networking":{"type":"unrestricted"}}`,
		},
		"limited with every field": {
			config: configObject(networking("limited", types.BoolValue(true), types.BoolValue(false), list("a.example.com", "b.example.com")), nullPackages),
			want: `{"type":"cloud","networking":{"type":"limited","allow_mcp_servers":true,"allow_package_managers":false,` +
				`"allowed_hosts":["a.example.com","b.example.com"]}}`,
		},
		"limited with the other boolean set": {
			config: configObject(networking("limited", types.BoolValue(false), types.BoolValue(true), nullList), nullPackages),
			want:   `{"type":"cloud","networking":{"type":"limited","allow_mcp_servers":false,"allow_package_managers":true}}`,
		},
		"limited with null and unknown fields": {
			config: configObject(networking("limited", types.BoolNull(), types.BoolUnknown(), unknownList), unknownPackages),
			want:   `{"type":"cloud","networking":{"type":"limited"}}`,
		},
		"every package manager": {
			config: configObject(nullNetworking, packagesObject(map[string]types.List{
				"apt": list("apt1"), "cargo": list("cargo1"), "gem": list("gem1"),
				"go": list("go1"), "npm": list("npm1", "npm2"), "pip": list("pip1"),
			})),
			want: `{"type":"cloud","packages":{"apt":["apt1"],"cargo":["cargo1"],"gem":["gem1"],` +
				`"go":["go1"],"npm":["npm1","npm2"],"pip":["pip1"]}}`,
		},
		"empty, null and unknown package lists": {
			config: configObject(nullNetworking, packagesObject(map[string]types.List{
				"apt": list(), "cargo": unknownList, "pip": list("pip1"),
			})),
			want: `{"type":"cloud","packages":{"apt":[],"pip":["pip1"]}}`,
		},
		"no known package list": {
			config: configObject(nullNetworking, packagesObject(map[string]types.List{"go": unknownList})),
			want:   `{"type":"cloud"}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			params, diags := buildEnvironmentConfigParams(context.Background(), tc.config)
			if diags.HasError() {
				t.Fatalf("diagnostics: %v", diags)
			}
			got, err := json.Marshal(params)
			if err != nil {
				t.Fatal(err)
			}
			if diff := sdktest.JSONDiff(string(got), tc.want); diff != "" {
				t.Errorf("params %s differ from %s:\n%s", got, tc.want, diff)
			}
		})
	}
}

// TestBuildEnvironmentConfigParams_errors: a value that cannot be read into
// the model is an error diagnostic and no params, whichever part holds it.
func TestBuildEnvironmentConfigParams_errors(t *testing.T) {
	t.Parallel()
	wrongObject := types.ObjectValueMust(map[string]attr.Type{"other": types.StringType}, map[string]attr.Value{"other": types.StringValue("x")})
	cases := map[string]types.Object{
		"config":        wrongObject,
		"allowed_hosts": configObject(networking("limited", types.BoolNull(), types.BoolNull(), listWithUnknown), nullPackages),
	}
	for name := range environmentPackagesAttrTypes {
		cases[name] = configObject(nullNetworking, packagesObject(map[string]types.List{name: listWithUnknown}))
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			params, diags := buildEnvironmentConfigParams(context.Background(), config)
			if !diags.HasError() {
				t.Fatalf("no error diagnostic for %s", config)
			}
			if got, _ := json.Marshal(params); string(got) != `{"type":"cloud"}` {
				t.Errorf("params on error = %s, want the zero value", got)
			}
		})
	}
}
