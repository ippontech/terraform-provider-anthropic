// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package claudetag_test

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

const targetAddr = "anthropic_claude_tag_bundle_target.test"

func targetConfig(bundleID, attr, ref string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) + fmt.Sprintf(
		"resource \"anthropic_claude_tag_bundle_target\" \"test\" {\n  bundle_id = %q\n  %s = %q\n}\n", bundleID, attr, ref)
}

func targetState(id, kind, channelID, workspaceID, createdAt string) []statecheck.StateCheck {
	ref := func(v string) knownvalue.Check {
		if v == "" {
			return knownvalue.Null()
		}
		return knownvalue.StringExact(v)
	}
	return attrChecks(targetAddr, map[string]knownvalue.Check{
		"id": knownvalue.StringExact(id), "bundle_id": knownvalue.StringExact("capp_support"), "type": knownvalue.StringExact(kind),
		"slack_channel_id": ref(channelID), "slack_workspace_id": ref(workspaceID), "created_at": knownvalue.StringExact(createdAt),
	})
}

// seedTargets connects slackTeam (slws_0001), sets slackChan up (slch_0002)
// and stores two Bundles.
func seedTargets(t *testing.T) *claudeTagFake {
	t.Helper()
	f := newClaudeTagFake(t, 0)
	f.putWorkspace(slackTeam, nil)                            // 16:01
	f.putChannel(nil)                                         // 16:02
	f.putBundle("capp_support", "Support tools", "Runbooks.") // 16:03
	f.putBundle("capp_github", "GitHub", nil)                 // 16:04
	return f
}

// TestBundleTargetResource_lifecycle attaches a Bundle to a channel, imports
// the attachment as <bundle_id>:<target_id>, moves it to the workspace (a
// replacement: the target has no update), attaches it again after a detach
// in claude.ai (same pair, same ID, #bundle-targets), and detaches it.
func TestBundleTargetResource_lifecycle(t *testing.T) {
	t.Parallel()
	f := seedTargets(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		CheckDestroy: func(*terraform.State) error {
			if ids := f.targetIDs(); len(ids) != 0 {
				return fmt.Errorf("still attached: %v", ids)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:            targetConfig("capp_support", "slack_channel_id", "slch_0002"),
				ConfigPlanChecks:  expectAction(targetAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: targetState("btgt_supportX0002", "slack_channel", "slch_0002", "", at(5)),
			},
			{
				ResourceName: targetAddr, ImportState: true, ImportStateVerify: true,
				ImportStateIdFunc: func(*terraform.State) (string, error) { return "capp_support:btgt_supportX0002", nil },
			},
			{
				Config:            targetConfig("capp_support", "slack_workspace_id", "slws_0001"),
				ConfigPlanChecks:  expectAction(targetAddr, plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: targetState("btgt_supportX0001", "slack_workspace", "", "slws_0001", at(6)),
			},
			{
				PreConfig:         func() { f.detachOutOfBand("btgt_supportX0001") },
				Config:            targetConfig("capp_support", "slack_workspace_id", "slws_0001"),
				ConfigPlanChecks:  expectAction(targetAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: targetState("btgt_supportX0001", "slack_workspace", "", "slws_0001", at(7)),
			},
		},
	})

	f.AssertJSONBodies(routeAttach,
		`{"type": "slack_channel", "slack_channel_id": "slch_0002"}`,
		`{"type": "slack_workspace", "slack_workspace_id": "slws_0001"}`,
		`{"type": "slack_workspace", "slack_workspace_id": "slws_0001"}`)
	var detached []string
	for _, r := range f.Requests(routeDetach) {
		detached = append(detached, r.Path)
	}
	want := []string{
		"/v1/organizations/claude_tag/bundles/capp_support/targets/btgt_supportX0002",
		"/v1/organizations/claude_tag/bundles/capp_support/targets/btgt_supportX0001",
	}
	if !slices.Equal(detached, want) {
		t.Errorf("detached %q, want %q", detached, want)
	}
	f.assertLookupsAndProbes(routeListWorkspaces, "", 0)
	f.assertRequests()
}

// TestBundleTargetResource_adoptsAndErrors: an attachment made in claude.ai
// is adopted ("Attaching a pair that is already attached returns 200 with the
// existing Bundle target", #attach-a-bundle); attaching to a channel that is
// not set up is a 404 the diagnostic explains; a detach that answers 404 is
// success (#detach-a-bundle); a removed channel takes its targets with it.
func TestBundleTargetResource_adoptsAndErrors(t *testing.T) {
	t.Parallel()

	t.Run("adopts", func(t *testing.T) {
		t.Parallel()
		f := seedTargets(t)
		f.set(func(f *claudeTagFake) {
			f.targets = append(f.targets, &target{id: "btgt_githubX0002", bundleID: "capp_github", kind: "slack_channel", ref: "slch_0002", createdAt: at(9)})
		})
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config: targetConfig("capp_github", "slack_channel_id", "slch_0002"),
				ConfigStateChecks: attrChecks(targetAddr, map[string]knownvalue.Check{
					"id": knownvalue.StringExact("btgt_githubX0002"), "created_at": knownvalue.StringExact(at(9)),
				}),
			}},
		})
		f.AssertCalled(routeAttach, 1)
	})

	t.Run("not set up", func(t *testing.T) {
		t.Parallel()
		f := seedTargets(t)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config:      targetConfig("capp_support", "slack_channel_id", "slch_9999"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+attach\s+Claude\s+Tag\s+bundle\s+capp_support.*attaching\s+never\s+sets\s+a\s+channel\s+up`),
			}},
		})
	})

	t.Run("detach 404 and channel removed", func(t *testing.T) {
		t.Parallel()
		f := seedTargets(t)
		config := targetConfig("capp_support", "slack_channel_id", "slch_0002")
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{
				{Config: config},
				{
					// The channel is removed in claude.ai: its targets go with it
					// (#remove-a-channel), and the target's 404 removes it from
					// the state; it cannot be attached again until the channel
					// is set up again.
					PreConfig:          func() { f.removeOutOfBand("slch_0002") },
					Config:             config,
					PlanOnly:           true,
					ExpectNonEmptyPlan: true,
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction(targetAddr, plancheck.ResourceActionCreate)},
					},
				},
				// The plan-only step kept the old state: the final destroy
				// detaches a target that is gone, and its 404 is success.
			},
		})
		f.AssertCalled(routeDetach, 1)
	})

	t.Run("errors", func(t *testing.T) {
		t.Parallel()
		f := seedTargets(t)
		config := targetConfig("capp_support", "slack_channel_id", "slch_0002")
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{
				{Config: config},
				{
					PreConfig:   func() { f.FailNext(routeGetTarget, http.StatusForbidden, 1) },
					Config:      config,
					ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+Claude\s+Tag\s+bundle\s+target\s+btgt_supportX0002`),
				},
				{
					PreConfig:   func() { f.FailNext(routeDetach, http.StatusForbidden, 1) },
					Config:      config,
					Destroy:     true,
					ExpectError: regexp.MustCompile(`Unable\s+to\s+detach\s+Claude\s+Tag\s+bundle\s+capp_support`),
				},
			},
		})
		f.AssertCalled(routeDetach, 2)
	})
}

// TestBundleTargetResource_invalid: IDs of the wrong kind, both or neither
// target, and a malformed import ID fail before any request.
func TestBundleTargetResource_invalid(t *testing.T) {
	t.Parallel()
	both := providertest.ProviderConfig(sdktest.AdminKey) + "resource \"anthropic_claude_tag_bundle_target\" \"test\" {\n" +
		"  bundle_id = \"capp_support\"\n  slack_channel_id = \"slch_0002\"\n  slack_workspace_id = \"slws_0001\"\n}\n"
	neither := providertest.ProviderConfig(sdktest.AdminKey) +
		"resource \"anthropic_claude_tag_bundle_target\" \"test\" {\n  bundle_id = \"capp_support\"\n}\n"
	for name, tc := range map[string]struct{ config, err string }{
		"both":            {both, `Invalid\s+Attribute\s+Combination`},
		"neither":         {neither, `Missing\s+Attribute\s+Configuration`},
		"bundle ID":       {targetConfig("support", "slack_channel_id", "slch_0002"), `must\s+be\s+a\s+Bundle\s+ID`},
		"Slack channel":   {targetConfig("capp_support", "slack_channel_id", slackChan), `not\s+Slack's\s+own\s+channel\s+ID`},
		"Slack workspace": {targetConfig("capp_support", "slack_workspace_id", slackTeam), `not\s+Slack's\s+own\s+workspace\s+ID`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newClaudeTagFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps:                    []resource.TestStep{{Config: tc.config, ExpectError: regexp.MustCompile(tc.err)}},
			})
			f.AssertCalled("", 0)
		})
	}

	t.Run("import ID", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		for _, id := range []string{"btgt_supportX0002", "capp_support/btgt_supportX0002", "capp_support:slch_0002"} {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config: targetConfig("capp_support", "slack_channel_id", "slch_0002"), ResourceName: targetAddr,
					ImportState: true, ImportStateId: id,
					ExpectError: regexp.MustCompile(`Expected\s+<bundle_id>:<target_id>`),
				}},
			})
		}
		f.AssertCalled("", 0)
	})
}
