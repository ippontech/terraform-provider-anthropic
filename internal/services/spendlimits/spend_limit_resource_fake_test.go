// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package spendlimits_test

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

const limitAddr = "anthropic_spend_limit.test"

func limitConfig(body string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) + "resource \"anthropic_spend_limit\" \"test\" {\n" + body + "}\n"
}

func userLimit(period, amount string) string {
	body := "  scope = {\n    type    = \"user\"\n    user_id = \"user_alice\"\n  }\n"
	if period != "" {
		body += "  period = \"" + period + "\"\n"
	}
	if amount != "" {
		body += "  amount = \"" + amount + "\"\n"
	}
	return limitConfig(body)
}

// expectLimit is the state of the n-th limit; amount "" means null.
func expectLimit(n int, scope map[string]string, period, amount string, enabled bool, updatedAt string) []statecheck.StateCheck {
	scopeChecks := map[string]knownvalue.Check{"type": knownvalue.StringExact(scope["type"]), "user_id": knownvalue.Null(), "workspace_id": knownvalue.Null()}
	for _, k := range []string{"user_id", "workspace_id"} {
		if v, ok := scope[k]; ok {
			scopeChecks[k] = knownvalue.StringExact(v)
		}
	}
	var amountCheck knownvalue.Check = knownvalue.Null()
	if amount != "" {
		amountCheck = knownvalue.StringExact(amount)
	}
	created := fmt.Sprintf("2026-08-%02dT10:00:00Z", n)
	if updatedAt == "" {
		updatedAt = created
	}
	at := func(attr string, c knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(limitAddr, tfjsonpath.New(attr), c)
	}
	return []statecheck.StateCheck{
		at("id", knownvalue.StringExact(limitID(n))),
		at("scope", knownvalue.ObjectExact(scopeChecks)),
		at("period", knownvalue.StringExact(period)),
		at("amount", amountCheck),
		at("currency", knownvalue.StringExact("USD")),
		at("is_enabled", knownvalue.Bool(enabled)),
		at("created_at", knownvalue.StringExact(created)),
		at("updated_at", knownvalue.StringExact(updatedAt)),
	}
}

func (f *limitsFake) checkGone(ids ...string) func(*terraform.State) error {
	return func(*terraform.State) error {
		for _, id := range ids {
			if f.limit(id) != nil {
				return fmt.Errorf("spend limit %s still exists after destroy", id)
			}
		}
		return nil
	}
}

var alice = map[string]string{"type": "user", "user_id": "user_alice"}

// TestSpendLimitResource_lifecycle sets a member's monthly limit (period
// defaults to monthly), imports it, changes the amount in place (the upsert
// keeps the row), makes it an explicit no-limit override (amount null),
// replaces it on a new period, and deletes it on destroy.
func TestSpendLimitResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newLimitsFake(t)
	const updated = "2026-08-31T00:00:00Z"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkGone(limitID(1), limitID(2)),
		Steps: []resource.TestStep{
			{
				Config:            userLimit("", "50000"),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectLimit(1, alice, "monthly", "50000", true, ""),
			},
			{
				Config:            userLimit("", "50000"),
				ResourceName:      limitAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:            userLimit("", "75000"),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectLimit(1, alice, "monthly", "75000", true, updated),
			},
			{
				Config:            userLimit("", ""),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectLimit(1, alice, "monthly", "", true, updated),
			},
			{
				Config:            userLimit("weekly", "10000"),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionReplace),
				ConfigStateChecks: expectLimit(2, alice, "weekly", "10000", true, ""),
			},
		},
	})

	const scope = `"scope":{"type":"user","user_id":"user_alice"}`
	srv.AssertJSONBodies(routeSetLimit,
		`{`+scope+`,"period":"monthly","amount":"50000"}`,
		`{`+scope+`,"period":"monthly","amount":"75000"}`,
		`{`+scope+`,"period":"monthly","amount":null}`,
		`{`+scope+`,"period":"weekly","amount":"10000"}`)
	var deleted []string
	for _, r := range srv.Requests(routeDeleteLimit) {
		deleted = append(deleted, r.Path)
	}
	if want := fmt.Sprint([]string{"/v1/organizations/spend_limits/" + limitID(1), "/v1/organizations/spend_limits/" + limitID(2)}); fmt.Sprint(deleted) != want {
		t.Errorf("deleted %v, want %s", deleted, want)
	}
	srv.assertRequests(t)
}

// TestSpendLimitResource_consoleScopes sets a workspace limit and an
// organization limit (Claude Console), and follows extra usage switched off
// (is_enabled false) without a diff.
func TestSpendLimitResource_consoleScopes(t *testing.T) {
	t.Parallel()
	srv := newLimitsFake(t)
	ws := map[string]string{"type": "workspace", "workspace_id": "wrkspc_team"}
	org := map[string]string{"type": "organization"}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkGone(limitID(1), limitID(2)),
		Steps: []resource.TestStep{
			{
				Config:            limitConfig("  scope = {\n    type = \"workspace\"\n    workspace_id = \"wrkspc_team\"\n  }\n  amount = \"200000\"\n"),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectLimit(1, ws, "monthly", "200000", true, ""),
			},
			{
				Config:            limitConfig("  scope = { type = \"organization\" }\n  period = \"monthly\"\n  amount = \"900000\"\n"),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionReplace),
				ConfigStateChecks: expectLimit(2, org, "monthly", "900000", true, ""),
			},
			{
				PreConfig: func() {
					srv.mu.Lock()
					defer srv.mu.Unlock()
					srv.limits[0]["is_enabled"] = false
				},
				Config:            limitConfig("  scope = { type = \"organization\" }\n  period = \"monthly\"\n  amount = \"900000\"\n"),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionNoop),
				ConfigStateChecks: expectLimit(2, org, "monthly", "900000", false, ""),
			},
		},
	})
	srv.AssertJSONBodies(routeSetLimit,
		`{"scope":{"type":"workspace","workspace_id":"wrkspc_team"},"period":"monthly","amount":"200000"}`,
		`{"scope":{"type":"organization"},"period":"monthly","amount":"900000"}`)
}

// TestSpendLimitResource_removedOutsideTerraform: a limit deleted behind
// Terraform's back leaves the state on refresh, and the next apply sets it
// again.
func TestSpendLimitResource_removedOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newLimitsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkGone(limitID(2)),
		Steps: []resource.TestStep{
			{Config: userLimit("daily", "1000"), ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate)},
			{
				PreConfig:         func() { srv.drop(limitID(1)) },
				Config:            userLimit("daily", "1000"),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectLimit(2, alice, "daily", "1000", true, ""),
			},
		},
	})
	srv.AssertCalled(routeSetLimit, 2)
}

// TestSpendLimitResource_deleteNotFound: a limit already gone (404) when
// Terraform deletes it is not an error.
func TestSpendLimitResource_deleteNotFound(t *testing.T) {
	t.Parallel()
	srv := newLimitsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: userLimit("", "1"), ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate)},
			{
				PreConfig:        func() { srv.FailNext(routeDeleteLimit, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.AdminKey),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionDestroy),
			},
		},
	})
	srv.AssertCalled(routeDeleteLimit, 1)
}

// TestSpendLimitResource_apiErrors: an API error on set, refresh, update or
// delete is an error diagnostic naming the operation.
func TestSpendLimitResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newLimitsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkGone(limitID(1)),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { srv.FailNext(routeSetLimit, http.StatusBadRequest, 1) },
				Config:      userLimit("", "100"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+set\s+spend\s+limit:\s+API\s+error\s+\(400\s+invalid_request_error\)`),
			},
			{Config: userLimit("", "100"), ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.FailNext(routeGetLimit, http.StatusForbidden, 1) },
				Config:      userLimit("", "100"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+spend\s+limit:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeSetLimit, http.StatusBadRequest, 1) },
				Config:      userLimit("", "200"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+update\s+spend\s+limit:\s+API\s+error\s+\(400\s+invalid_request_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeDeleteLimit, http.StatusForbidden, 1) },
				Config:      providertest.ProviderConfig(sdktest.AdminKey),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+delete\s+spend\s+limit:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
	srv.AssertCalled(routeSetLimit, 3)
}

// TestSpendLimitResource_invalidConfig: the scope identifiers must match the
// scope type, and Console scopes are monthly with an amount; checked before
// any request.
func TestSpendLimitResource_invalidConfig(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ body, err string }{
		"user without user_id":       {"  scope = { type = \"user\" }\n  amount = \"1\"\n", `(?s)Missing\s+scope\s+identifier.*scope.user_id\s+is\s+required`},
		"workspace with user_id":     {"  scope = {\n    type = \"workspace\"\n    workspace_id = \"w\"\n    user_id = \"u\"\n  }\n  amount = \"1\"\n", `(?s)Unexpected\s+scope\s+identifier.*scope.user_id\s+is\s+only\s+allowed`},
		"weekly organization limit":  {"  scope = { type = \"organization\" }\n  period = \"weekly\"\n  amount = \"1\"\n", `(?s)Unsupported\s+period.*organization\s+limits\s+are\s+monthly\s+only`},
		"workspace limit, no amount": {"  scope = {\n    type = \"workspace\"\n    workspace_id = \"w\"\n  }\n", `(?s)Missing\s+amount.*workspace\s+limits\s+always\s+carry\s+an\s+amount`},
		"amount with a leading zero": {"  scope = {\n    type = \"user\"\n    user_id = \"u\"\n  }\n  amount = \"0100\"\n", `must\s+be\s+a\s+non-negative\s+integer\s+decimal\s+string`},
		"read-only scope type":       {"  scope = { type = \"seat_tier\" }\n  amount = \"1\"\n", `Invalid\s+Attribute\s+Value\s+Match`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newLimitsFake(t)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps:                    []resource.TestStep{{Config: limitConfig(tc.body), ExpectError: regexp.MustCompile(tc.err)}},
			})
			srv.AssertCalled("", 0)
		})
	}
}
