// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package externalkeys_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the external key (CMEK) endpoints (Admin key, no beta header).
// References: https://platform.claude.com/docs/en/api/organization/external_keys/{create,list,retrieve,update,delete}.
const (
	routeCreateKey = "POST /v1/organizations/external_keys"
	routeListKeys  = "GET /v1/organizations/external_keys"
	routeGetKey    = "GET /v1/organizations/external_keys/{external_key_id}"
	routeUpdateKey = "POST /v1/organizations/external_keys/{external_key_id}"
	routeDeleteKey = "DELETE /v1/organizations/external_keys/{external_key_id}"
)

var allRoutes = []string{routeCreateKey, routeListKeys, routeGetKey, routeUpdateKey, routeDeleteKey}

// keysFake serves the organization's external key configs.
type keysFake struct {
	*sdktest.Server
	pageSize int

	mu      sync.Mutex
	created int
	keys    []map[string]any
}

func newKeysFake(t *testing.T, pageSize int) *keysFake {
	t.Helper()
	f := &keysFake{Server: sdktest.NewServer(t), pageSize: pageSize}
	f.Handle(routeCreateKey, f.create)
	f.Handle(routeListKeys, f.list)
	f.Handle(routeGetKey, f.get)
	f.Handle(routeUpdateKey, f.update)
	f.Handle(routeDeleteKey, f.delete)
	return f
}

func keyID(n int) string { return fmt.Sprintf("ekey_%024d", n) }

// keyJSON is an ExternalKey: display_name null when none was set,
// attachment unattached until a workspace uses it.
func keyJSON(id, displayName string, providerConfig map[string]any) map[string]any {
	var name any
	if displayName != "" {
		name = displayName
	}
	return map[string]any{
		"type": "external_key", "id": id, "display_name": name, "geo": "us",
		"attachment": map[string]any{"type": "unattached"}, "provider_config": providerConfig,
		"created_at": "2026-09-01T10:00:00Z", "updated_at": "2026-09-01T10:00:00Z",
	}
}

func (f *keysFake) put(k map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	f.keys = append(f.keys, k)
}

// index returns the position of key id, -1 when unknown. The caller holds
// f.mu.
func (f *keysFake) index(id string) int {
	return slices.IndexFunc(f.keys, func(k map[string]any) bool { return k["id"] == id })
}

func (f *keysFake) key(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.index(id); i >= 0 {
		return copyJSON(f.keys[i])
	}
	return nil
}

func (f *keysFake) drop(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = slices.DeleteFunc(f.keys, func(k map[string]any) bool { return k["id"] == id })
}

// setAttachment records a workspace using the key ("attached"), or the last
// such workspace deleted ("unattached": deleted workspaces do not count).
func (f *keysFake) setAttachment(id, attachment string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys[f.index(id)]["attachment"] = map[string]any{"type": attachment}
}

// providerConfig checks a provider_config union member and fills region,
// which the reference says is "derived from kms_arn if omitted".
func providerConfig(v any) (map[string]any, bool) {
	pc, _ := v.(map[string]any)
	required := map[string][]string{"aws": {"kms_arn"}, "gcp": {"key_name"}, "azure": {"key_name", "vault_uri", "tenant_id"}}
	fields, ok := required[fmt.Sprint(pc["type"])]
	if !ok {
		return nil, false
	}
	for _, field := range fields {
		if s, _ := pc[field].(string); s == "" {
			return nil, false
		}
	}
	out := maps.Clone(pc)
	if out["type"] == "aws" && out["region"] == nil {
		if parts := strings.Split(out["kms_arn"].(string), ":"); len(parts) > 3 {
			out["region"] = parts[3]
		}
	}
	return out, true
}

// create applies Create External Key: provider_config required,
// display_name (1 to 255) optional, geo defaulting to the only one, "us".
func (f *keysFake) create(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "body is not JSON")
		return
	}
	pc, ok := providerConfig(body["provider_config"])
	if !ok {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "provider_config: invalid")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	name, _ := body["display_name"].(string)
	k := keyJSON(keyID(f.created), name, pc)
	k["created_at"] = fmt.Sprintf("2026-09-%02dT10:00:00Z", f.created)
	k["updated_at"] = k["created_at"]
	f.keys = append(f.keys, k)
	sdktest.WriteJSON(w, http.StatusOK, k)
}

func (f *keysFake) get(w http.ResponseWriter, r *http.Request) {
	if k := f.key(r.PathValue("external_key_id")); k != nil {
		sdktest.WriteJSON(w, http.StatusOK, k)
		return
	}
	sdktest.WriteAPIError(w, http.StatusNotFound, "", "external key not found")
}

// update applies Update External Key: the fields sent change (a null
// display_name clears it), the others stay. Once attached, "the provider
// fields become effectively immutable": geo and provider_config are refused.
func (f *keysFake) update(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "body is not JSON")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.index(r.PathValue("external_key_id"))
	if i < 0 {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "external key not found")
		return
	}
	k := f.keys[i]
	_, geo := body["geo"]
	_, pcSent := body["provider_config"]
	if (geo || pcSent) && k["attachment"].(map[string]any)["type"] == "attached" {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "the key is attached to a workspace")
		return
	}
	if pcSent {
		pc, ok := providerConfig(body["provider_config"])
		if !ok {
			sdktest.WriteAPIError(w, http.StatusBadRequest, "", "provider_config: invalid")
			return
		}
		body["provider_config"] = pc
	}
	maps.Copy(k, body)
	k["updated_at"] = "2026-09-30T00:00:00Z"
	sdktest.WriteJSON(w, http.StatusOK, k)
}

// delete applies Delete External Key: "The request is rejected if any
// workspace still references this config"; the answer is {"id", "type":
// "external_key_deleted"}.
func (f *keysFake) delete(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := r.PathValue("external_key_id")
	i := f.index(id)
	switch {
	case i < 0:
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "external key not found")
	case f.keys[i]["attachment"].(map[string]any)["type"] == "attached":
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "external key is referenced by a workspace")
	default:
		f.keys = slices.Delete(f.keys, i, i+1)
		sdktest.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "type": "external_key_deleted"})
	}
}

// list applies List External Keys: page/next_page pages, limit up to 100.
func (f *keysFake) list(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	items := make([]map[string]any, len(f.keys))
	for i, k := range f.keys {
		items[i] = copyJSON(k)
	}
	f.mu.Unlock()
	f.Paginate(w, r, sdktest.PageToken, f.pageSize, items)
}

func copyJSON(o map[string]any) map[string]any {
	b, _ := json.Marshal(o)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

// expectAction checks the planned action on the external key resource, then
// an empty plan after the apply and refresh.
func expectAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(keyAddr, action)},
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}

// assertRequests checks that every route the fake received carried the
// Admin key alone and no beta header.
func (f *keysFake) assertRequests(t *testing.T) {
	t.Helper()
	n := 0
	for _, route := range allRoutes {
		if len(f.Requests(route)) == 0 {
			continue
		}
		n++
		f.AssertCredential(route, sdktest.AdminKey)
		f.AssertNoBeta(route)
	}
	if n == 0 {
		t.Fatal("the fake received no request")
	}
}
