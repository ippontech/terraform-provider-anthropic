// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package platform

import (
	"encoding/base64"
	"net/url"
	"os"

	"github.com/anthropics/anthropic-sdk-go/option"
)

// BaseURLWithoutUserInfo resolves the base URL of an SDK client (a gateway
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
// signature, set later by the SDK's middleware, still replaces it. Without it,
// the information is dropped, as net/http sent it on none of these requests:
// a first-party bearer token sets the Authorization header itself, and the
// Google and federation middlewares set their token before net/http looks
// but stand down when they find an Authorization header already set, so a
// Basic one would replace the token.
//
// The URL is returned explicitly, so the SDK does not read envName again.
func BaseURLWithoutUserInfo(value, envName string, basic bool) (string, []option.RequestOption) {
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
