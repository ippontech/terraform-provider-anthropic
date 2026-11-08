// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"

	"github.com/anthropics/anthropic-sdk-go/option"
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

// baseURLWithoutUserInfo resolves the base URL of an SDK client (a gateway
// platform, or first party with ANTHROPIC_BASE_URL), value or
// else envName, and takes its user information (user:password@ before the
// host) out of it.
//
// The SDK builds every request URL from the base URL and prints that URL in
// its errors (*anthropic.Error), which resources paste into diagnostics: a
// password left in it would reach the plan output and CI logs.
//
// net/http sends the user information as an Authorization: Basic header, but
// only on a request that has no Authorization header when it is sent. With
// basic set (AWS, a first-party API key), the information becomes that
// header, so a reverse proxy in front of the API still gets it, and a SigV4
// signature, set later by the SDK's middleware, still replaces it. Without it
// (Google Cloud, a first-party bearer token or federation), the information
// is dropped: the SDK sets the token before net/http looks, and its Google
// and federation middlewares stand down when they find an Authorization
// header, so a Basic one would replace the token.
//
// The URL is returned explicitly, so the SDK does not read envName again.
func baseURLWithoutUserInfo(value, envName string, basic bool) (string, []option.RequestOption) {
	if value == "" {
		value = os.Getenv(envName)
	}
	if value == "" {
		return "", nil
	}
	u, err := url.Parse(value)
	if err != nil || u.User == nil {
		return value, nil
	}
	password, _ := u.User.Password()
	credentials := base64.StdEncoding.EncodeToString([]byte(u.User.Username() + ":" + password))
	u.User = nil
	if !basic {
		return u.String(), nil
	}
	return u.String(), []option.RequestOption{option.WithHeader("Authorization", "Basic "+credentials)}
}
