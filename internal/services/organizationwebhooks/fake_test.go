// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizationwebhooks_test

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the organization webhook endpoints (Admin key, beta
// webhooks-2026-10-07). References:
// https://platform.claude.com/docs/en/api/beta/organization/webhooks/{create,list,retrieve,update,delete,regenerate_signing_secret}
// and the guide https://platform.claude.com/docs/en/manage-claude/organization-webhooks
// ("Endpoint fields", "Manage endpoints", "Errors").
const (
	routeCreateWebhook = "POST /v1/organizations/webhooks"
	routeListWebhooks  = "GET /v1/organizations/webhooks"
	routeGetWebhook    = "GET /v1/organizations/webhooks/{webhook_endpoint_id}"
	routeUpdateWebhook = "POST /v1/organizations/webhooks/{webhook_endpoint_id}"
	routeDeleteWebhook = "DELETE /v1/organizations/webhooks/{webhook_endpoint_id}"
	routeRegenSecret   = "POST /v1/organizations/webhooks/{webhook_endpoint_id}/regenerate_signing_secret"

	webhooksBeta = "webhooks-2026-10-07"
	eventOpened  = "user_profile.enforcement_action_opened"
	eventClosed  = "user_profile.enforcement_action_resolved"
)

var allRoutes = []string{routeCreateWebhook, routeListWebhooks, routeGetWebhook, routeUpdateWebhook, routeDeleteWebhook, routeRegenSecret}

// webhooksFake serves the organization's webhook endpoints.
type webhooksFake struct {
	*sdktest.Server

	mu        sync.Mutex
	created   int
	regens    int
	endpoints []map[string]any // creation order
}

func newWebhooksFake(t *testing.T) *webhooksFake {
	t.Helper()
	f := &webhooksFake{Server: sdktest.NewServer(t)}
	for route, h := range map[string]http.HandlerFunc{
		routeCreateWebhook: f.create,
		routeListWebhooks:  f.list,
		routeGetWebhook:    f.get,
		routeUpdateWebhook: f.update,
		routeDeleteWebhook: f.delete,
		routeRegenSecret:   f.regenerate,
	} {
		// "exactly as if this API did not exist" without the beta header.
		f.Handle(route, sdktest.RequireBeta(webhooksBeta, h))
	}
	return f
}

func webhookID(n int) string { return fmt.Sprintf("wep_%024d", n) }

// endpointJSON is an endpoint as every read returns it: "" for an unset
// name, description or disabled_reason.
func endpointJSON(n int, url, name, description string, events ...string) map[string]any {
	at := fmt.Sprintf("2026-10-%02dT19:00:00Z", n)
	return map[string]any{
		"id": webhookID(n), "url": url, "name": name, "description": description, "enabled_events": events,
		"state": "enabled", "disabled_reason": "", "created_at": at, "updated_at": at,
	}
}

func (f *webhooksFake) put(ep map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	f.endpoints = append(f.endpoints, ep)
}

// index returns the position of the endpoint id, -1 when unknown. The caller
// holds f.mu.
func (f *webhooksFake) index(id string) int {
	return slices.IndexFunc(f.endpoints, func(ep map[string]any) bool { return ep["id"] == id })
}

func (f *webhooksFake) endpoint(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.index(id); i >= 0 {
		return sdktest.DeepCopy(f.endpoints[i])
	}
	return nil
}

func (f *webhooksFake) drop(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.endpoints = slices.DeleteFunc(f.endpoints, func(ep map[string]any) bool { return ep["id"] == id })
}

// disable turns an endpoint off as Anthropic does on its own.
func (f *webhooksFake) disable(id, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ep := f.endpoints[f.index(id)]
	ep["state"], ep["disabled_reason"] = "disabled", reason
}

// create applies Create: url, enabled_events, name and description; the
// answer adds the whsec_ signing_secret.
func (f *webhooksFake) create(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	url, _ := body["url"].(string)
	name, _ := body["name"].(string)
	description, _ := body["description"].(string)
	ep := endpointJSON(f.created, url, name, description)
	ep["enabled_events"] = body["enabled_events"]
	f.endpoints = append(f.endpoints, ep)
	out := sdktest.DeepCopy(ep)
	out["signing_secret"] = fmt.Sprintf("whsec_created%d", f.created)
	sdktest.WriteJSON(w, http.StatusOK, out)
}

// list answers the list of endpoints with an empty page. The provider reads
// it only after a 404 on one endpoint, to tell an endpoint deleted (the list
// answers) from a lost beta (the list answers 404 too), and ignores the page.
func (f *webhooksFake) list(w http.ResponseWriter, _ *http.Request) {
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"data": []any{}, "has_more": false, "first_id": nil, "last_id": nil})
}

func (f *webhooksFake) get(w http.ResponseWriter, r *http.Request) {
	if ep := f.endpoint(r.PathValue("webhook_endpoint_id")); ep != nil {
		sdktest.WriteJSON(w, http.StatusOK, ep)
		return
	}
	sdktest.WriteAPIError(w, http.StatusNotFound, "", "webhook endpoint not found")
}

// update applies Update: the fields sent change, the others stay; state
// enabled clears disabled_reason
// (https://platform.claude.com/docs/en/api/beta/organization/webhooks/update),
// which mapOrganizationWebhookToModel then maps to null when the next apply
// turns an endpoint Anthropic disabled back on.
func (f *webhooksFake) update(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.index(r.PathValue("webhook_endpoint_id"))
	if i < 0 {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "webhook endpoint not found")
		return
	}
	ep := f.endpoints[i]
	maps.Copy(ep, body)
	if body["state"] == "enabled" {
		ep["disabled_reason"] = ""
	}
	ep["updated_at"] = "2026-10-25T00:00:00Z"
	sdktest.WriteJSON(w, http.StatusOK, ep)
}

func (f *webhooksFake) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("webhook_endpoint_id")
	if f.endpoint(id) == nil {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "webhook endpoint not found")
		return
	}
	f.drop(id)
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "type": "webhook_endpoint_deleted"})
}

// regenerate answers {"signing_secret"} with a new secret.
func (f *webhooksFake) regenerate(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.index(r.PathValue("webhook_endpoint_id")) < 0 {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "webhook endpoint not found")
		return
	}
	f.regens++
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"signing_secret": fmt.Sprintf("whsec_regenerated%d", f.regens)})
}

// expectAction checks the planned action on the webhook resource, then an
// empty plan after the apply and refresh.
func expectAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return providertest.ExpectAction(webhookAddr, action)
}

// assertRequests checks the credential and the beta header of every route
// the fake received.
func (f *webhooksFake) assertRequests(t *testing.T) {
	t.Helper()
	f.AssertShape(sdktest.Shape{Credential: sdktest.AdminKey, Beta: webhooksBeta}, allRoutes...)
}
