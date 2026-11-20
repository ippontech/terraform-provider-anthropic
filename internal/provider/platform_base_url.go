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

// checkBaseURL refuses the base URL of an SDK client, configured (value) or
// else read from envName, when it does not parse. The SDK would
// refuse it too, with a parse error that quotes the URL, user information
// included: this message names the attribute or the variable and repeats
// neither.
func checkBaseURL(attr path.Path, name, value, envName string) diag.Diagnostics {
	var diags diag.Diagnostics
	source := name
	if value == "" {
		value = os.Getenv(envName)
		source = "The " + envName + " environment variable"
	}
	if value == "" {
		return diags
	}
	if _, err := url.Parse(value); err != nil {
		diags.AddAttributeError(attr, "Invalid Base URL", fmt.Sprintf("%s is not a valid URL.", source))
	}
	return diags
}
