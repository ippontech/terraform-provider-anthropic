// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package claudetag_test

import (
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
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

const slackWorkspacesURL = "/v1/organizations/claude_tag/slack_workspaces"

const workspaceAddr = "anthropic_claude_tag_slack_workspace_settings.test"

func workspaceConfig(slackID string, settings ...string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) +
		"resource \"anthropic_claude_tag_slack_workspace_settings\" \"test\" {\n  slack = {\n    workspace_id = \"" + slackID +
		"\"\n  }\n  " + strings.Join(settings, "\n  ") + "\n}\n"
}

// slackWorkspaceCheck checks the `slack` object of a workspace.
func slackWorkspaceCheck(workspaceID string) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{"workspace_id": knownvalue.StringExact(workspaceID)})
}

func workspaceState(id, createdAt, updatedAt string, settings map[string]knownvalue.Check) []statecheck.StateCheck {
	checks := map[string]knownvalue.Check{
		"id": knownvalue.StringExact(id), "slack": slackWorkspaceCheck(slackTeam),
		"created_at": knownvalue.StringExact(createdAt), "updated_at": knownvalue.StringExact(updatedAt),
		"enabled": knownvalue.Null(), "instructions": knownvalue.Null(), "model": knownvalue.Null(),
		"environment_id": knownvalue.Null(), "guest_policy": knownvalue.Null(),
	}
	maps.Copy(checks, settings)
	return attrChecks(workspaceAddr, checks)
}

// TestSlackWorkspaceSettingsResource_lifecycle adopts a workspace connected in
// claude.ai (clearing the instructions it stores and the configuration does
// not declare), imports it, changes two settings, follows a disconnection
// and new connection (a new slws_ ID), and on destroy clears what it stores,
// leaving the workspace connected.
func TestSlackWorkspaceSettingsResource_lifecycle(t *testing.T) {
	t.Parallel()
	f := newClaudeTagFake(t, 0)
	f.putWorkspace(slackTeam, map[string]any{"instructions": "Set in claude.ai.", "model": "claude-sonnet-5-5"}) // slws_0001, 16:01

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		CheckDestroy: func(*terraform.State) error {
			s := f.settingsOf("slws_0002")
			if s == nil {
				return fmt.Errorf("the workspace was disconnected")
			}
			for k, v := range s {
				if v != nil {
					return fmt.Errorf("the workspace still stores %s = %v after destroy", k, v)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:           workspaceConfig(slackTeam, `enabled = true`, `model = "claude-sonnet-5-5"`, `guest_policy = "restrict"`),
				ConfigPlanChecks: expectAction(workspaceAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: workspaceState("slws_0001", at(1), at(2), map[string]knownvalue.Check{
					"enabled": knownvalue.Bool(true), "model": knownvalue.StringExact("claude-sonnet-5-5"),
					"guest_policy": knownvalue.StringExact("restrict"),
				}),
			},
			{ResourceName: workspaceAddr, ImportState: true, ImportStateVerify: true},
			{
				Config:           workspaceConfig(slackTeam, `enabled = true`, `model = "claude-opus-5-5"`),
				ConfigPlanChecks: expectAction(workspaceAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: workspaceState("slws_0001", at(1), at(3), map[string]knownvalue.Check{
					"enabled": knownvalue.Bool(true), "model": knownvalue.StringExact("claude-opus-5-5"),
				}),
			},
			{
				// Disconnected and connected again in claude.ai: a new ID with
				// nothing stored (#ids).
				PreConfig: func() {
					f.removeOutOfBand("slws_0001")
					f.putWorkspace(slackTeam, nil) // slws_0002, 16:04
				},
				Config:           workspaceConfig(slackTeam, `enabled = true`, `model = "claude-opus-5-5"`),
				ConfigPlanChecks: expectAction(workspaceAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: workspaceState("slws_0002", at(4), at(5), map[string]knownvalue.Check{
					"enabled": knownvalue.Bool(true), "model": knownvalue.StringExact("claude-opus-5-5"),
				}),
			},
		},
	})

	// Two lookups by Slack's ID, and between them the probes that confirmed
	// the 404 of the old ID on refresh.
	f.assertLookupsAndProbes(routeListWorkspaces, "slack_workspace_id="+slackTeam, 2)
	f.AssertJSONBodies(routeUpdateWorkspace,
		`{"settings": {"enabled": true, "instructions": null, "guest_policy": {"type": "restrict"}}}`,
		`{"settings": {"model": "claude-opus-5-5", "guest_policy": null}}`,
		`{"settings": {"enabled": true, "model": "claude-opus-5-5"}}`,
		`{"settings": {"enabled": null, "model": null}}`)
	f.assertRequests()
}

// TestSlackWorkspaceSettingsResource_slackIDReplaces: Slack's own ID names
// the workspace, so changing it clears the settings of the first workspace
// (its destroy) and adopts the second, leaving both connected.
func TestSlackWorkspaceSettingsResource_slackIDReplaces(t *testing.T) {
	t.Parallel()
	f := newClaudeTagFake(t, 0)
	first := f.putWorkspace(slackTeam, nil)    // slws_0001
	second := f.putWorkspace("T02SECOND", nil) // slws_0002

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: workspaceConfig(slackTeam, `enabled = false`), ConfigPlanChecks: expectAction(workspaceAddr, plancheck.ResourceActionCreate)},
			{
				Config:           workspaceConfig("T02SECOND", `enabled = false`),
				ConfigPlanChecks: expectAction(workspaceAddr, plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("id"), knownvalue.StringExact(second)),
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("slack"), slackWorkspaceCheck("T02SECOND")),
				},
				Check: func(*terraform.State) error {
					if s := f.settingsOf(first); s == nil || s["enabled"] != nil {
						return fmt.Errorf("after the replacement %s stores %v, want connected with nothing stored", first, s)
					}
					return nil
				},
			},
		},
	})
	var paths []string
	for _, r := range f.Requests(routeUpdateWorkspace) {
		paths = append(paths, r.Path)
	}
	want := []string{slackWorkspacesURL + "/" + first, slackWorkspacesURL + "/" + first, slackWorkspacesURL + "/" + second, slackWorkspacesURL + "/" + second}
	if !slices.Equal(paths, want) {
		t.Errorf("updated %q, want %q (set, clear, set, clear)", paths, want)
	}
	f.AssertJSONBodies(routeUpdateWorkspace,
		`{"settings": {"enabled": false}}`, `{"settings": {"enabled": null}}`,
		`{"settings": {"enabled": false}}`, `{"settings": {"enabled": null}}`)
}

// TestSlackWorkspaceSettingsResource_notSetUp: workspaces are connected in claude.ai,
// so a workspace that is not set up, or that the organization does not own,
// is an error naming what to do (#set-up-channels-and-workspaces,
// #list-workspaces).
func TestSlackWorkspaceSettingsResource_notSetUp(t *testing.T) {
	t.Parallel()
	f := newClaudeTagFake(t, 0)
	f.own("T02GRIDWS")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:      workspaceConfig("T02GRIDWS", `enabled = false`),
				ExpectError: regexp.MustCompile(`(?s)Slack\s+Workspace\s+Not\s+Set\s+Up.*connects\s+workspaces\s+in\s+claude.ai`),
			},
			{
				Config:      workspaceConfig("T03ELSEWHERE", `enabled = false`),
				ExpectError: regexp.MustCompile(`(?s)Slack\s+Workspace\s+Not\s+Found.*does\s+not\s+own\s+it.*anthropic_claude_tag_slack_channel`),
			},
			{
				PreConfig:   func() { f.FailNext(routeListWorkspaces, http.StatusForbidden, 1) },
				Config:      workspaceConfig("T02GRIDWS", `enabled = false`),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+look\s+up\s+Slack\s+workspace\s+T02GRIDWS`),
			},
			{
				Config:      workspaceConfig("t02gridws"),
				ExpectError: regexp.MustCompile(`must\s+be\s+Slack's\s+own\s+workspace\s+ID`),
			},
		},
	})
	f.AssertCalled(routeUpdateWorkspace, 0)
}

// TestSlackWorkspaceSettingsResource_runnerPoolAndErrors: destroy clears every
// setting but a runner pool, which the API cannot clear (#settings); a
// workspace already gone (404) is destroyed; a refused clear is reported;
// adopting or updating the workspace without its pool is refused before any
// request.
func TestSlackWorkspaceSettingsResource_runnerPoolAndErrors(t *testing.T) {
	t.Parallel()

	t.Run("runner pool", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		id := f.putWorkspace(slackTeam, map[string]any{"environment_id": "ccpool_01runner", "instructions": "Be brief."})
		config := workspaceConfig(slackTeam, `environment_id = "ccpool_01runner"`, `instructions = "Be brief."`)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			CheckDestroy: func(*terraform.State) error {
				if s := f.settingsOf(id); s["environment_id"] != "ccpool_01runner" || s["instructions"] != nil {
					return fmt.Errorf("after destroy the workspace stores %v", s)
				}
				return nil
			},
			Steps: []resource.TestStep{
				{
					// Adopting the workspace without declaring its pool is
					// refused before any change is sent.
					Config:      workspaceConfig(slackTeam, `instructions = "Be brief."`),
					ExpectError: regexp.MustCompile(`(?s)Runner\s+Pool\s+Cannot\s+Be\s+Changed.*The\s+workspace\s+stores`),
				},
				{Config: config, ConfigPlanChecks: expectAction(workspaceAddr, plancheck.ResourceActionCreate)},
				{
					Config:      workspaceConfig(slackTeam, `instructions = "Be brief."`),
					ExpectError: regexp.MustCompile(`(?s)Runner\s+Pool\s+Cannot\s+Be\s+Changed.*The\s+workspace\s+stores`),
				},
			},
		})
		f.AssertJSONBodies(routeUpdateWorkspace, `{"settings": {"instructions": null}}`)
	})

	t.Run("gone and refused", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		f.putWorkspace(slackTeam, nil)
		config := workspaceConfig(slackTeam, `enabled = false`)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{
				{Config: config},
				{
					PreConfig: func() {
						f.refuseNext(routeUpdateWorkspace, http.StatusForbidden, "compliance_configuration_restricted")
					},
					Config:      config,
					Destroy:     true,
					ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+clear\s+the\s+Claude\s+Tag\s+settings\s+of\s+workspace\s+slws_0001.*stores\s+nothing\s+new`),
				},
				{
					PreConfig:   func() { f.FailNext(routeGetWorkspace, http.StatusForbidden, 1) },
					Config:      config,
					ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+Claude\s+Tag\s+workspace\s+slws_0001`),
				},
				{
					PreConfig:   func() { f.refuseNext(routeUpdateWorkspace, http.StatusBadRequest, "") },
					Config:      workspaceConfig(slackTeam, `enabled = true`),
					ExpectError: regexp.MustCompile(`Unable\s+to\s+update\s+the\s+Claude\s+Tag\s+settings\s+of\s+the\s+workspace`),
				},
				{PreConfig: func() { f.FailNext(routeUpdateWorkspace, http.StatusNotFound, 1) }, Config: config, Destroy: true},
			},
		})
		f.AssertCalled(routeUpdateWorkspace, 4)
	})

	t.Run("nothing stored", func(t *testing.T) {
		t.Parallel()
		// No setting declared, none stored: adopting and destroying send nothing.
		f := newClaudeTagFake(t, 0)
		f.putWorkspace(slackTeam, nil)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config:            workspaceConfig(slackTeam),
				ConfigPlanChecks:  expectAction(workspaceAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: workspaceState("slws_0001", at(1), at(1), nil),
			}},
		})
		f.AssertCalled(routeUpdateWorkspace, 0)
	})

	t.Run("white space only", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		f.putWorkspace(slackTeam, nil)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{
				{Config: workspaceConfig(slackTeam, `instructions = "Be brief."`)},
				{
					Config:           workspaceConfig(slackTeam, `instructions = "Be brief.\n"`),
					ConfigPlanChecks: expectAction(workspaceAddr, plancheck.ResourceActionUpdate),
				},
				{ResourceName: workspaceAddr, ImportState: true, ImportStateId: slackTeam,
					ExpectError: regexp.MustCompile(`Expected\s+the\s+workspace's\s+Claude\s+Tag\s+ID\s+\(prefixed\s+slws_\)`)},
			},
		})
		f.AssertJSONBodies(routeUpdateWorkspace, `{"settings": {"instructions": "Be brief."}}`, `{"settings": {"instructions": null}}`)
	})
}
