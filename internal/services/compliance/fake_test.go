// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package compliance_test

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the Compliance API endpoints the package uses (Admin key or
// Compliance Access Key in x-api-key, no beta header). References:
// https://platform.claude.com/docs/en/api/organization/retrieve and
// .../organization/compliance_settings/{retrieve,update}.
const (
	routeGetOrganization = "GET /v1/organizations/me"
	routeGetSettings     = "GET /v1/organizations/compliance_settings"
	routeUpdateSettings  = "POST /v1/organizations/compliance_settings"

	orgID = "12345678-1234-5678-1234-567812345678"
)

var allRoutes = []string{routeGetOrganization, routeGetSettings, routeUpdateSettings}

// complianceFake serves the organization and its compliance settings.
type complianceFake struct {
	*sdktest.Server

	mu    sync.Mutex
	state string // compliance settings state.type
}

func newComplianceFake(t *testing.T, state string) *complianceFake {
	t.Helper()
	f := &complianceFake{Server: sdktest.NewServer(t), state: state}
	// "OrganizationInfo object": type, id, name.
	f.Respond(routeGetOrganization, http.StatusOK, map[string]any{"id": orgID, "name": "Ippon Test Org", "type": "organization"})
	f.Handle(routeGetSettings, func(w http.ResponseWriter, _ *http.Request) { f.writeSettings(w) })
	f.Handle(routeUpdateSettings, f.updateSettings)
	return f
}

func (f *complianceFake) setState(state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = state
}

func (f *complianceFake) currentState() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

// writeSettings answers the OrganizationComplianceSettings object, state in
// its canonical object form.
func (f *complianceFake) writeSettings(w http.ResponseWriter) {
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"type": "compliance_settings", "state": map[string]any{"type": f.currentState()}})
}

// updateSettings applies Update Compliance Settings: state as an object
// {"type": ...} or the "string shorthand"; the response "always returns the
// canonical object form", which parseState reads (the schema checks the
// value at plan).
func (f *complianceFake) updateSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		State json.RawMessage `json:"state"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var state string
	if json.Unmarshal(body.State, &state) != nil {
		var obj struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(body.State, &obj)
		state = obj.Type
	}
	f.setState(state)
	f.writeSettings(w)
}

// expectAction checks the planned action on the settings resource, then an
// empty plan after the apply and refresh.
func expectAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return providertest.ExpectAction(settingsAddr, action)
}

// assertRequests checks that every route the fake received carried the
// Admin key alone and no beta header.
func (f *complianceFake) assertRequests(t *testing.T) {
	t.Helper()
	f.AssertShape(sdktest.Shape{Credential: sdktest.AdminKey}, allRoutes...)
}
