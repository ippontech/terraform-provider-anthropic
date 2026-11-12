// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package webhooks implements the workspace webhook endpoints that receive
// Managed Agents events (sessions, vaults, agents, deployments, environments,
// memory stores): the anthropic_webhook resource. Anthropic documents these
// endpoints only as registered in the Console, and no SDK release manages
// them, so the resource sends raw requests through the standard SDK client
// (`/v1/webhooks`, beta `managed-agents-2026-04-01`) and is experimental.
package webhooks

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// webhooksBeta is the `anthropic-beta` value of the Managed Agents API, which
// the Console sends on every webhook endpoint request.
const webhooksBeta = "managed-agents-2026-04-01"

// webhooksPath is the collection endpoint. The API scopes it to the workspace
// of the key, or of the anthropic-workspace-id header. It is relative to the
// SDK's base URL, which on a gateway or a proxy carries a path of its own
// (/v1/projects/.../invoke on Google Cloud), as internal/admin writes its
// paths; SDK v1.84.0 also trims a leading slash, but the routes do not rely
// on it (TestDoWebhookRequest_keepsBaseURLPath).
const webhooksPath = "v1/webhooks"

// webhookAttemptTimeout bounds one attempt, response body included, as the
// Admin client's attemptTimeout does: a write holds webhookWrites, so a
// server that never answers would otherwise stall every other webhook write.
const webhookAttemptTimeout = 60 * time.Second

// webhookStateEnabled is the `state` of an endpoint that receives deliveries;
// the other value is `disabled`.
const (
	webhookStateEnabled  = "enabled"
	webhookStateDisabled = "disabled"
)

// webhookEventTypes lists the event types a webhook endpoint accepts, as the
// Managed Agents webhooks page documents them (re-read on 2026-11-12). The
// enabled_events validator and its documentation are built from it, so a new
// event type documented by the API is added here and nowhere else.
var webhookEventTypes = []string{
	"session.status_run_started",
	"session.status_idled",
	"session.budget_reached",
	"session.status_rescheduled",
	"session.status_terminated",
	"session.thread_created",
	"session.thread_idled",
	"session.thread_terminated",
	"session.outcome_evaluation_ended",
	"session.updated",
	"session.deleted",
	"vault.created",
	"vault.archived",
	"vault.deleted",
	"vault_credential.created",
	"vault_credential.archived",
	"vault_credential.deleted",
	"vault_credential.refresh_failed",
	"agent.created",
	"agent.updated",
	"agent.archived",
	"agent.deleted",
	"deployment.created",
	"deployment.updated",
	"deployment.paused",
	"deployment.unpaused",
	"deployment.archived",
	"deployment.deleted",
	"deployment_run.started",
	"deployment_run.succeeded",
	"deployment_run.failed",
	"environment.created",
	"environment.updated",
	"environment.archived",
	"environment.deleted",
	"memory_store.created",
	"memory_store.archived",
	"memory_store.deleted",
}

// webhookPath returns the item endpoint, with the ID path-escaped.
func webhookPath(id string) string {
	return webhooksPath + "/" + url.PathEscape(id)
}

// webhookWrites serializes this provider's writes to the workspace's webhook
// endpoints, as anthropic_organization_webhook does: Terraform creates,
// updates and deletes several endpoints in parallel, the organization
// endpoints answer 409 to concurrent changes, and nothing says these do not.
// Writes are few, so waiting costs little.
var webhookWrites sync.Mutex

// doWebhookRequest sends one request with the Managed Agents beta header and
// a per-attempt timeout, and returns the raw response body. opts come last,
// so a caller can override the client's retries. Any request other than a
// GET waits for the other writes of this provider to finish.
func doWebhookRequest(ctx context.Context, client *anthropic.Client, method, path string, body any, opts ...option.RequestOption) ([]byte, error) {
	if method != http.MethodGet {
		webhookWrites.Lock()
		defer webhookWrites.Unlock()
	}
	var out []byte
	opts = append([]option.RequestOption{
		option.WithHeader("anthropic-beta", webhooksBeta),
		option.WithRequestTimeout(webhookAttemptTimeout),
	}, opts...)
	err := client.Execute(ctx, method, path, body, &out, opts...)
	return out, err
}

// isNotFound reports whether err is an API 404.
func isNotFound(err error) bool {
	var apierr *anthropic.Error
	return errors.As(err, &apierr) && apierr.StatusCode == http.StatusNotFound
}

// webhooksUnavailable reports, after a 404 on an endpoint, whether the whole
// API answers 404 too: a retired beta version or a gateway that stops serving
// the routes would answer 404 for every endpoint, and only a list that
// answers tells a deleted endpoint from that. It returns the list's error, or
// nil when the list answered.
func webhooksUnavailable(ctx context.Context, client *anthropic.Client) error {
	_, err := doWebhookRequest(ctx, client, http.MethodGet, webhooksPath+"?limit=1", nil)
	return err
}

// --- API request/response types ---

// webhookAPIResponse is an endpoint as every read returns it. The
// create response adds signing_secret, which no other response carries.
type webhookAPIResponse struct {
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

type webhookSigningSecretResponse struct {
	SigningSecret string `json:"signing_secret"`
}

// --- Terraform data models ---

// WebhookModel holds the attributes read back from the API, which
// the resource copies into its own model. It never carries the signing
// secret: only the resource stores it, and no read returns it.
type WebhookModel struct {
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

// mapWebhookToModel maps an API endpoint to
// WebhookModel. The API returns "" for an unset name, description
// or disabled_reason, which maps to null; `state` maps to the enabled boolean.
func mapWebhookToModel(ep *webhookAPIResponse) (WebhookModel, diag.Diagnostics) {
	events := make([]attr.Value, len(ep.EnabledEvents))
	for i, e := range ep.EnabledEvents {
		events[i] = types.StringValue(e)
	}
	eventSet, diags := types.SetValue(types.StringType, events)

	return WebhookModel{
		ID:             types.StringValue(ep.ID),
		URL:            types.StringValue(ep.URL),
		EnabledEvents:  eventSet,
		Name:           tfvalue.StringOrNull(ep.Name),
		Description:    tfvalue.StringOrNull(ep.Description),
		Enabled:        types.BoolValue(ep.State == webhookStateEnabled),
		DisabledReason: tfvalue.StringOrNull(ep.DisabledReason),
		CreatedAt:      tfvalue.StringOrNull(ep.CreatedAt),
		UpdatedAt:      tfvalue.StringOrNull(ep.UpdatedAt),
	}, diags
}
