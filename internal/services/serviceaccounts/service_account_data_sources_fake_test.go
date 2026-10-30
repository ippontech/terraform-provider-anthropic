// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

const (
	serviceAccountDataAddr  = "data.anthropic_service_account.test"
	serviceAccountsDataAddr = "data.anthropic_service_accounts.test"
	membershipsDataAddr     = "data.anthropic_service_account_workspaces.test"
)

// accountObject is the exact list element of a service account the fake
// stored with newAccount and never updated.
func accountObject(id, name string, description any, role, createdAt string, archivedAt any) knownvalue.Check {
	archivedBy := any(nil)
	updatedAt := createdAt
	if archivedAt != nil {
		archivedBy = fakeActorID
		updatedAt = archivedAt.(string)
	}
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"id":                   knownvalue.StringExact(id),
		"name":                 knownvalue.StringExact(name),
		"description":          stringOrNull(description),
		"organization_role":    knownvalue.StringExact(role),
		"created_at":           knownvalue.StringExact(createdAt),
		"updated_at":           knownvalue.StringExact(updatedAt),
		"archived_at":          stringOrNull(archivedAt),
		"created_by_actor_id":  knownvalue.StringExact(fakeActorID),
		"updated_by_actor_id":  knownvalue.StringExact(fakeActorID),
		"archived_by_actor_id": stringOrNull(archivedBy),
	})
}

// archive archives a stored account as the archive endpoint does.
func (f *serviceAccountsFake) archiveAccount(id string) {
	a, _ := f.accounts.Archive(id)
	a["archived_by_actor_id"] = fakeActorID
	f.accounts.Put(a)
}

// TestServiceAccountDataSource_read reads a live and an archived service
// account by ID, with every attribute exact, and an empty description as
// null.
func TestServiceAccountDataSource_read(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 0)
	live := srv.newAccount("reader", "Reads things", "admin")["id"].(string)
	archived := srv.newAccount("retired", "", nil)["id"].(string)
	srv.archiveAccount(archived)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config: providertest.ProviderConfig(sdktest.AuthToken) + `
data "anthropic_service_account" "test" { id = "` + live + `" }
`,
				ConfigStateChecks: expectServiceAccount(serviceAccountDataAddr, live, "reader", "Reads things", "admin", at(1), at(1)),
			},
			{
				Config: providertest.ProviderConfig(sdktest.AuthToken) + `
data "anthropic_service_account" "test" { id = "` + archived + `" }
`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(serviceAccountDataAddr, tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue(serviceAccountDataAddr, tfjsonpath.New("archived_at"), knownvalue.StringExact(at(3))),
					statecheck.ExpectKnownValue(serviceAccountDataAddr, tfjsonpath.New("archived_by_actor_id"), knownvalue.StringExact(fakeActorID)),
					statecheck.ExpectKnownValue(serviceAccountDataAddr, tfjsonpath.New("updated_at"), knownvalue.StringExact(at(3))),
				},
			},
		},
	})
	srv.AssertWalks(routeGetServiceAccount, "beta=true")
	srv.assertRequestShape(t)
}

// TestServiceAccountDataSource_notFound: an unknown ID is an error.
func TestServiceAccountDataSource_notFound(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.AuthToken) + `data "anthropic_service_account" "test" { id = "svac_missing" }`,
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+service\s+account:.*404`),
		}},
	})
	srv.AssertCalled(routeGetServiceAccount, 1)
}

// fourAccounts stores four service accounts and archives the second.
func (f *serviceAccountsFake) fourAccounts() {
	f.newAccount("first", "One", nil)
	f.newAccount("second", nil, "admin")
	f.newAccount("third", nil, nil)
	f.newAccount("fourth", nil, nil)
	f.archiveAccount(serviceAccountID(2))
}

// TestServiceAccountsDataSource_pages lists the live service accounts over
// two pages, newest first, without the archived one.
func TestServiceAccountsDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 2)
	srv.fourAccounts()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AuthToken) + `data "anthropic_service_accounts" "test" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(serviceAccountsDataAddr, tfjsonpath.New("service_accounts"), knownvalue.ListExact([]knownvalue.Check{
					accountObject(serviceAccountID(4), "fourth", nil, "developer", at(4), nil),
					accountObject(serviceAccountID(3), "third", nil, "developer", at(3), nil),
					accountObject(serviceAccountID(1), "first", "One", "developer", at(1), nil),
				})),
			},
		}},
	})
	srv.AssertWalks(routeListServiceAccounts, "beta=true", "beta=true&page="+sdktest.NextPageToken(serviceAccountID(3)))
	srv.AssertAllPagesRead(routeListServiceAccounts)
	srv.assertRequestShape(t)
}

// TestServiceAccountsDataSource_includeArchived sends include_archived=true
// and walks every page, the archived account included.
func TestServiceAccountsDataSource_includeArchived(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 2)
	srv.fourAccounts()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AuthToken) + `data "anthropic_service_accounts" "test" { include_archived = true }`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(serviceAccountsDataAddr, tfjsonpath.New("service_accounts"), knownvalue.ListExact([]knownvalue.Check{
					accountObject(serviceAccountID(4), "fourth", nil, "developer", at(4), nil),
					accountObject(serviceAccountID(3), "third", nil, "developer", at(3), nil),
					accountObject(serviceAccountID(2), "second", nil, "admin", at(2), at(5)),
					accountObject(serviceAccountID(1), "first", "One", "developer", at(1), nil),
				})),
			},
		}},
	})
	srv.AssertWalks(routeListServiceAccounts,
		"beta=true&include_archived=true",
		"beta=true&include_archived=true&page="+sdktest.NextPageToken(serviceAccountID(3)))
	srv.AssertAllPagesRead(routeListServiceAccounts)
	srv.assertRequestShape(t)
}

// TestServiceAccountsDataSource_empty: no service account is an empty list,
// never null.
func TestServiceAccountsDataSource_empty(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AuthToken) + `data "anthropic_service_accounts" "test" { include_archived = false }`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(serviceAccountsDataAddr, tfjsonpath.New("service_accounts"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
	srv.AssertWalks(routeListServiceAccounts, "beta=true&include_archived=false")
}

// TestServiceAccountsDataSource_apiError: a failed list is an error.
func TestServiceAccountsDataSource_apiError(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 0)
	srv.FailNext(routeListServiceAccounts, http.StatusForbidden, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.AuthToken) + `data "anthropic_service_accounts" "test" {}`,
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+service\s+accounts:.*403`),
		}},
	})
	srv.AssertCalled(routeListServiceAccounts, 1)
}

// memberObject is the exact list element of a membership.
func memberObject(workspaceID, role string, implicit bool) knownvalue.Check {
	createdBy := knownvalue.Check(knownvalue.StringExact(fakeActorID))
	if implicit {
		createdBy = knownvalue.Null()
	}
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"workspace_id":        knownvalue.StringExact(workspaceID),
		"workspace_role":      knownvalue.StringExact(role),
		"implicit":            knownvalue.Bool(implicit),
		"created_by_actor_id": createdBy,
	})
}

func (f *serviceAccountsFake) putMembership(serviceAccountID, workspaceID, role string) {
	f.memberships.Put(map[string]any{
		"id": serviceAccountID + ":" + workspaceID, "type": "service_account_workspace_member",
		"service_account_id": serviceAccountID, "workspace_id": workspaceID, "workspace_role": role,
		"implicit": false, "created_by_actor_id": fakeActorID,
	})
}

// TestServiceAccountWorkspacesDataSource_pages lists the implicit
// membership and two explicit ones over two pages, and only the memberships
// of the service account asked for.
func TestServiceAccountWorkspacesDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 2)
	sa := srv.newAccount("member", nil, nil)["id"].(string)
	other := srv.newAccount("other", nil, nil)["id"].(string)
	srv.putMembership(sa, "wrkspc_01", "workspace_admin")
	srv.putMembership(other, "wrkspc_01", "workspace_user")
	srv.putMembership(sa, "wrkspc_02", "workspace_restricted_developer")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AuthToken) + `data "anthropic_service_account_workspaces" "test" { service_account_id = "` + sa + `" }`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(membershipsDataAddr, tfjsonpath.New("workspaces"), knownvalue.ListExact([]knownvalue.Check{
					memberObject(defaultWorkspaceID, "workspace_user", true),
					memberObject("wrkspc_01", "workspace_admin", false),
					memberObject("wrkspc_02", "workspace_restricted_developer", false),
				})),
			},
		}},
	})
	srv.AssertWalks(routeListMemberships, "beta=true", "beta=true&page="+sdktest.NextPageToken(sa+":wrkspc_01"))
	srv.AssertAllPagesRead(routeListMemberships)
	srv.assertRequestShape(t)
}

// TestServiceAccountWorkspacesDataSource_archived: an archived service
// account has no membership, which is an empty list, never null.
func TestServiceAccountWorkspacesDataSource_archived(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 0)
	sa := srv.newAccount("retired", nil, nil)["id"].(string)
	srv.putMembership(sa, "wrkspc_01", "workspace_admin")
	srv.archiveAccount(sa)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AuthToken) + `data "anthropic_service_account_workspaces" "test" { service_account_id = "` + sa + `" }`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(membershipsDataAddr, tfjsonpath.New("workspaces"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
	srv.AssertWalks(routeListMemberships, "beta=true")
}

// TestServiceAccountWorkspacesDataSource_notFound: an unknown service
// account is an error.
func TestServiceAccountWorkspacesDataSource_notFound(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.AuthToken) + `data "anthropic_service_account_workspaces" "test" { service_account_id = "svac_missing" }`,
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+service\s+account\s+workspaces:.*404`),
		}},
	})
	srv.AssertCalled(routeListMemberships, 1)
}
