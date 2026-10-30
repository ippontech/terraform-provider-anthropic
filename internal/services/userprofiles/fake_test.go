// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package userprofiles_test

import (
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

func (f *profilesFake) create(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	metadata, _ := body["metadata"].(map[string]any)
	sdktest.WriteJSON(w, http.StatusOK, f.newProfile(body["access_type"], body["external_id"], body["name"], metadata))
}

// update replaces the fields sent (a null external_id or name clears it,
// which buildUserProfileUpdateParams sends) and merges metadata, where an
// empty value removes the key, which buildUserProfileMetadataUpdate sends
// for a key removed from the configuration
// (https://platform.claude.com/docs/en/api/beta/user_profiles/update).
func (f *profilesFake) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("user_profile_id")
	if !f.profiles.Has(id) {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "user profile "+id+" not found")
		return
	}
	body, ok := f.DecodeBody(w, r)
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
	f.AssertShape(sdktest.Shape{Credential: sdktest.APIKey, Beta: userProfilesBeta, BetaQuery: true}, allRoutes...)
}
