// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package claudetag

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

func settings(enabled *bool, instructions, model, env, guest string) settingsModel {
	s := nullSettings()
	if enabled != nil {
		s.Enabled = types.BoolValue(*enabled)
	}
	str := func(v string) types.String {
		if v == "" {
			return types.StringNull()
		}
		return types.StringValue(v)
	}
	s.Instructions, s.Model, s.EnvironmentID, s.GuestPolicy = str(instructions), str(model), str(env), str(guest)
	return s
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSettingsPatch: only what differs is sent; a setting the plan drops is
// null (guide, Keep configuration in version control, step 3); instructions
// compare after trimming, as the API stores them.
func TestSettingsPatch(t *testing.T) {
	t.Parallel()
	on, off := true, false
	for name, tc := range map[string]struct {
		plan, stored settingsModel
		want         string
	}{
		"nothing differs":   {settings(&on, "Be brief.", "", "", "allow"), settings(&on, "Be brief.", "", "", "allow"), `{}`},
		"white space":       {settings(nil, "Be brief.\n", "", "", ""), settings(nil, "Be brief.", "", "", ""), `{}`},
		"set from nothing":  {settings(&off, "Hi.", "claude-opus-5-5", "env_01", "channel_only"), nullSettings(), `{"enabled":false,"environment_id":"env_01","guest_policy":{"type":"channel_only"},"instructions":"Hi.","model":"claude-opus-5-5"}`},
		"clear everything":  {nullSettings(), settings(&on, "Hi.", "claude-opus-5-5", "env_01", "allow"), `{"enabled":null,"environment_id":null,"guest_policy":null,"instructions":null,"model":null}`},
		"change two, keep":  {settings(&on, "New.", "claude-opus-5-5", "", "restrict"), settings(&on, "Old.", "claude-opus-5-5", "", "allow"), `{"guest_policy":{"type":"restrict"},"instructions":"New."}`},
		"enabled to false":  {settings(&off, "", "", "", ""), settings(&on, "", "", "", ""), `{"enabled":false}`},
		"instructions gone": {settings(nil, "", "", "", ""), settings(nil, "Hi.", "", "", ""), `{"instructions":null}`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := jsonOf(t, settingsPatch(tc.plan, tc.stored)); got != tc.want {
				t.Errorf("patch = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestRunnerPool: a change that touches a stored runner pool, or sets one,
// cannot be sent (guide, Settings); clearing everything keeps the pool.
func TestRunnerPool(t *testing.T) {
	t.Parallel()
	pool := settings(nil, "Hi.", "", "ccpool_01", "")
	if msg := runnerPoolConflict(settingsPatch(settings(nil, "Bye.", "", "ccpool_01", ""), pool), pool, settings(nil, "Bye.", "", "ccpool_01", ""), "channel"); msg != "" {
		t.Errorf("a change that keeps the pool is refused: %s", msg)
	}
	if msg := runnerPoolConflict(settingsPatch(nullSettings(), pool), pool, nullSettings(), "channel"); !strings.Contains(msg, "cannot change or clear") {
		t.Errorf("clearing a pool: %q", msg)
	}
	plan := settings(nil, "", "", "ccpool_02", "")
	if msg := runnerPoolConflict(settingsPatch(plan, nullSettings()), nullSettings(), plan, "workspace"); !strings.Contains(msg, "cannot set") {
		t.Errorf("setting a pool: %q", msg)
	}
	patch, kept := clearAllPatch(pool)
	if got := jsonOf(t, patch); got != `{"instructions":null}` || kept != "ccpool_01" {
		t.Errorf("clearAllPatch = %s, kept %q", got, kept)
	}
}

// TestSettingsFromAPI: an unrecognized guest_policy type is kept as read
// (guide, Unrecognized values), and the configuration's untrimmed
// instructions survive a trimmed answer.
func TestSettingsFromAPI(t *testing.T) {
	t.Parallel()
	var api settingsAPIObject
	if err := json.Unmarshal([]byte(`{"enabled": null, "instructions": "Be brief.", "model": null, "environment_id": null, "guest_policy": {"type": "members_only"}}`), &api); err != nil {
		t.Fatal(err)
	}
	got := settingsFromAPI(api, settings(nil, "  Be brief.\n", "", "", ""))
	if got.GuestPolicy.ValueString() != "members_only" || got.Instructions.ValueString() != "  Be brief.\n" || !got.Enabled.IsNull() {
		t.Errorf("settingsFromAPI = %+v", got)
	}
	if got := settingsFromAPI(api, nullSettings()); got.Instructions.ValueString() != "Be brief." {
		t.Errorf("without a prior value: %q", got.Instructions.ValueString())
	}
}

func TestInstructionsValidator(t *testing.T) {
	t.Parallel()
	v := instructionsValidator{}
	if !strings.Contains(v.MarkdownDescription(context.Background()), "8192") {
		t.Errorf("description = %q", v.MarkdownDescription(context.Background()))
	}
	for value, wantErr := range map[string]bool{
		"Be brief.":                           false,
		" \t\n":                               true,
		strings.Repeat("ü", 8192):             false,
		" " + strings.Repeat("ü", 8192) + " ": false,
		strings.Repeat("a", 8193):             true,
	} {
		var resp validator.StringResponse
		v.ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringValue(value)}, &resp)
		if resp.Diagnostics.HasError() != wantErr {
			t.Errorf("%d characters: error = %v, want %v", len([]rune(value)), resp.Diagnostics, wantErr)
		}
	}
	var resp validator.StringResponse
	v.ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringUnknown()}, &resp)
	if resp.Diagnostics.HasError() {
		t.Error("an unknown value is refused")
	}
}

// TestListAll: only a null next_page ends a walk, a short or empty page
// does not (guide, Pagination); a cursor sent back as next_page would loop
// and is an error; an answer that is not JSON is an error.
func TestListAll(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewSequenceServer(t,
		sdktest.Response{Body: `{"data": [{"id": "capp_a"}], "next_page": "p1"}`},
		sdktest.Response{Body: `{"data": [], "next_page": "p2"}`},
		sdktest.Response{Body: `{"data": [{"id": "capp_b"}], "next_page": null}`},
	)
	got, err := listAll[bundleAPIObject](context.Background(), admintest.NewClient(t, srv.Server), bundlesPath, nil)
	if err != nil || len(got) != 2 || got[1].ID != "capp_b" {
		t.Fatalf("listAll = %v, %v", got, err)
	}
	srv.AssertQueries("", "limit=100", "limit=100&page=p1", "limit=100&page=p2")

	loop := sdktest.NewSequenceServer(t,
		sdktest.Response{Body: `{"data": [], "next_page": "p1"}`},
		sdktest.Response{Body: `{"data": [], "next_page": "p1"}`},
	)
	if _, err := listAll[bundleAPIObject](context.Background(), admintest.NewClient(t, loop.Server), bundlesPath, nil); err == nil ||
		!strings.Contains(err.Error(), "cannot advance") {
		t.Errorf("a repeated cursor: %v", err)
	}

	bad := sdktest.NewSequenceServer(t, sdktest.Response{Body: `not json`})
	if _, err := listAll[bundleAPIObject](context.Background(), admintest.NewClient(t, bad.Server), bundlesPath, nil); err == nil ||
		!strings.Contains(err.Error(), "unable to parse") {
		t.Errorf("a body that is not JSON: %v", err)
	}
}
