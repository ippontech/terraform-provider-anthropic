// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package userprofiles_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the user profiles endpoints (beta user-profiles-2026-08-18,
// ?beta=true, standard key). References:
// https://platform.claude.com/docs/en/api/beta/user_profiles/create,
// .../retrieve, .../update, .../list and .../delete.
const (
	routeCreateProfile = "POST /v1/user_profiles"
	routeListProfiles  = "GET /v1/user_profiles"
	routeGetProfile    = "GET /v1/user_profiles/{user_profile_id}"
	routeUpdateProfile = "POST /v1/user_profiles/{user_profile_id}"
	routeDeleteProfile = "DELETE /v1/user_profiles/{user_profile_id}"

	userProfilesBeta = "user-profiles-2026-08-18"
)

var allRoutes = []string{routeCreateProfile, routeListProfiles, routeGetProfile, routeUpdateProfile, routeDeleteProfile}

// profilesFake is the user profiles API on an sdktest collection, with the
// payload of the user-profiles-2026-08-18 header: external_id at the top
// level, no external_user_details (list reference).
type profilesFake struct {
	*sdktest.Server
	profiles *sdktest.Collection
}

func newProfilesFake(t *testing.T, pageSize int) *profilesFake {
	t.Helper()
	srv := sdktest.NewServer(t)
	f := &profilesFake{Server: srv, profiles: srv.NewCollection("uprof_")}
	f.Handle(routeCreateProfile, f.create)
	f.Handle(routeListProfiles, func(w http.ResponseWriter, r *http.Request) {
		items := f.profiles.List(nil)
		if r.URL.Query().Get("order") != "asc" {
			slices.Reverse(items) // newest first by default
		}
		f.Paginate(w, r, sdktest.PageToken, pageSize, items)
	})
	f.profiles.HandleGet(routeGetProfile)
	f.Handle(routeUpdateProfile, f.update)
	f.profiles.HandleDelete(routeDeleteProfile, "user_profile_deleted")
	return f
}

// newProfile stores a profile: access_type "application" unless given,
// external_id and name null unless given, no trust grant (create
// reference).
func (f *profilesFake) newProfile(accessType, externalID, name any, metadata map[string]any) map[string]any {
	if accessType == nil {
		accessType = "application"
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	return f.profiles.Create(map[string]any{
		"type": "user_profile", "access_type": accessType, "external_id": externalID, "name": name,
		"metadata": metadata, "trust_grants": map[string]any{},
	})
}

// create refuses an empty metadata value ("Values must be non-empty
// strings", create reference).
func (f *profilesFake) create(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	metadata, _ := body["metadata"].(map[string]any)
	for k, v := range metadata {
		if v == "" {
			sdktest.WriteAPIError(w, http.StatusBadRequest, "", "metadata."+k+": value must be non-empty")
			return
		}
	}
	sdktest.WriteJSON(w, http.StatusOK, f.newProfile(body["access_type"], body["external_id"], body["name"], metadata))
}

// update replaces the fields sent (a null external_id or name clears it)
// and merges metadata, where an empty value removes the key (update
// reference).
func (f *profilesFake) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("user_profile_id")
	if !f.profiles.Has(id) {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "user profile "+id+" not found")
		return
	}
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	p, _ := f.profiles.Update(id, body, sdktest.PatchNullOrEmpty)
	sdktest.WriteJSON(w, http.StatusOK, p)
}

// assertRequestShape checks what every request of a test must carry: the
// user profiles beta, the standard key and ?beta=true, on every route
// called.
func (f *profilesFake) assertRequestShape(t *testing.T) {
	t.Helper()
	for _, route := range allRoutes {
		if len(f.Requests(route)) == 0 {
			continue
		}
		f.AssertBeta(route, userProfilesBeta)
		f.AssertCredential(route, sdktest.APIKey)
		for _, r := range f.Requests(route) {
			if r.Query.Get("beta") != "true" {
				t.Errorf("%s %s without ?beta=true", r.Method, r.Path)
			}
		}
	}
}

// decodeBody reads a JSON object body or answers 400 invalid_request_error.
func decodeBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	body := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "body is not a JSON object: "+err.Error())
		return nil, false
	}
	return body, true
}
