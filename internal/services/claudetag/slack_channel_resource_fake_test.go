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

const (
	channelAddr      = "anthropic_claude_tag_slack_channel.test"
	slackTeam        = "T01ABCD2EFG"
	slackChan        = "C05HJKL6MNP"
	environment      = "env_011CZsupport"
	lookupQuery      = "slack_channel_id=" + slackChan + "&slack_workspace_id=" + slackTeam
	slackChannelsURL = "/v1/organizations/claude_tag/slack_channels"
	slackBody        = `"slack": {"workspace_id": "` + slackTeam + `", "channel_id": "` + slackChan + `"}`
)

// channelConfig is the channel resource on slackTeam/slackChan with the
// given setting lines.
func channelConfig(settings ...string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) +
		"resource \"anthropic_claude_tag_slack_channel\" \"test\" {\n" +
		"  slack = {\n    workspace_id = \"" + slackTeam + "\"\n    channel_id   = \"" + slackChan + "\"\n  }\n  " +
		strings.Join(settings, "\n  ") + "\n}\n"
}

// channelState checks every attribute of the channel; settings not given
// are null.
func channelState(id, createdAt, updatedAt string, settings map[string]knownvalue.Check) []statecheck.StateCheck {
	checks := map[string]knownvalue.Check{
		"id": knownvalue.StringExact(id), "slack": slackChannelCheck(slackTeam, slackChan),
		"created_at": knownvalue.StringExact(createdAt), "updated_at": knownvalue.StringExact(updatedAt),
		"enabled": knownvalue.Null(), "instructions": knownvalue.Null(), "model": knownvalue.Null(),
		"environment_id": knownvalue.Null(), "guest_policy": knownvalue.Null(),
	}
	maps.Copy(checks, settings)
	return attrChecks(channelAddr, checks)
}

// slackChannelCheck checks the `slack` object of a channel: Slack's own IDs.
func slackChannelCheck(workspaceID, channelID string) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"workspace_id": knownvalue.StringExact(workspaceID), "channel_id": knownvalue.StringExact(channelID),
	})
}

func attrChecks(addr string, checks map[string]knownvalue.Check) []statecheck.StateCheck {
	out := make([]statecheck.StateCheck, 0, len(checks))
	for k, c := range checks {
		out = append(out, statecheck.ExpectKnownValue(addr, tfjsonpath.New(k), c))
	}
	return out
}

func at(minute int) string { return fmt.Sprintf("2026-10-19T16:%02d:00Z", minute) }

func noChannel(f *claudeTagFake) func(*terraform.State) error {
	return func(*terraform.State) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if l := f.findSlackLocked("slack_channel", slackTeam, slackChan); l != nil {
			return fmt.Errorf("channel %s is still set up after destroy", l.id)
		}
		return nil
	}
}

// TestSlackChannelResource_lifecycle runs the guide's version-control
// workflow through Terraform: set the channel up with two settings, import
// it, change two settings and drop one (only those are sent, the dropped one
// as null), revert a person's change made in claude.ai, set it up again after
// it was removed in Slack, turn Claude off and remove it.
func TestSlackChannelResource_lifecycle(t *testing.T) {
	t.Parallel()
	f := newClaudeTagFake(t, 0)
	f.putWorkspace(slackTeam, map[string]any{"instructions": "Answer in English unless asked otherwise."}) // slws_0001, 16:01
	f.putEnvironment(environment)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		CheckDestroy:             noChannel(f),
		Steps: []resource.TestStep{
			{
				Config:           channelConfig(`enabled = true`, `instructions = "Answer in the language of the question."`),
				ConfigPlanChecks: expectAction(channelAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: channelState("slch_0002", at(2), at(2), map[string]knownvalue.Check{
					"enabled": knownvalue.Bool(true), "instructions": knownvalue.StringExact("Answer in the language of the question."),
				}),
			},
			{ResourceName: channelAddr, ImportState: true, ImportStateVerify: true},
			{
				Config:           channelConfig(`enabled = true`, `model = "claude-opus-5-5"`, `environment_id = "`+environment+`"`),
				ConfigPlanChecks: expectAction(channelAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: channelState("slch_0002", at(2), at(3), map[string]knownvalue.Check{
					"enabled": knownvalue.Bool(true), "model": knownvalue.StringExact("claude-opus-5-5"),
					"environment_id": knownvalue.StringExact(environment),
				}),
			},
			{
				// A person opens the channel to guests in claude.ai (read-only
				// mode off): the refresh shows it, and the apply clears it.
				PreConfig: func() {
					if !f.personSets("slch_0002", "guest_policy", map[string]any{"type": "allow"}) {
						t.Error("claude.ai refused a change with read-only mode off")
					}
				},
				Config:           channelConfig(`enabled = true`, `model = "claude-opus-5-5"`, `environment_id = "`+environment+`"`),
				ConfigPlanChecks: expectAction(channelAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: channelState("slch_0002", at(2), at(5), map[string]knownvalue.Check{
					"enabled": knownvalue.Bool(true), "model": knownvalue.StringExact("claude-opus-5-5"),
					"environment_id": knownvalue.StringExact(environment),
				}),
			},
			{
				// Deleted in Slack: the stored ID answers 404, listing the
				// workspaces answers 200, so the channel is gone and is set up
				// again, with a new ID.
				PreConfig:        func() { f.removeOutOfBand("slch_0002") },
				Config:           channelConfig(`enabled = true`, `model = "claude-opus-5-5"`, `environment_id = "`+environment+`"`),
				ConfigPlanChecks: expectAction(channelAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: channelState("slch_0003", at(6), at(6), map[string]knownvalue.Check{
					"enabled": knownvalue.Bool(true), "model": knownvalue.StringExact("claude-opus-5-5"),
					"environment_id": knownvalue.StringExact(environment),
				}),
			},
			{
				// Claude Tag removes a channel only while Claude is off in it.
				Config:           channelConfig(`enabled = false`, `model = "claude-opus-5-5"`, `environment_id = "`+environment+`"`),
				ConfigPlanChecks: expectAction(channelAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: channelState("slch_0003", at(6), at(7), map[string]knownvalue.Check{
					"enabled": knownvalue.Bool(false), "model": knownvalue.StringExact("claude-opus-5-5"),
					"environment_id": knownvalue.StringExact(environment),
				}),
			},
		},
	})

	f.AssertQueries(routeListChannels, lookupQuery, lookupQuery)
	f.AssertJSONBodies(routeSetUpChannel,
		`{`+slackBody+`, "settings": {"enabled": true, "instructions": "Answer in the language of the question."}}`,
		`{`+slackBody+`, "settings": {"enabled": true, "model": "claude-opus-5-5", "environment_id": "`+environment+`"}}`)
	f.AssertJSONBodies(routeUpdateChannel,
		`{"settings": {"instructions": null, "model": "claude-opus-5-5", "environment_id": "`+environment+`"}}`,
		`{"settings": {"guest_policy": null}}`,
		`{"settings": {"enabled": false}}`)
	f.AssertCalled(routeDeleteChannel, 1)
	if got := f.Requests(routeDeleteChannel)[0].Path; got != slackChannelsURL+"/slch_0003" {
		t.Errorf("DELETE path = %q, want the second channel's", got)
	}
	probes := f.Requests(routeListWorkspaces)
	if len(probes) == 0 {
		t.Error("the 404 on refresh was not confirmed by listing the workspaces")
	}
	for _, r := range probes {
		if r.Query.Encode() != "limit=1" {
			t.Errorf("probe query = %q, want limit=1", r.Query.Encode())
		}
	}
	for _, r := range f.Requests(routeGetChannel) {
		if r.Query.Has("include_inherited") {
			t.Errorf("the resource read the channel with %q; it compares what the channel stores", r.Query.Encode())
		}
	}
	f.assertRequests()
}

// TestSlackChannelResource_adoptsSetUpChannel: a channel already set up in
// claude.ai is adopted. Only what differs is sent (instructions it stores and
// the configuration drops, as null); a channel that already stores the
// configuration gets no write at all.
func TestSlackChannelResource_adoptsSetUpChannel(t *testing.T) {
	t.Parallel()

	t.Run("differs", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		f.putWorkspace(slackTeam, nil)
		id := f.putChannel(map[string]any{"instructions": "Set in claude.ai.", "model": "claude-opus-5-5"})
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config:           channelConfig(`enabled = false`, `model = "claude-opus-5-5"`),
				ConfigPlanChecks: expectAction(channelAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: channelState(id, at(2), at(3), map[string]knownvalue.Check{
					"enabled": knownvalue.Bool(false), "model": knownvalue.StringExact("claude-opus-5-5"),
				}),
			}},
		})
		f.AssertJSONBodies(routeSetUpChannel, `{`+slackBody+`, "settings": {"enabled": false, "instructions": null}}`)
		f.AssertCalled(routeDeleteChannel, 1)
		f.assertRequests()
	})

	t.Run("same", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		f.putWorkspace(slackTeam, nil)
		id := f.putChannel(map[string]any{"enabled": false})
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config:            channelConfig(`enabled = false`),
				ConfigPlanChecks:  expectAction(channelAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: channelState(id, at(2), at(2), map[string]knownvalue.Check{"enabled": knownvalue.Bool(false)}),
			}},
		})
		f.AssertCalled(routeSetUpChannel, 0)
		f.AssertCalled(routeUpdateChannel, 0)
	})
}

// TestSlackChannelResource_slackIDsReplace: Slack's own IDs name the
// channel, so changing one sets up another channel and removes the first,
// which a channel with Claude off allows (#remove-a-channel).
func TestSlackChannelResource_slackIDsReplace(t *testing.T) {
	t.Parallel()
	const other = "C06QRST7UVW"
	f := newClaudeTagFake(t, 0)
	f.putWorkspace(slackTeam, nil)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		CheckDestroy:             noChannel(f),
		Steps: []resource.TestStep{
			{Config: channelConfig(`enabled = false`), ConfigPlanChecks: expectAction(channelAddr, plancheck.ResourceActionCreate)},
			{
				Config:           strings.Replace(channelConfig(`enabled = false`), slackChan, other, 1),
				ConfigPlanChecks: expectAction(channelAddr, plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(channelAddr, tfjsonpath.New("id"), knownvalue.StringExact("slch_0003")),
					statecheck.ExpectKnownValue(channelAddr, tfjsonpath.New("slack"), slackChannelCheck(slackTeam, other)),
				},
			},
		},
	})
	f.AssertJSONBodies(routeSetUpChannel,
		`{`+slackBody+`, "settings": {"enabled": false}}`,
		`{"slack": {"workspace_id": "`+slackTeam+`", "channel_id": "`+other+`"}, "settings": {"enabled": false}}`)
	var removed []string
	for _, r := range f.Requests(routeDeleteChannel) {
		removed = append(removed, r.Path)
	}
	if want := []string{slackChannelsURL + "/slch_0002", slackChannelsURL + "/slch_0003"}; !slices.Equal(removed, want) {
		t.Errorf("removed %q, want %q", removed, want)
	}
}

// TestSlackChannelResource_instructionsWhiteSpace: the API stores
// instructions trimmed (#settings); a heredoc's final newline is kept in the
// state, so the refresh plans nothing, and a change of white space alone
// sends nothing.
func TestSlackChannelResource_instructionsWhiteSpace(t *testing.T) {
	t.Parallel()
	f := newClaudeTagFake(t, 0)
	f.putWorkspace(slackTeam, nil)
	heredoc := "instructions = <<-EOT\n    Answer in the language of the question.\n  EOT"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           channelConfig(`enabled = false`, heredoc),
				ConfigPlanChecks: expectAction(channelAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue(channelAddr, tfjsonpath.New("instructions"),
					knownvalue.StringExact("Answer in the language of the question.\n"))},
				Check: func(*terraform.State) error {
					if got := f.settingsOf("slch_0002")["instructions"]; got != "Answer in the language of the question." {
						return fmt.Errorf("the fake stores %q, want the trimmed text", got)
					}
					return nil
				},
			},
			{
				Config:           channelConfig(`enabled = false`, `instructions = "  Answer in the language of the question."`),
				ConfigPlanChecks: expectAction(channelAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue(channelAddr, tfjsonpath.New("instructions"),
					knownvalue.StringExact("  Answer in the language of the question."))},
			},
		},
	})
	f.AssertJSONBodies(routeSetUpChannel,
		`{`+slackBody+`, "settings": {"enabled": false, "instructions": "Answer in the language of the question.\n"}}`)
	f.AssertCalled(routeUpdateChannel, 0)
}

// TestSlackChannelResource_removal: Claude Tag refuses to remove a channel
// while Claude is on in it (slack_channel_enabled), and the diagnostic says
// how to proceed; a group DM cannot be removed at all; a removal that
// answers 404 is success (#remove-a-channel).
func TestSlackChannelResource_removal(t *testing.T) {
	t.Parallel()

	t.Run("enabled", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		f.putWorkspace(slackTeam, nil)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			CheckDestroy:             noChannel(f),
			Steps: []resource.TestStep{
				{Config: channelConfig(`enabled = true`)},
				{
					Config:  channelConfig(`enabled = true`),
					Destroy: true,
					ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+remove\s+Claude\s+Tag\s+channel\s+slch_0002.*slack_channel_enabled.*` +
						`Set\s+enabled\s+=\s+false\s+on\s+this\s+resource\s+and\s+apply,\s+then\s+destroy\s+it`),
				},
				{Config: channelConfig(`enabled = false`), ConfigPlanChecks: expectAction(channelAddr, plancheck.ResourceActionUpdate)},
			},
		})
		f.AssertCalled(routeDeleteChannel, 2)
	})

	t.Run("inherited enabled", func(t *testing.T) {
		t.Parallel()
		// Nothing stored on the channel: enabled resolves to the built-in
		// default, true, so the removal is refused too.
		f := newClaudeTagFake(t, 0)
		f.putWorkspace(slackTeam, nil)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{
				{Config: channelConfig()},
				{Config: channelConfig(), Destroy: true, ExpectError: regexp.MustCompile(`slack_channel_enabled`)},
				{PreConfig: func() { f.set(func(f *claudeTagFake) { f.defaults["enabled"] = false }) }, Config: channelConfig()},
			},
		})
		f.AssertJSONBodies(routeSetUpChannel, `{`+slackBody+`}`)
	})

	t.Run("group DM", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		f.putWorkspace(slackTeam, nil)
		id := f.putChannel(map[string]any{"enabled": false})
		f.set(func(f *claudeTagFake) { f.findLocked("", id).groupDM = true })
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{
				{Config: channelConfig(`enabled = false`)},
				{
					Config: channelConfig(`enabled = false`), Destroy: true,
					ExpectError: regexp.MustCompile(`(?s)API\s+error\s+\(400\s+invalid_request_error\).*A\s+group\s+direct\s+message`),
				},
				{
					PreConfig: func() { f.set(func(f *claudeTagFake) { f.findLocked("", id).groupDM = false }) },
					Config:    channelConfig(`enabled = false`),
				},
			},
		})
	})

	t.Run("404", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		f.putWorkspace(slackTeam, nil)
		config := channelConfig(`enabled = false`)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{
				{Config: config},
				{PreConfig: func() { f.FailNext(routeDeleteChannel, http.StatusNotFound, 1) }, Config: config, Destroy: true},
			},
		})
		f.AssertCalled(routeDeleteChannel, 1)
	})
}

// TestSlackChannelResource_runnerPool: an environment_id holding a ccpool_
// runner pool cannot be changed, cleared or set through the API (#settings).
// The provider says so before sending anything; declaring the pool keeps it.
func TestSlackChannelResource_runnerPool(t *testing.T) {
	t.Parallel()
	f := newClaudeTagFake(t, 0)
	f.putWorkspace(slackTeam, nil)
	f.putEnvironment(environment)
	id := f.putChannel(map[string]any{"environment_id": "ccpool_01runner"})
	stored := regexp.MustCompile(`(?s)Runner\s+Pool\s+Cannot\s+Be\s+Changed.*stores\s+the\s+self-hosted\s+runner\s+pool\s+ccpool_01runner.*` +
		`Declare\s+environment_id\s+=\s+"ccpool_01runner"`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: channelConfig(`enabled = false`), ExpectError: stored},
			{
				Config:           channelConfig(`enabled = false`, `environment_id = "ccpool_01runner"`),
				ConfigPlanChecks: expectAction(channelAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: channelState(id, at(2), at(3), map[string]knownvalue.Check{
					"enabled": knownvalue.Bool(false), "environment_id": knownvalue.StringExact("ccpool_01runner"),
				}),
			},
			{Config: channelConfig(`enabled = false`, `environment_id = "`+environment+`"`), ExpectError: stored},
			{
				// The channel holds configuration the API does not manage, so it
				// cannot be removed here either (#remove-a-channel).
				Config: channelConfig(`enabled = false`, `environment_id = "ccpool_01runner"`), Destroy: true,
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+remove.*a\s+runner\s+pool`),
			},
			{
				// An owner moves the channel to an environment in claude.ai.
				PreConfig: func() {
					f.set(func(f *claudeTagFake) { f.findLocked("", id).settings["environment_id"] = environment })
				},
				Config:      channelConfig(`enabled = false`, `environment_id = "ccpool_02other"`),
				ExpectError: regexp.MustCompile(`(?s)environment_id\s+ccpool_02other\s+is\s+a\s+self-hosted\s+runner\s+pool,\s+which\s+the\s+Claude\s+Tag\s+API\s+cannot\s+set`),
			},
			{Config: channelConfig(`enabled = false`, `environment_id = "`+environment+`"`)},
		},
	})
	f.AssertJSONBodies(routeSetUpChannel, `{`+slackBody+`, "settings": {"enabled": false}}`)
	f.AssertCalled(routeUpdateChannel, 0)
	f.AssertCalled(routeDeleteChannel, 2)
	f.assertRequests()
}

// TestSlackChannelResource_refusals: the organization-level refusals the
// guide documents (#error-responses) reach the user with what to do.
func TestSlackChannelResource_refusals(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		route, code string
		status      int
		want        string
	}{
		"writes not enabled": {routeSetUpChannel, "claude_tag_writes_not_enabled", http.StatusForbidden, `can\s+read\s+through\s+the\s+Claude\s+Tag\s+API\s+but\s+is\s+not\s+enabled\s+for\s+changes`},
		"compliance":         {routeSetUpChannel, "compliance_configuration_restricted", http.StatusForbidden, `compliance\s+configuration.*stores\s+nothing\s+new`},
		"console key":        {routeListChannels, "organization_level_scoped_api_key_required", http.StatusForbidden, `Claude\s+Enterprise\s+scoped\s+Admin\s+API\s+key`},
		"scope":              {routeListChannels, "api_key_scope_missing", http.StatusForbidden, `read:claude_tag\s+for\s+reads,\s+write:claude_tag\s+for\s+changes`},
		"archived env":       {routeSetUpChannel, "environment_archived", http.StatusBadRequest, `archived\s+environment`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newClaudeTagFake(t, 0)
			f.putWorkspace(slackTeam, nil)
			f.refuseNext(tc.route, tc.status, tc.code)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps:                    []resource.TestStep{{Config: channelConfig(`enabled = true`), ExpectError: regexp.MustCompile(`(?s)` + tc.code + `.*` + tc.want)}},
			})
		})
	}

	t.Run("organization not enabled for changes", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		f.putWorkspace(slackTeam, nil)
		f.set(func(f *claudeTagFake) { f.writesDisabled = true })
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config:      channelConfig(`enabled = true`),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+set\s+up\s+Claude\s+Tag\s+channel\s+C05HJKL6MNP:\s+API\s+error\s+\(403\s+permission_error\).*not\s+enabled\s+for\s+changes`),
			}},
		})
		f.AssertCalled(routeListChannels, 1) // reads still work
		f.AssertCalled(routeSetUpChannel, 1)
	})
}

// TestSlackChannelResource_readOnlyMode: read-only mode makes claude.ai and
// Slack refuse a person's changes, not the API (#read-only-mode). With it
// on, the provider's writes go through and nobody else changes the channel;
// with it off, a person's change is drift the next apply reverts, sending
// only that setting.
func TestSlackChannelResource_readOnlyMode(t *testing.T) {
	t.Parallel()
	f := newClaudeTagFake(t, 0)
	f.putWorkspace(slackTeam, nil)
	f.set(func(f *claudeTagFake) { f.readOnlyMode = true })
	config := channelConfig(`enabled = false`, `model = "claude-opus-5-5"`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: expectAction(channelAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig: func() {
					if f.personSets("slch_0002", "model", "claude-sonnet-5-5") {
						t.Error("claude.ai accepted a person's change with read-only mode on")
					}
				},
				Config:   config,
				PlanOnly: true,
			},
			{
				PreConfig: func() {
					f.set(func(f *claudeTagFake) { f.readOnlyMode = false })
					if !f.personSets("slch_0002", "model", "claude-sonnet-5-5") {
						t.Error("claude.ai refused a person's change with read-only mode off")
					}
				},
				Config:           config,
				ConfigPlanChecks: expectAction(channelAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(channelAddr, tfjsonpath.New("model"), knownvalue.StringExact("claude-opus-5-5")),
				},
			},
		},
	})
	f.AssertJSONBodies(routeSetUpChannel, `{`+slackBody+`, "settings": {"enabled": false, "model": "claude-opus-5-5"}}`)
	f.AssertJSONBodies(routeUpdateChannel, `{"settings": {"model": "claude-opus-5-5"}}`)
}

// TestSlackChannelResource_setUpThenRefused: setting up and applying
// settings are two steps; when settings are refused after a new setup, the
// channel stays set up (slack_channel_set_up_*, its ID in details), and the
// next apply adopts it (#set-up-a-channel).
func TestSlackChannelResource_setUpThenRefused(t *testing.T) {
	t.Parallel()
	f := newClaudeTagFake(t, 0)
	f.putWorkspace(slackTeam, nil)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: channelConfig(`enabled = false`, `environment_id = "`+environment+`"`),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+set\s+up\s+Claude\s+Tag\s+channel\s+C05HJKL6MNP:.*slack_channel_set_up_environment_not_found,\s+channel\s+slch_0002\).*` +
					`next\s+apply\s+finds\s+the\s+channel\s+and\s+adopts\s+it`),
			},
			{
				PreConfig:        func() { f.putEnvironment(environment) },
				Config:           channelConfig(`enabled = false`, `environment_id = "`+environment+`"`),
				ConfigPlanChecks: expectAction(channelAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(channelAddr, tfjsonpath.New("id"), knownvalue.StringExact("slch_0002")),
				},
			},
		},
	})
	body := `{` + slackBody + `, "settings": {"enabled": false, "environment_id": "` + environment + `"}}`
	f.AssertJSONBodies(routeSetUpChannel, body, body)
}

// TestSlackChannelResource_apiUnavailable: a 404 on refresh is not taken for
// a removed channel when listing the workspaces answers 404 too: every
// endpoint then reads as nonexistent (no beta, API not enabled), and the
// state keeps the channel (#ids, #error-responses).
func TestSlackChannelResource_apiUnavailable(t *testing.T) {
	t.Parallel()
	f := newClaudeTagFake(t, 0)
	f.putWorkspace(slackTeam, nil)
	config := channelConfig(`enabled = false`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() {
					f.FailNext(routeGetChannel, http.StatusNotFound, 1)
					f.FailNext(routeListWorkspaces, http.StatusNotFound, 1)
				},
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)Claude\s+Tag\s+API\s+Unavailable.*does\s+not\s+say\s+the\s+Claude\s+Tag\s+channel\s+slch_0002\s+is\s+gone`),
			},
			{
				PreConfig:   func() { f.FailNext(routeGetChannel, http.StatusForbidden, 1) },
				Config:      config,
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+Claude\s+Tag\s+channel\s+slch_0002:\s+API\s+error\s+\(403`),
			},
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		},
	})
	f.AssertCalled(routeSetUpChannel, 1)
}

// TestSlackChannelResource_updateErrors: a refused update and a setup the
// app cannot make are reported with the guide's explanation.
func TestSlackChannelResource_updateErrors(t *testing.T) {
	t.Parallel()

	t.Run("update", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		f.putWorkspace(slackTeam, nil)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{
				{Config: channelConfig(`enabled = false`)},
				{
					Config:      channelConfig(`enabled = false`, `environment_id = "env_011CZunknown"`),
					ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+update\s+the\s+Claude\s+Tag\s+settings\s+of\s+the\s+channel.*environment_not_found.*active\s+environment`),
				},
			},
		})
	})

	t.Run("app not installed", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config:      channelConfig(`enabled = false`),
				ExpectError: regexp.MustCompile(`(?s)API\s+error\s+\(404\s+not_found_error\).*Claude's\s+Slack\s+app\s+confirms\s+with\s+Slack`),
			}},
		})
		// The lookup's 404 (a workspace not set up) leads to the setup.
		f.AssertCalled(routeListChannels, 1)
		f.AssertCalled(routeSetUpChannel, 1)
	})

	t.Run("lookup", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		f.putWorkspace(slackTeam, nil)
		f.FailNext(routeListChannels, http.StatusForbidden, 1)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config:      channelConfig(`enabled = false`),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+look\s+up\s+Claude\s+Tag\s+channel\s+C05HJKL6MNP`),
			}},
		})
		f.AssertCalled(routeSetUpChannel, 0)
	})
}

// TestSlackChannelResource_invalid: values the API refuses with a 400
// (#settings, #ids) fail at plan, before any request.
func TestSlackChannelResource_invalid(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 8193)
	for name, tc := range map[string]struct{ config, err string }{
		"blank instructions": {channelConfig(`instructions = "  \n "`), `Blank\s+instructions`},
		"long instructions":  {channelConfig(`instructions = "` + long + `"`), `at\s+most\s+8192\s+characters.*these\s+hold\s+8193`},
		"long model":         {channelConfig(`model = "` + strings.Repeat("m", 129) + `"`), `string\s+length\s+must\s+be\s+between\s+1\s+and\s+128`},
		"guest policy":       {channelConfig(`guest_policy = "everyone"`), `(?s)guest_policy.*value\s+must\s+be\s+one\s+of`},
		"environment":        {channelConfig(`environment_id = "wrkspc_01"`), `must\s+be\s+an\s+environment\s+ID`},
		"channel ID":         {strings.Replace(channelConfig(), slackChan, "slch_0002", 1), `must\s+be\s+Slack's\s+own\s+channel\s+ID`},
		"workspace ID":       {strings.Replace(channelConfig(), slackTeam, "t01abcd2efg", 1), `must\s+be\s+Slack's\s+own\s+workspace\s+ID`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newClaudeTagFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps:                    []resource.TestStep{{Config: tc.config, ExpectError: regexp.MustCompile(`(?s)` + tc.err)}},
			})
			f.AssertCalled("", 0)
		})
	}

	t.Run("trimmed instructions at the limit", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		f.putWorkspace(slackTeam, nil)
		text := "  " + strings.Repeat("é", 8192) + "\n"
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config: channelConfig(`enabled = false`, fmt.Sprintf("instructions = %q", text)),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(channelAddr, tfjsonpath.New("instructions"), knownvalue.StringExact(text)),
				},
			}},
		})
	})

	t.Run("import ID", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config: channelConfig(), ResourceName: channelAddr, ImportState: true, ImportStateId: slackChan,
				ExpectError: regexp.MustCompile(`Expected\s+the\s+channel's\s+Claude\s+Tag\s+ID\s+\(prefixed\s+slch_\)`),
			}},
		})
		f.AssertCalled("", 0)
	})
}
