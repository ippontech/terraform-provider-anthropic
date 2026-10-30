// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// namedString is one string attribute of a platform block, with its name.
type namedString struct {
	name  string
	value types.String
}

// firstPartyConflicts reports every first-party credential written in the
// configuration of an alias whose platform block, block, is set; reaches
// says what the block reaches and with which credentials. It reads the
// configured values, not the resolved ones: an Anthropic credential written
// in the configuration is a mistake to report, even when unknown at plan
// time, while the same credential exported in the environment is ignored
// (see ignoredFirstPartyEnvironment).
func firstPartyConflicts(data AnthropicProviderModel, block, reaches string) diag.Diagnostics {
	var diags diag.Diagnostics
	for _, other := range []struct {
		name  string
		value attr.Value
	}{
		{"api_key", data.ApiKey},
		{"admin_api_key", data.AdminApiKey},
		{"auth_token", data.AuthToken},
		{"federation", data.Federation},
	} {
		if !other.value.IsNull() {
			diags.AddAttributeError(path.Root(block), "Conflicting Platform Configuration",
				fmt.Sprintf("%s and %s cannot both be set: the %s block reaches %s, where no Anthropic credential "+
					"is accepted. Use a separate provider alias for each organization.", block, other.name, block, reaches))
		}
	}
	return diags
}

// unknownBlockAttributes reports, under summary, every attribute of the
// platform block block that is unknown at plan time, in the order of attrs.
func unknownBlockAttributes(block, summary string, attrs []namedString) diag.Diagnostics {
	var diags diag.Diagnostics
	for _, a := range attrs {
		if a.value.IsUnknown() {
			diags.AddAttributeError(path.Root(block).AtName(a.name), summary,
				fmt.Sprintf("%s.%s depends on a value that is not known until apply. Provider configuration must be known at plan time.", block, a.name))
		}
	}
	return diags
}
