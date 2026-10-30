// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills

import (
	"context"
	"errors"
	"io"
	"maps"
	"mime"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

// Fixtures in the beta shape the API serves under anthropic-beta:
// skills-2025-10-02 (field lists from the live probes recorded in #192:
// skills carry display_title/latest_version/source as a string, versions carry
// directory/version). The timestamps keep sub-second digits, so a test fails if
// they are ever re-formatted through time.Time instead of copied verbatim as
// SDK v1.67.0 did.
const (
	fixtureSkillID       = "skill_01AbCdEfGhIjKlMnOpQrStUv"
	fixtureSkillVersion  = "1759178010641129"
	fixtureSkillCreated  = "2025-10-02T12:34:56.789012Z"
	fixtureSkillUpdated  = "2025-10-03T08:00:00.123456Z"
	fixtureVersionID     = "skillver_01AbCdEfGhIjKlMnOpQrStUv"
	fixtureVersionCreate = "2025-10-03T08:00:00.123456Z"

	betaSkillJSON = `{
		"id": "` + fixtureSkillID + `",
		"created_at": "` + fixtureSkillCreated + `",
		"display_title": "Test Skill",
		"latest_version": "` + fixtureSkillVersion + `",
		"source": "custom",
		"type": "skill",
		"updated_at": "` + fixtureSkillUpdated + `"
	}`

	betaSkillVersionJSON = `{
		"id": "` + fixtureVersionID + `",
		"created_at": "` + fixtureVersionCreate + `",
		"description": "For testing.",
		"directory": "test-skill",
		"name": "test-skill",
		"skill_id": "` + fixtureSkillID + `",
		"type": "skill_version",
		"version": "` + fixtureSkillVersion + `"
	}`
)

func TestDecodeBetaSkill_betaShape(t *testing.T) {
	t.Parallel()
	got := decodeBetaSkill(betaSkillJSON)
	want := betaSkill{
		ID:            fixtureSkillID,
		CreatedAt:     fixtureSkillCreated,
		UpdatedAt:     fixtureSkillUpdated,
		DisplayTitle:  "Test Skill",
		LatestVersion: fixtureSkillVersion,
		Source:        "custom",
		Type:          "skill",
	}
	if got != want {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestDecodeBetaSkillVersion_betaShape(t *testing.T) {
	t.Parallel()
	got := decodeBetaSkillVersion(betaSkillVersionJSON)
	want := betaSkillVersion{
		ID:          fixtureVersionID,
		SkillID:     fixtureSkillID,
		Version:     fixtureSkillVersion,
		Name:        "test-skill",
		Description: "For testing.",
		Directory:   "test-skill",
		CreatedAt:   fixtureVersionCreate,
		Type:        "skill_version",
	}
	if got != want {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

// The decoder fills each field the way the SDK v1.67.0 decoder filled a string
// field, so an unexpected payload maps to the same Terraform values as before.
func TestDecodeBetaSkill_fieldsLikeSDKv1_67_0(t *testing.T) {
	t.Parallel()
	got := decodeBetaSkill(`{"id":"skill_x","display_title":null,"latest_version":1759178010641129,"source":{"type":"custom"}}`)
	want := betaSkill{
		ID:            "skill_x",
		LatestVersion: "1759178010641129",
		Source:        `{"type":"custom"}`,
	}
	if got != want {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

// A body that is not a JSON object reads as all-empty, as with SDK v1.67.0.
func TestDecodeBetaSkill_notAnObject(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{``, `null`, `[]`, `"skill"`} {
		if got := decodeBetaSkill(raw); got != (betaSkill{}) {
			t.Errorf("decodeBetaSkill(%q) = %+v, want all empty", raw, got)
		}
		if got := decodeBetaSkillVersion(raw); got != (betaSkillVersion{}) {
			t.Errorf("decodeBetaSkillVersion(%q) = %+v, want all empty", raw, got)
		}
	}
}

func TestSkillsBetas_freshSlice(t *testing.T) {
	t.Parallel()
	a := skillsBetas()
	a[0] = "tampered"
	if b := skillsBetas(); len(b) != 1 || b[0] != anthropic.AnthropicBetaSkills2025_10_02 {
		t.Fatalf("skillsBetas() = %v", b)
	}
}

// betaSkillsServer is an httptest server for the skills endpoints. Every
// request must carry exactly the headers and query SDK v1.67.0 sent: a single
// anthropic-beta: skills-2025-10-02 and ?beta=true. routes maps "METHOD /path"
// to a handler; an unexpected route fails the test.
type betaSkillsServer struct {
	*httptest.Server
	mu    sync.Mutex
	calls []string
}

func newBetaSkillsServer(t *testing.T, routes map[string]http.HandlerFunc) *betaSkillsServer {
	t.Helper()
	s := &betaSkillsServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := r.Method + " " + r.URL.Path
		s.mu.Lock()
		s.calls = append(s.calls, route)
		s.mu.Unlock()
		if got := r.Header.Values("anthropic-beta"); len(got) != 1 || got[0] != "skills-2025-10-02" {
			t.Errorf("%s: anthropic-beta = %q, want exactly [skills-2025-10-02]", route, got)
		}
		if got := r.URL.Query().Get("beta"); got != "true" {
			t.Errorf("%s: query beta = %q, want true", route, got)
		}
		h, ok := routes[route]
		if !ok {
			t.Errorf("unexpected request %s", route)
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *betaSkillsServer) client(t *testing.T) *anthropic.Client {
	t.Helper()
	return oauthtest.NewSDKClient(t, s.Server)
}

func (s *betaSkillsServer) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// multipartCapture records the multipart form of a request, for the test
// goroutine to read once the call returned.
type multipartCapture struct {
	mu     sync.Mutex
	fields map[string][]string
	files  []string
}

// handler records the request's multipart form, then answers body.
func (c *multipartCapture) handler(t *testing.T, body string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		fields, files := multipartParts(t, r)
		c.mu.Lock()
		c.fields, c.files = fields, files
		c.mu.Unlock()
		jsonResponse(body)(w, r)
	}
}

// recorded returns the form fields and the "name=filename" file parts.
func (c *multipartCapture) recorded() (map[string][]string, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fields, c.files
}

func jsonResponse(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// tfObjectValue builds a tftypes object of typ from vals, every other
// attribute null.
func tfObjectValue(typ tftypes.Object, vals map[string]tftypes.Value) tftypes.Value {
	all := make(map[string]tftypes.Value, len(typ.AttributeTypes))
	for name, at := range typ.AttributeTypes {
		all[name] = tftypes.NewValue(at, nil)
	}
	maps.Copy(all, vals)
	return tftypes.NewValue(typ, all)
}

func tfString(s string) tftypes.Value { return tftypes.NewValue(tftypes.String, s) }

// attrGetter is what tfsdk.State and tfsdk.Plan have in common.
type attrGetter interface {
	GetAttribute(ctx context.Context, p path.Path, target any) diag.Diagnostics
}

// assertStringAttrs compares string attributes. A nil want means the attribute
// must be null.
func assertStringAttrs(t *testing.T, g attrGetter, want map[string]*string) {
	t.Helper()
	for name, w := range want {
		var v types.String
		if d := g.GetAttribute(context.Background(), path.Root(name), &v); d.HasError() {
			t.Fatalf("%s: %v", name, d)
		}
		switch {
		case w == nil && !v.IsNull():
			t.Errorf("%s = %s, want null", name, v)
		case w != nil && (v.IsNull() || v.IsUnknown() || v.ValueString() != *w):
			t.Errorf("%s = %s, want %q", name, v, *w)
		}
	}
}

// multipartParts reads a multipart request into its form fields (name to
// values) and "name=filename" for each file part, in order. It runs on the
// server goroutine, so it reports problems with t.Errorf, never t.Fatalf. It reads the
// Content-Disposition header itself: multipart.Part.FileName would strip the
// bundle directory the skills API requires.
func multipartParts(t *testing.T, r *http.Request) (map[string][]string, []string) {
	t.Helper()
	mr, err := r.MultipartReader()
	if err != nil {
		t.Errorf("multipart reader: %v", err)
		return nil, nil
	}
	fields := map[string][]string{}
	var filenames []string
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Errorf("next part: %v", err)
			break
		}
		_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if err != nil {
			t.Errorf("content-disposition: %v", err)
			break
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Errorf("read part: %v", err)
			break
		}
		if filename, ok := params["filename"]; ok {
			filenames = append(filenames, params["name"]+"="+filename)
			continue
		}
		fields[params["name"]] = append(fields[params["name"]], string(body))
	}
	return fields, filenames
}
