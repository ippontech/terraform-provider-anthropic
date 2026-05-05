// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package providerdata

import (
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

// ProviderData is passed to every resource and data source Configure call.
type ProviderData struct {
	// Client is the Anthropic SDK client for standard API endpoints.
	Client *anthropic.Client
	// AdminClient handles /v1/organizations/* endpoints using the Admin API key.
	// Nil when admin_api_key is not configured.
	AdminClient *admin.Client
}
