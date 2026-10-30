// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package oauthtest provides shared test helpers for constructing an OAuth
// bearer SDK client that targets a local httptest server instead of the real
// API. It is the OAuth counterpart of internal/admintest.
package oauthtest

import (
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// NewSDKClient returns a bare *anthropic.Client authenticated with a dummy
// bearer token (sdktest.TestAuthToken) and pointed at srv. Use it when a test
// drives the SDK directly (e.g. to reproduce a 404 or a paginated list)
// without going through a resource or data source. Extra options are applied
// after the defaults, so a test that counts requests can pass
// option.WithMaxRetries(0) to keep the SDK's own retries out of the count.
func NewSDKClient(t *testing.T, srv *httptest.Server, opts ...option.RequestOption) *anthropic.Client {
	t.Helper()
	return sdktest.NewAuthTokenClient(t, srv, opts...)
}

// NewClient wraps NewSDKClient in the *providerdata.OAuthClient the
// resources and data sources expect from Configure.
func NewClient(t *testing.T, srv *httptest.Server, opts ...option.RequestOption) *providerdata.OAuthClient {
	t.Helper()
	return &providerdata.OAuthClient{Client: NewSDKClient(t, srv, opts...)}
}
