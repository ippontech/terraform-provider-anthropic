// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizationwebhooks_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

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
		f.Handle(route, f.requireBeta(h))
	}
	return f
}

// requireBeta answers 404, "exactly as if this API did not exist", to a
// request without the anthropic-beta header.
func (f *webhooksFake) requireBeta(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(strings.Split(r.Header.Get("Anthropic-Beta"), ","), webhooksBeta) {
			sdktest.WriteAPIError(w, http.StatusNotFound, "", "Not Found")
			return
		}
		h(w, r)
	}
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
		return copyJSON(f.endpoints[i])
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

// urlTaken reports whether another enabled endpoint uses url: "Two enabled
// organization webhook endpoints in your organization can't share a URL"
// (409). The caller holds f.mu.
func (f *webhooksFake) urlTaken(url, except string) bool {
	return slices.ContainsFunc(f.endpoints, func(ep map[string]any) bool {
		return ep["id"] != except && ep["url"] == url && ep["state"] == "enabled"
	})
}

// validEvents applies "Must list at least one" and the two event types.
func validEvents(v any) ([]any, bool) {
	events, _ := v.([]any)
	for _, e := range events {
		if e != eventOpened && e != eventClosed {
			return nil, false
		}
	}
	return events, true
}

// create applies Create: url (HTTPS), enabled_events (at least one), name
// and description; the answer adds the whsec_ signing_secret.
func (f *webhooksFake) create(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "body is not JSON")
		return
	}
	url, _ := body["url"].(string)
	events, ok := validEvents(body["enabled_events"])
	if !strings.HasPrefix(url, "https://") || !ok || len(events) == 0 {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "url must be HTTPS and enabled_events must list at least one event type")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.urlTaken(url, "") {
		sdktest.WriteAPIError(w, http.StatusConflict, "", "another enabled endpoint uses this url")
		return
	}
	f.created++
	name, _ := body["name"].(string)
	description, _ := body["description"].(string)
	ep := endpointJSON(f.created, url, name, description)
	ep["enabled_events"] = events
	f.endpoints = append(f.endpoints, ep)
	out := copyJSON(ep)
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

// update applies Update: the fields sent change, the others stay; "An empty
// list doesn't clear the list"; state enabled clears disabled_reason.
func (f *webhooksFake) update(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "body is not JSON")
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
	if url, ok := body["url"].(string); ok && f.urlTaken(url, ep["id"].(string)) {
		sdktest.WriteAPIError(w, http.StatusConflict, "", "another enabled endpoint uses this url")
		return
	}
	for field, v := range body {
		switch field {
		case "enabled_events":
			events, ok := validEvents(v)
			if !ok {
				sdktest.WriteAPIError(w, http.StatusBadRequest, "", "unknown event type")
				return
			}
			if len(events) > 0 {
				ep[field] = events
			}
		case "state":
			ep[field] = v
			if v == "enabled" {
				ep["disabled_reason"] = ""
			}
		default:
			ep[field] = v
		}
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

func copyJSON(o map[string]any) map[string]any {
	b, _ := json.Marshal(o)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

// expectAction checks the planned action on the webhook resource, then an
// empty plan after the apply and refresh.
func expectAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(webhookAddr, action)},
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}

// assertRequests checks the credential and the beta header of every route
// the fake received.
func (f *webhooksFake) assertRequests(t *testing.T) {
	t.Helper()
	n := 0
	for _, route := range allRoutes {
		if len(f.Requests(route)) == 0 {
			continue
		}
		n++
		f.AssertCredential(route, sdktest.AdminKey)
		f.AssertBeta(route, webhooksBeta)
	}
	if n == 0 {
		t.Fatal("the fake received no request")
	}
}
