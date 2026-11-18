// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package platform

import "fmt"

// State is whether a gateway platform serves a resource or data source.
type State int

const (
	// StateUndeclared means nobody said. It behaves like StateUnverified at
	// run time; the provider's registry tests require a declaration for every
	// platform a provider alias can be configured for.
	StateUndeclared State = iota
	// StateSupported means the documentation lists every route the type uses.
	StateSupported
	// StateUnverified means the documentation is silent or contradicts itself.
	// The type is allowed: a route the gateway turns out not to serve fails
	// with an explicit error (see NotFoundGuard), and never drops an object
	// from state.
	StateUnverified
	// StateUnavailable means the documentation says the gateway does not serve
	// the routes the type needs. Configuring the type fails with a diagnostic
	// naming the platform, before any request is sent.
	StateUnavailable
)

// Support is one platform's answer for one resource or data source type.
type Support struct {
	State State
	// Reason says why the type is unavailable or unverified. It is printed in
	// the diagnostic, after the platform name, so it is a full sentence.
	Reason string
	// OrganizationRoutes marks a type that calls /v1/organizations routes
	// only. Claude Models on Google Cloud serves them without a workspace,
	// under the organization's URL, so such a type also works on a provider
	// alias that sets no workspace; every other type needs one there.
	OrganizationRoutes bool
}

// OnOrganizationRoutes returns s for a type that calls organization routes
// only (see Support.OrganizationRoutes).
func (s Support) OnOrganizationRoutes() Support {
	s.OrganizationRoutes = true
	return s
}

// Supported declares a type the platform serves.
func Supported() Support {
	return Support{State: StateSupported}
}

// Unverified declares a type whose availability the documentation leaves open;
// reason says what is open.
func Unverified(reason string) Support {
	return Support{State: StateUnverified, Reason: reason}
}

// Unavailable declares a type the platform does not serve; reason says why,
// for the diagnostic.
func Unavailable(reason string) Support {
	return Support{State: StateUnavailable, Reason: reason}
}

// Allows reports whether a type with this support may be configured.
func (s Support) Allows() bool {
	return s.State != StateUnavailable
}

// UnavailableSummary and UnavailableDetail make the diagnostic for typeName,
// which p does not serve. The detail names the platform, says why (the reason
// also says where the object is managed instead, when the platform has such a
// place) and links the platform's documentation.
//
// It does not suggest a first-party alias: a gateway platform has its own
// organization, so an alias for api.anthropic.com would manage a different
// organization's objects.
func UnavailableSummary(typeName string, p Platform) string {
	return fmt.Sprintf("%s Not Available on %s", typeName, p.DisplayName())
}

// UnavailableDetail is the detail of the diagnostic UnavailableSummary heads.
func UnavailableDetail(typeName string, p Platform, s Support) string {
	detail := fmt.Sprintf("%s is not available on %s, which this provider alias is configured for.", typeName, p.DisplayName())
	if s.Reason != "" {
		detail += " " + s.Reason
	}
	return detail + " See " + p.DocURL() + "."
}

// WorkspaceRequiredSummary and WorkspaceRequiredDetail make the diagnostic for
// typeName on a provider alias for p that sets no workspace, when typeName
// calls workspace routes.
func WorkspaceRequiredSummary(typeName string, p Platform) string {
	return fmt.Sprintf("%s Requires a Workspace on %s", typeName, p.DisplayName())
}

// WorkspaceRequiredDetail is the detail of the diagnostic
// WorkspaceRequiredSummary heads.
func WorkspaceRequiredDetail(typeName string, p Platform) string {
	return fmt.Sprintf("%s calls workspace routes, which %s serves under a workspace's URL, and this provider alias "+
		"sets no workspace. Set workspace_id (or the ANTHROPIC_GOOGLE_CLOUD_WORKSPACE_ID environment variable) to a "+
		"workspace ID from the Claude Console, under Settings > Workspaces. Without one, an alias serves only the "+
		"types that call organization routes. See %s.", typeName, p.DisplayName(), p.DocURL())
}
