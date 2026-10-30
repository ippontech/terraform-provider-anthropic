// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation_test

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeFederationAPI is an in-memory stand-in for the federation rule
// endpoints, for resource.UnitTest: the real provider talks to it through
// ANTHROPIC_BASE_URL with a dummy OAuth bearer, so Terraform itself checks
// the plan/apply contract ("inconsistent result after apply") that a direct
// method call would not.
//
// It reproduces the two response quirks the resource works around:
//   - the create response has an empty issuer_name, which GET returns (#243);
//   - the update response has no target.service_account_name, which GET
//     returns (#349); the update reference marks it "optional string or null"
//     (https://platform.claude.com/docs/en/api/beta/organization/federation/rules/update).
//
// An update replaces each field present in the body, match and target as
// whole objects, and a null description clears it.
type fakeFederationAPI struct {
	srv *httptest.Server

	mu     sync.Mutex
	rules  map[string]map[string]any
	lastID int
	clock  int
	// failReadAfterUpdate makes the first GET after each update answer 403,
	// so the update's consistency wait gives up at once.
	failReadAfterUpdate bool
	failNextRead        bool
	failedReads         int
}

// fakeServiceAccountNames are the display names GET resolves target IDs to.
var fakeServiceAccountNames = map[string]string{"svac_001": "sa-one", "svac_002": "sa-two"}

// newFakeFederationAPI starts the fake and points the provider at it with a
// dummy OAuth bearer. t.Setenv rules out t.Parallel for the caller.
func newFakeFederationAPI(t *testing.T) *fakeFederationAPI {
	t.Helper()
	f := &fakeFederationAPI{rules: map[string]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/organizations/federation_rules", f.create)
	mux.HandleFunc("GET /v1/organizations/federation_rules/{id}", f.get)
	mux.HandleFunc("POST /v1/organizations/federation_rules/{id}", f.update)
	mux.HandleFunc("POST /v1/organizations/federation_rules/{id}/archive", f.archive)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)

	t.Setenv("ANTHROPIC_BASE_URL", f.srv.URL)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "test-oauth-token")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_ADMIN_API_KEY", "")
	return f
}

// with runs fn under the fake's lock, to change its behaviour between steps.
func (f *fakeFederationAPI) with(fn func(f *fakeFederationAPI)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeFederationAPI) create(w http.ResponseWriter, r *http.Request) {
	body := f.begin(r)
	defer f.mu.Unlock()

	f.lastID++
	now := f.now()
	rule := map[string]any{
		"id":                        fmt.Sprintf("fdrl_%03d", f.lastID),
		"type":                      "federation_rule",
		"issuer_id":                 body["issuer_id"],
		"issuer_name":               "github",
		"description":               nil,
		"workspace_id":              nil,
		"applies_to_all_workspaces": false,
		"token_lifetime_seconds":    3600,
		"attributes":                nil,
		"created_at":                now,
		"created_by_actor_id":       "user_001",
		"archived_at":               nil,
		"archived_by_actor_id":      nil,
	}
	f.apply(rule, body, now)
	f.rules[rule["id"].(string)] = rule

	created := maps.Clone(rule)
	created["issuer_name"] = "" // #243
	f.reply(w, http.StatusOK, created)
}

func (f *fakeFederationAPI) get(w http.ResponseWriter, r *http.Request) {
	f.begin(r)
	defer f.mu.Unlock()

	rule, ok := f.rules[r.PathValue("id")]
	switch {
	case !ok:
		f.reply(w, http.StatusNotFound, apiError("not_found_error", "Federation rule not found"))
	case f.failNextRead:
		f.failNextRead = false
		f.failedReads++
		f.reply(w, http.StatusForbidden, apiError("permission_error", "scripted read failure"))
	default:
		f.reply(w, http.StatusOK, rule)
	}
}

func (f *fakeFederationAPI) update(w http.ResponseWriter, r *http.Request) {
	body := f.begin(r)
	defer f.mu.Unlock()

	rule, ok := f.rules[r.PathValue("id")]
	if !ok {
		f.reply(w, http.StatusNotFound, apiError("not_found_error", "Federation rule not found"))
		return
	}
	f.apply(rule, body, f.now())
	f.failNextRead = f.failReadAfterUpdate

	updated := maps.Clone(rule)
	target := maps.Clone(rule["target"].(map[string]any))
	delete(target, "service_account_name") // #349
	updated["target"] = target
	f.reply(w, http.StatusOK, updated)
}

func (f *fakeFederationAPI) archive(w http.ResponseWriter, r *http.Request) {
	f.begin(r)
	defer f.mu.Unlock()

	rule, ok := f.rules[r.PathValue("id")]
	if !ok {
		f.reply(w, http.StatusNotFound, apiError("not_found_error", "Federation rule not found"))
		return
	}
	rule["archived_at"] = f.now()
	f.reply(w, http.StatusOK, rule)
}

// apply copies the writable fields present in body onto rule.
func (f *fakeFederationAPI) apply(rule, body map[string]any, now string) {
	for _, field := range []string{"name", "description", "match", "oauth_scope", "workspace_id", "applies_to_all_workspaces", "token_lifetime_seconds", "attributes"} {
		if v, present := body[field]; present {
			rule[field] = v
		}
	}
	if target, ok := body["target"].(map[string]any); ok {
		id, _ := target["service_account_id"].(string)
		rule["target"] = map[string]any{
			"type":                 "service_account",
			"service_account_id":   id,
			"service_account_name": fakeServiceAccountNames[id],
		}
	}
	workspaceIDs := []any{}
	if id, ok := rule["workspace_id"].(string); ok {
		workspaceIDs = append(workspaceIDs, id)
	}
	rule["workspace_ids"] = workspaceIDs
	rule["updated_at"] = now
	rule["updated_by_actor_id"] = "user_001"
}

// begin locks the fake (the handler unlocks it) and returns the decoded JSON
// body (nil when empty).
func (f *fakeFederationAPI) begin(r *http.Request) map[string]any {
	var body map[string]any
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	f.mu.Lock()
	return body
}

func (f *fakeFederationAPI) reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(typ, message string) map[string]any {
	return map[string]any{"type": "error", "error": map[string]any{"type": typ, "message": message}}
}

// now returns a strictly increasing RFC 3339 timestamp.
func (f *fakeFederationAPI) now() string {
	f.clock++
	return time.Date(2026, 1, 1, 0, 0, f.clock, 0, time.UTC).Format(time.RFC3339)
}
