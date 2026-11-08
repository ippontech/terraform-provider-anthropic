// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

type liveRateLimit struct {
	Type   string `json:"type"`
	Value  int64  `json:"value"`
	Source *struct {
		Type string `json:"type"`
	} `json:"source"`
}

type liveRateLimitEntry struct {
	RateLimitID string          `json:"rate_limit_id"`
	Limits      []liveRateLimit `json:"limits"`
}

// liveRateLimits lists the first page of a workspace's rate limits, inherited
// ones included, straight from the Admin API.
func liveRateLimits(workspaceID string) ([]liveRateLimitEntry, error) {
	client := admin.NewClient(os.Getenv("ANTHROPIC_ADMIN_API_KEY"))
	body, err := client.DoRequest(context.Background(), "GET",
		"/v1/organizations/workspaces/"+workspaceID+"/rate_limits?include_inherited=true&limit=1000", nil)
	if err != nil {
		return nil, err
	}
	var page struct {
		Data []liveRateLimitEntry `json:"data"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("parse rate limits: %w", err)
	}
	return page.Data, nil
}

// liveSource returns the source of one limiter type of one group, "" when
// the list does not carry it.
func liveSource(workspaceID, rateLimitID, limitType string) (string, error) {
	entries, err := liveRateLimits(workspaceID)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.RateLimitID != rateLimitID {
			continue
		}
		for _, l := range e.Limits {
			if l.Type == limitType && l.Source != nil {
				return l.Source.Type, nil
			}
		}
	}
	return "", nil
}

// waitForSource polls until the type's source is want: the reference does not
// say how soon a list reflects a write.
func waitForSource(workspaceID, rateLimitID, limitType, want string) error {
	var got string
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(5 * time.Second) {
		var err error
		got, err = liveSource(workspaceID, rateLimitID, limitType)
		if err != nil {
			return err
		}
		if got == want || time.Now().After(deadline) {
			break
		}
	}
	if got != want {
		return fmt.Errorf("%s of %s: source = %q after 60s, want %q", limitType, rateLimitID, got, want)
	}
	return nil
}

// inheritedCandidate picks a requests_per_minute limit the workspace inherits
// from the organization, so the test never takes over an existing override.
func inheritedCandidate(t *testing.T, workspaceID string) (rateLimitID string, value int64) {
	t.Helper()
	entries, err := liveRateLimits(workspaceID)
	if err != nil {
		t.Fatalf("list rate limits: %v", err)
	}
	for _, e := range entries {
		for _, l := range e.Limits {
			if l.Type == "requests_per_minute" && l.Source != nil && l.Source.Type == "organization" && l.Value > 0 {
				return e.RateLimitID, l.Value
			}
		}
	}
	t.Skip("skipping: no inherited requests_per_minute limit on the test workspace")
	return "", 0
}

// The live test writes a rate-limit override through the Admin API, so it
// runs on the second test workspace, and with a value that changes nothing: it
// sets the override to the value the workspace already inherits from the
// organization, so an override left behind by an interrupted run limits
// nothing, then destroy clears it.
func TestAccWorkspaceRateLimitResource_inheritedValue(t *testing.T) {
	acctest.PreCheckAdmin(t)
	workspaceID := acctest.TerraformTests2WorkspaceID
	rateLimitID, value := inheritedCandidate(t, workspaceID)
	const name = "anthropic_workspace_rate_limit.test"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			return waitForSource(workspaceID, rateLimitID, "requests_per_minute", "organization")
		},
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "anthropic_workspace_rate_limit" "test" {
  workspace_id  = %q
  rate_limit_id = %q
  limits = {
    requests_per_minute = %d
  }
}
`, workspaceID, rateLimitID, value),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "limits.requests_per_minute", fmt.Sprint(value)),
					resource.TestCheckResourceAttr(name, "id", workspaceID+":"+rateLimitID),
					resource.TestCheckResourceAttrSet(name, "group.id"),
					func(*terraform.State) error {
						return waitForSource(workspaceID, rateLimitID, "requests_per_minute", "workspace")
					},
				),
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateId:     workspaceID + ":" + rateLimitID + ":requests_per_minute",
				ImportStateVerify: true,
			},
		},
	})
}
