// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

// TestVaultCredentialConfigValidator_EnvironmentVariableNetworkingNotSet: the
// limited-networking check reads the networking block only when it is set.
// An unknown block (an unresolved reference) raises nothing, and a null one
// only the missing-attribute diagnostic of the field check.
func TestVaultCredentialConfigValidator_EnvironmentVariableNetworkingNotSet(t *testing.T) {
	t.Parallel()
	networkingType := schematest.ResourceObjectType(t, NewVaultCredentialResource()).AttributeTypes["networking"]
	for name, tc := range map[string]struct {
		networking tftypes.Value
		want       []string
	}{
		"unknown": {networking: tftypes.NewValue(networkingType, tftypes.UnknownValue)},
		"null": {
			networking: tftypes.NewValue(networkingType, nil),
			want:       []string{`"networking" is required when type is "environment_variable".`},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			vals := schematest.NullValues(t, NewVaultCredentialResource())
			vals["type"] = tftypes.NewValue(tftypes.String, "environment_variable")
			vals["secret_name"] = tftypes.NewValue(tftypes.String, "MY_KEY")
			vals["secret_value"] = tftypes.NewValue(tftypes.String, "my-secret")
			vals["token_wo_version"] = tftypes.NewValue(tftypes.Number, 1)
			vals["networking"] = tc.networking

			resp := validateConfig(t, vals)
			var got []string
			for _, d := range resp.Diagnostics {
				got = append(got, d.Detail())
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("diagnostics = %q, want %q", got, tc.want)
			}
		})
	}
}
