// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package spendlimits_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the spend limit endpoints the resource calls (Admin key,
// ?beta=true in the query, no beta header). References:
// https://platform.claude.com/docs/en/api/beta/organization/spend_limits/{set,retrieve,delete}.
const (
	routeSetLimit    = "POST /v1/organizations/spend_limits"
	routeGetLimit    = "GET /v1/organizations/spend_limits/{spend_limit_id}"
	routeDeleteLimit = "DELETE /v1/organizations/spend_limits/{spend_limit_id}"
)

var allRoutes = []string{routeSetLimit, routeGetLimit, routeDeleteLimit}

// limitsFake serves the organization's spend limits.
type limitsFake struct {
	*sdktest.Server

	mu      sync.Mutex
	created int
	limits  []map[string]any
}

func newLimitsFake(t *testing.T) *limitsFake {
	t.Helper()
	f := &limitsFake{Server: sdktest.NewServer(t)}
	f.Handle(routeSetLimit, f.set)
	f.Handle(routeGetLimit, f.get)
	f.Handle(routeDeleteLimit, f.delete)
	return f
}

func limitID(n int) string { return fmt.Sprintf("spl_%024d", n) }

// limitJSON is a BetaSpendLimit; a nil amount is the no-limit override.
func limitJSON(n int, scope map[string]any, period string, amount any) map[string]any {
	at := fmt.Sprintf("2026-08-%02dT10:00:00Z", n)
	return map[string]any{
		"type": "spend_limit", "id": limitID(n), "scope": scope, "period": period, "amount": amount,
		"currency": "USD", "is_enabled": true, "created_at": at, "updated_at": at,
	}
}

func (f *limitsFake) limit(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.limits {
		if l["id"] == id {
			return sdktest.DeepCopy(l)
		}
	}
	return nil
}

func (f *limitsFake) drop(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.limits = slices.DeleteFunc(f.limits, func(l map[string]any) bool { return l["id"] == id })
}

func sameScope(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// set applies Set Spend Limit
// (https://platform.claude.com/docs/en/api/beta/organization/spend_limits/set):
// an "upsert keyed on (scope, period)", which SpendLimitResource.Create
// relies on to adopt an existing limit; period defaults to monthly, which
// mapSpendLimitToState maps back.
func (f *limitsFake) set(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	scope, _ := body["scope"].(map[string]any)
	period, _ := body["period"].(string)
	if period == "" {
		period = "monthly"
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.limits {
		if sameScope(l["scope"], scope) && l["period"] == period {
			l["amount"] = body["amount"]
			l["updated_at"] = "2026-08-31T00:00:00Z"
			sdktest.WriteJSON(w, http.StatusOK, l)
			return
		}
	}
	f.created++
	l := limitJSON(f.created, scope, period, body["amount"])
	f.limits = append(f.limits, l)
	sdktest.WriteJSON(w, http.StatusOK, l)
}

func (f *limitsFake) get(w http.ResponseWriter, r *http.Request) {
	if l := f.limit(r.PathValue("spend_limit_id")); l != nil {
		sdktest.WriteJSON(w, http.StatusOK, l)
		return
	}
	sdktest.WriteAPIError(w, http.StatusNotFound, "", "spend limit not found")
}

// delete answers {"id", "type": "spend_limit_deleted"}.
func (f *limitsFake) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("spend_limit_id")
	if f.limit(id) == nil {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "spend limit not found")
		return
	}
	f.drop(id)
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "type": "spend_limit_deleted"})
}

// expectAction checks the planned action on the spend limit resource, then
// an empty plan after the apply and refresh.
func expectAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return providertest.ExpectAction(limitAddr, action)
}

// assertRequests checks the credential, ?beta=true and the absence of a
// beta header on every request.
func (f *limitsFake) assertRequests(t *testing.T) {
	t.Helper()
	n := 0
	for _, route := range allRoutes {
		reqs := f.Requests(route)
		if len(reqs) == 0 {
			continue
		}
		n++
		f.AssertCredential(route, sdktest.AdminKey)
		f.AssertNoBeta(route)
		for _, r := range reqs {
			if r.Query.Get("beta") != "true" {
				t.Errorf("%s %s?%s: want beta=true", r.Method, r.Path, r.Query.Encode())
			}
		}
	}
	if n == 0 {
		t.Fatal("the fake received no request")
	}
}
