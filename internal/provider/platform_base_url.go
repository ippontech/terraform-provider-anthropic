// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/url"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
)

// The variables the SDK's platform clients read when base_url is unset.
const (
	awsBaseURLEnv         = "ANTHROPIC_AWS_BASE_URL"
	googleCloudBaseURLEnv = "ANTHROPIC_GOOGLE_CLOUD_BASE_URL"
)

// checkGatewayBaseURL refuses the base URL of a gateway platform, configured
// (value) or else read from envName, when it does not parse or when it
// carries user information (user:password@ before the host).
//
// The SDK builds every request URL from the base URL, user information
// included, and prints that URL in its errors (*anthropic.Error), which
// resources paste into diagnostics: a password there would reach the plan
// output and the CI logs. The value itself is never repeated, for the same
// reason, nor is the parse error, which quotes it.
func checkGatewayBaseURL(attr path.Path, name, value, envName string) diag.Diagnostics {
	var diags diag.Diagnostics
	source := name
	if value == "" {
		value = os.Getenv(envName)
		source = "The " + envName + " environment variable"
	}
	if value == "" {
		return diags
	}
	u, err := url.Parse(value)
	switch {
	case err != nil:
		diags.AddAttributeError(attr, "Invalid Base URL", fmt.Sprintf("%s is not a valid URL.", source))
	case u.User != nil:
		diags.AddAttributeError(attr, "Invalid Base URL",
			fmt.Sprintf("%s carries user information (user:password@ before the host). Error messages repeat the "+
				"request URL, so the password would end up in the plan output and in CI logs. Remove it: the gateway "+
				"authenticates through the block's own credentials.", source))
	}
	return diags
}
