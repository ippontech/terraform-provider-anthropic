// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package organizationwebhooks implements the organization webhook endpoints
// that receive events about enforcement actions on the organization's user
// profiles: the anthropic_organization_webhook resource. It goes through the
// Admin API client (internal/admin) with the `webhooks-2026-10-07` beta
// header.
package organizationwebhooks

import (
	"context"
	"net/http"
	"net/url"
	"sync"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// organizationWebhooksBeta is the `anthropic-beta` value every organization
// webhook request must carry: without it the API answers 404, exactly as if
// the endpoints did not exist.
const organizationWebhooksBeta = "webhooks-2026-10-07"

// organizationWebhooksPath is the collection endpoint.
const organizationWebhooksPath = "/v1/organizations/webhooks"

// organizationWebhookStateEnabled is the `state` of an endpoint that receives
// deliveries; the other value is `disabled`.
const (
	organizationWebhookStateEnabled  = "enabled"
	organizationWebhookStateDisabled = "disabled"
)

// organizationWebhookEventTypes lists the event types an organization webhook
// endpoint accepts. The enabled_events validator and its documentation are
// built from it, so a new event type documented by the API is added here and
// nowhere else.
var organizationWebhookEventTypes = []string{
	"user_profile.enforcement_action_opened",
	"user_profile.enforcement_action_resolved",
}

// organizationWebhookPath returns the item endpoint, with the ID path-escaped.
func organizationWebhookPath(id string) string {
	return organizationWebhooksPath + "/" + url.PathEscape(id)
}

// organizationWebhookWrites serializes this provider's writes to the
// organization's webhook endpoints. The organization webhooks guide documents
// a 409 for concurrent changes to an organization's endpoints, Terraform
// creates, updates and deletes several endpoints in parallel, and the Admin
// client does not replay a POST after a 409. Writes are few (at most 32
// endpoints), so waiting costs little.
var organizationWebhookWrites sync.Mutex

// doOrganizationWebhookRequest sends one Admin API request with the beta
// header these endpoints require. Any request other than a GET waits for the
// other writes of this provider to finish.
func doOrganizationWebhookRequest(ctx context.Context, client *admin.Client, method, path string, body any) ([]byte, error) {
	if method != http.MethodGet {
		organizationWebhookWrites.Lock()
		defer organizationWebhookWrites.Unlock()
	}
	return client.DoRequest(ctx, method, path, body, admin.WithBeta(organizationWebhooksBeta))
}

// organizationWebhooksUnavailable reports, after a 404 on an endpoint, whether
// the whole API answers 404 too: the API also answers 404 when the
// organization does not have the webhooks beta or the pinned beta version is
// retired, and only a list that answers tells a deleted endpoint from that.
// It returns the list's error, or nil when the list answered.
func organizationWebhooksUnavailable(ctx context.Context, client *admin.Client) error {
	_, err := doOrganizationWebhookRequest(ctx, client, http.MethodGet, organizationWebhooksPath+"?limit=1", nil)
	return err
}

// --- Admin API request/response types ---

// organizationWebhookAPIResponse is an endpoint as every read returns it. The
// create response adds signing_secret, which no other response carries.
type organizationWebhookAPIResponse struct {
	ID string `json:"id"`
	// Name, Description and DisabledReason are "" when unset.
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	URL            string   `json:"url"`
	EnabledEvents  []string `json:"enabled_events"`
	State          string   `json:"state"`
	DisabledReason string   `json:"disabled_reason"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
	SigningSecret  string   `json:"signing_secret"`
}

type organizationWebhookSigningSecretResponse struct {
	SigningSecret string `json:"signing_secret"`
}

// --- Terraform data models ---

// OrganizationWebhookModel holds the attributes read back from the API, which
// the resource copies into its own model. It never carries the signing
// secret: only the resource stores it, and no read returns it.
type OrganizationWebhookModel struct {
	ID             types.String `tfsdk:"id"`
	URL            types.String `tfsdk:"url"`
	EnabledEvents  types.Set    `tfsdk:"enabled_events"`
	Name           types.String `tfsdk:"name"`
	Description    types.String `tfsdk:"description"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	DisabledReason types.String `tfsdk:"disabled_reason"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
}

// mapOrganizationWebhookToModel maps an API endpoint to
// OrganizationWebhookModel. The API returns "" for an unset name, description
// or disabled_reason, which maps to null; `state` maps to the enabled boolean.
func mapOrganizationWebhookToModel(ep *organizationWebhookAPIResponse) (OrganizationWebhookModel, diag.Diagnostics) {
	events := make([]attr.Value, len(ep.EnabledEvents))
	for i, e := range ep.EnabledEvents {
		events[i] = types.StringValue(e)
	}
	eventSet, diags := types.SetValue(types.StringType, events)

	return OrganizationWebhookModel{
		ID:             types.StringValue(ep.ID),
		URL:            types.StringValue(ep.URL),
		EnabledEvents:  eventSet,
		Name:           tfvalue.StringOrNull(ep.Name),
		Description:    tfvalue.StringOrNull(ep.Description),
		Enabled:        types.BoolValue(ep.State == organizationWebhookStateEnabled),
		DisabledReason: tfvalue.StringOrNull(ep.DisabledReason),
		CreatedAt:      tfvalue.StringOrNull(ep.CreatedAt),
		UpdatedAt:      tfvalue.StringOrNull(ep.UpdatedAt),
	}, diags
}
