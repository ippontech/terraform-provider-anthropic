// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package claudetag_test

import (
	"maps"
	"net/http"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

func dataConfig(typ, body string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) + "data \"" + typ + "\" \"test\" {\n" + body + "}\n"
}

// distinctQueries returns the encoded queries of the requests to route,
// once each, sorted: a data source is read a number of times that belongs
// to terraform-plugin-testing.
func (f *claudeTagFake) distinctQueries(route string) []string {
	var out []string
	for _, r := range f.Requests(route) {
		if q := r.Query.Encode(); !slices.Contains(out, q) {
			out = append(out, q)
		}
	}
	slices.Sort(out)
	return out
}

func bundleCheck(id, name string, description knownvalue.Check, minute int) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"id": knownvalue.StringExact(id), "display_name": knownvalue.StringExact(name), "description": description,
		"created_at": knownvalue.StringExact(at(minute)), "updated_at": knownvalue.StringExact(at(minute)),
	})
}

// TestBundlesDataSource_pagesEmptyAndError lists three Bundles one per page,
// newest first, following next_page to null (#pagination, #list-bundles),
// then an empty organization, then a refused list.
func TestBundlesDataSource_pagesEmptyAndError(t *testing.T) {
	t.Parallel()
	const addr = "data.anthropic_claude_tag_bundles.test"

	t.Run("pages", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 1)
		f.putBundle("capp_support", "Support tools", "Issue tracker and the support runbooks repository.") // 16:01
		f.putBundle("capp_github", "GitHub", nil)                                                          // 16:02
		f.putBundle("capp_monitoring", "Monitoring", "Dashboards.")                                        // 16:03
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config: dataConfig("anthropic_claude_tag_bundles", ""),
				ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue(addr, tfjsonpath.New("bundles"), knownvalue.ListExact([]knownvalue.Check{
					bundleCheck("capp_monitoring", "Monitoring", knownvalue.StringExact("Dashboards."), 3),
					bundleCheck("capp_github", "GitHub", knownvalue.Null(), 2),
					bundleCheck("capp_support", "Support tools", knownvalue.StringExact("Issue tracker and the support runbooks repository."), 1),
				}))},
			}},
		})
		f.AssertWalks(routeListBundles, "limit=100", "limit=100&page="+pageToken("capp_monitoring"), "limit=100&page="+pageToken("capp_github"))
		f.AssertAllPagesRead(routeListBundles)
		f.assertRequests()
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config: dataConfig("anthropic_claude_tag_bundles", ""),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("bundles"), knownvalue.ListExact([]knownvalue.Check{})),
				},
			}},
		})
	})

	t.Run("error", func(t *testing.T) {
		t.Parallel()
		f := newClaudeTagFake(t, 0)
		f.refuseNext(routeListBundles, http.StatusForbidden, "api_key_scope_missing")
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config:      dataConfig("anthropic_claude_tag_bundles", ""),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+Claude\s+Tag\s+bundles.*read:claude_tag\s+for\s+reads`),
			}},
		})
	})
}

// TestBundleDataSource_readAndNotFound reads a Bundle with a description and
// one without (null, #bundles), then fails on one that does not exist.
func TestBundleDataSource_readAndNotFound(t *testing.T) {
	t.Parallel()
	f := newClaudeTagFake(t, 0)
	f.putBundle("capp_support", "Support tools", "Runbooks.") // 16:01
	f.putBundle("capp_github", "GitHub", nil)                 // 16:02
	const addr = "data.anthropic_claude_tag_bundle.test"
	check := func(id, name string, description knownvalue.Check, minute int) []statecheck.StateCheck {
		return attrChecks(addr, map[string]knownvalue.Check{
			"id": knownvalue.StringExact(id), "display_name": knownvalue.StringExact(name), "description": description,
			"created_at": knownvalue.StringExact(at(minute)), "updated_at": knownvalue.StringExact(at(minute)),
		})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:            dataConfig("anthropic_claude_tag_bundle", "  id = \"capp_support\"\n"),
				ConfigStateChecks: check("capp_support", "Support tools", knownvalue.StringExact("Runbooks."), 1),
			},
			{
				Config:            dataConfig("anthropic_claude_tag_bundle", "  id = \"capp_github\"\n"),
				ConfigStateChecks: check("capp_github", "GitHub", knownvalue.Null(), 2),
			},
			{
				Config:      dataConfig("anthropic_claude_tag_bundle", "  id = \"capp_gone\"\n"),
				ExpectError: regexp.MustCompile(`(?s)Claude\s+Tag\s+Bundle\s+Not\s+Found.*No\s+Bundle\s+capp_gone`),
			},
			{
				PreConfig:   func() { f.FailNext(routeGetBundle, http.StatusForbidden, 1) },
				Config:      dataConfig("anthropic_claude_tag_bundle", "  id = \"capp_support\"\n"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+Claude\s+Tag\s+bundle\s+capp_support`),
			},
			{
				Config:      dataConfig("anthropic_claude_tag_bundle", "  id = \"support\"\n"),
				ExpectError: regexp.MustCompile(`must\s+be\s+a\s+Bundle\s+ID`),
			},
			{Config: dataConfig("anthropic_claude_tag_bundle", "  id = \"capp_github\"\n")},
		},
	})
	f.assertRequests()
}

func workspaceCheck(id, slackID string, minute int, settings map[string]knownvalue.Check) knownvalue.Check {
	checks := map[string]knownvalue.Check{
		"id": knownvalue.StringExact(id), "slack": slackWorkspaceCheck(slackID),
		"created_at": knownvalue.StringExact(at(minute)), "updated_at": knownvalue.StringExact(at(minute)),
		"enabled": knownvalue.Null(), "instructions": knownvalue.Null(), "model": knownvalue.Null(),
		"environment_id": knownvalue.Null(), "guest_policy": knownvalue.Null(),
	}
	maps.Copy(checks, settings)
	return knownvalue.ObjectExact(checks)
}

// TestSlackWorkspacesDataSource lists two workspaces one per page with what
// each stores, then with include_inherited what applies in each (Slack
// defaults merged, built-in defaults filled, #stored-and-inherited-values),
// then finds one by Slack's ID, one that is not set up (empty list), and one
// the organization does not own (404, #list-workspaces).
func TestSlackWorkspacesDataSource(t *testing.T) {
	t.Parallel()
	const addr = "data.anthropic_claude_tag_slack_workspaces.test"
	f := newClaudeTagFake(t, 1)
	f.set(func(f *claudeTagFake) { f.defaults = map[string]any{"enabled": false, "instructions": "Org text."} })
	f.putWorkspace(slackTeam, map[string]any{"instructions": "WS text.", "model": "claude-opus-5-5"}) // slws_0001, 16:01
	f.putWorkspace("T02SECOND", map[string]any{"guest_policy": map[string]any{"type": "allow"}})      // slws_0002, 16:02
	f.own("T03NOTSETUP")
	list := func(checks ...knownvalue.Check) []statecheck.StateCheck {
		return []statecheck.StateCheck{statecheck.ExpectKnownValue(addr, tfjsonpath.New("slack_workspaces"), knownvalue.ListExact(checks))}
	}
	first := workspaceCheck("slws_0001", slackTeam, 1, map[string]knownvalue.Check{
		"instructions": knownvalue.StringExact("WS text."), "model": knownvalue.StringExact("claude-opus-5-5"),
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: dataConfig("anthropic_claude_tag_slack_workspaces", ""),
				ConfigStateChecks: list(
					workspaceCheck("slws_0002", "T02SECOND", 2, map[string]knownvalue.Check{"guest_policy": knownvalue.StringExact("allow")}),
					first),
			},
			{
				Config: dataConfig("anthropic_claude_tag_slack_workspaces", "  include_inherited = true\n"),
				ConfigStateChecks: list(
					workspaceCheck("slws_0002", "T02SECOND", 2, map[string]knownvalue.Check{
						"enabled": knownvalue.Bool(false), "instructions": knownvalue.StringExact("Org text."),
						"guest_policy": knownvalue.StringExact("allow"),
					}),
					workspaceCheck("slws_0001", slackTeam, 1, map[string]knownvalue.Check{
						"enabled": knownvalue.Bool(false), "instructions": knownvalue.StringExact("Org text.\n\nWS text."),
						"model": knownvalue.StringExact("claude-opus-5-5"), "guest_policy": knownvalue.StringExact("restrict"),
					})),
			},
			{
				Config:            dataConfig("anthropic_claude_tag_slack_workspaces", "  slack = { workspace_id = \""+slackTeam+"\" }\n"),
				ConfigStateChecks: list(first),
			},
			{
				Config:            dataConfig("anthropic_claude_tag_slack_workspaces", "  slack = { workspace_id = \"T03NOTSETUP\" }\n"),
				ConfigStateChecks: list(),
			},
			{
				Config:      dataConfig("anthropic_claude_tag_slack_workspaces", "  slack = { workspace_id = \"T04ELSEWHERE\" }\n"),
				ExpectError: regexp.MustCompile(`(?s)API\s+error\s+\(404.*does\s+not\s+own\s+Slack\s+workspace\s+T04ELSEWHERE`),
			},
			{
				PreConfig:   func() { f.FailNext(routeListWorkspaces, http.StatusForbidden, 1) },
				Config:      dataConfig("anthropic_claude_tag_slack_workspaces", ""),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+list\s+Claude\s+Tag\s+Slack\s+workspaces`),
			},
		},
	})
	want := []string{
		"include_inherited=true&limit=100", "include_inherited=true&limit=100&page=" + pageToken("slws_0002"),
		"limit=100", "limit=100&page=" + pageToken("slws_0002"),
		"limit=100&slack_workspace_id=T01ABCD2EFG", "limit=100&slack_workspace_id=T03NOTSETUP", "limit=100&slack_workspace_id=T04ELSEWHERE",
	}
	if got := f.distinctQueries(routeListWorkspaces); !slices.Equal(got, want) {
		t.Errorf("queries\n got: %q\nwant: %q", got, want)
	}
	f.AssertAllPagesRead(routeListWorkspaces)
	f.assertRequests()
}

// TestSlackChannelDataSource reads a channel by its slch_ ID (what it
// stores), by its Slack IDs with include_inherited (what applies: every
// level's instructions, broadest first, #stored-and-inherited-values), then
// fails on a channel that is not set up, an unknown ID and a workspace the
// organization does not own.
func TestSlackChannelDataSource(t *testing.T) {
	t.Parallel()
	const addr = "data.anthropic_claude_tag_slack_channel.test"
	f := newClaudeTagFake(t, 0)
	f.set(func(f *claudeTagFake) { f.defaults = map[string]any{"enabled": false, "instructions": "Org text."} })
	f.putWorkspace(slackTeam, map[string]any{"instructions": "WS text."}) // slws_0001, 16:01
	f.putChannel(map[string]any{                                          // slch_0002, 16:02
		"enabled": true, "instructions": "Channel text.", "guest_policy": map[string]any{"type": "channel_only"},
	})
	byIDs := slackBlock(slackTeam, slackChan)
	state := func(settings map[string]knownvalue.Check) []statecheck.StateCheck {
		checks := map[string]knownvalue.Check{
			"id": knownvalue.StringExact("slch_0002"), "slack": slackChannelCheck(slackTeam, slackChan),
			"created_at": knownvalue.StringExact(at(2)),
			"updated_at": knownvalue.StringExact(at(2)), "model": knownvalue.Null(), "environment_id": knownvalue.Null(),
			"enabled": knownvalue.Bool(true), "guest_policy": knownvalue.StringExact("channel_only"),
		}
		maps.Copy(checks, settings)
		return attrChecks(addr, checks)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:            dataConfig("anthropic_claude_tag_slack_channel", "  id = \"slch_0002\"\n"),
				ConfigStateChecks: state(map[string]knownvalue.Check{"instructions": knownvalue.StringExact("Channel text."), "include_inherited": knownvalue.Null()}),
			},
			{
				Config: dataConfig("anthropic_claude_tag_slack_channel", byIDs+"  include_inherited = true\n"),
				ConfigStateChecks: state(map[string]knownvalue.Check{
					"instructions": knownvalue.StringExact("Org text.\n\nWS text.\n\nChannel text."), "include_inherited": knownvalue.Bool(true),
				}),
			},
			{
				Config:      dataConfig("anthropic_claude_tag_slack_channel", slackBlock(slackTeam, "C09NOTSETUP")),
				ExpectError: regexp.MustCompile(`(?s)Claude\s+Tag\s+Channel\s+Not\s+Found.*Slack\s+channel\s+C09NOTSETUP\s+of\s+Slack\s+workspace\s+T01ABCD2EFG\s+is\s+not\s+set\s+up`),
			},
			{
				Config:      dataConfig("anthropic_claude_tag_slack_channel", "  id = \"slch_9999\"\n"),
				ExpectError: regexp.MustCompile(`(?s)Claude\s+Tag\s+Channel\s+Not\s+Found.*No\s+set-up\s+Claude\s+Tag\s+channel\s+slch_9999`),
			},
			{
				Config:      dataConfig("anthropic_claude_tag_slack_channel", slackBlock("T04ELSEWHERE", slackChan)),
				ExpectError: regexp.MustCompile(`(?s)Claude\s+Tag\s+Channel\s+Not\s+Found.*not\s+the\s+organization's`),
			},
			{
				PreConfig:   func() { f.FailNext(routeGetChannel, http.StatusForbidden, 1) },
				Config:      dataConfig("anthropic_claude_tag_slack_channel", "  id = \"slch_0002\"\n"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+Claude\s+Tag\s+channel\s+slch_0002`),
			},
		},
	})
	if got, want := f.distinctQueries(routeGetChannel), []string{""}; !slices.Equal(got, want) {
		t.Errorf("GET channel queries = %q, want %q (stored settings, no include_inherited)", got, want)
	}
	want := []string{
		"include_inherited=true&slack_channel_id=C05HJKL6MNP&slack_workspace_id=T01ABCD2EFG",
		"slack_channel_id=C05HJKL6MNP&slack_workspace_id=T04ELSEWHERE",
		"slack_channel_id=C09NOTSETUP&slack_workspace_id=T01ABCD2EFG",
	}
	if got := f.distinctQueries(routeListChannels); !slices.Equal(got, want) {
		t.Errorf("lookup queries\n got: %q\nwant: %q", got, want)
	}
	f.assertRequests()
}

// TestSlackChannelDataSource_invalid: the channel is named by its ID or by
// both Slack IDs under slack, never both ways and never half, and Slack IDs
// of the wrong form fail, before any request.
func TestSlackChannelDataSource_invalid(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ body, err string }{
		"both ways":       {"  id = \"slch_0002\"\n" + slackBlock(slackTeam, slackChan), `Invalid\s+Attribute\s+Combination`},
		"half":            {"  slack = { channel_id = \"" + slackChan + "\" }\n", `(?s)attribute\s+"workspace_id"\s+is\s+required`},
		"neither":         {"", `Missing\s+Attribute\s+Configuration`},
		"Slack channel":   {slackBlock(slackTeam, "slch_0002"), `must\s+be\s+Slack's\s+own\s+channel\s+ID`},
		"Slack workspace": {slackBlock("slws_0001", slackChan), `must\s+be\s+Slack's\s+own\s+workspace\s+ID`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newClaudeTagFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:      dataConfig("anthropic_claude_tag_slack_channel", tc.body),
					ExpectError: regexp.MustCompile(tc.err),
				}},
			})
			f.AssertCalled("", 0)
		})
	}
}

// slackBlock is the slack attribute naming a channel by Slack's own IDs.
func slackBlock(workspaceID, channelID string) string {
	return "  slack = {\n    workspace_id = \"" + workspaceID + "\"\n    channel_id   = \"" + channelID + "\"\n  }\n"
}
