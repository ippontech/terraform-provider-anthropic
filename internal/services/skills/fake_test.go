// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills_test

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the Skills API (beta skills-2025-10-02, ?beta=true, standard
// key). References: https://platform.claude.com/docs/en/api/beta/skills/create,
// .../skills/list, .../skills/retrieve, .../skills/delete and the same four
// under .../skills/versions/.
const (
	routeCreateSkill   = "POST /v1/skills"
	routeListSkills    = "GET /v1/skills"
	routeGetSkill      = "GET /v1/skills/{skill_id}"
	routeDeleteSkill   = "DELETE /v1/skills/{skill_id}"
	routeCreateVersion = "POST /v1/skills/{skill_id}/versions"
	routeListVersions  = "GET /v1/skills/{skill_id}/versions"
	routeGetVersion    = "GET /v1/skills/{skill_id}/versions/{version}"
	routeDeleteVersion = "DELETE /v1/skills/{skill_id}/versions/{version}"

	skillsBeta = "skills-2025-10-02"
)

// allRoutes lists every route of the fake, for the beta and credential
// assertions.
var allRoutes = []string{
	routeCreateSkill, routeListSkills, routeGetSkill, routeDeleteSkill,
	routeCreateVersion, routeListVersions, routeGetVersion, routeDeleteVersion,
}

// skillsFake is the Skills API on top of two sdktest collections.
//
// Payloads use the field names of SDK v1.67.0, which the deployed API was
// measured to send (CLAUDE.md, SDK pin note): display_title, latest_version,
// source as a string, and directory and version on a skill version. The
// reference pages have moved ahead (display_name, latest_version_id).
type skillsFake struct {
	*sdktest.Server
	skills   *sdktest.Collection
	versions *sdktest.Collection
	nextVer  atomic.Int64

	mu           sync.Mutex
	descriptions []string // what the next uploads' SKILL.md declare (describeNext)
}

func newSkillsFake(t *testing.T, pageSize int) *skillsFake {
	t.Helper()
	srv := sdktest.NewServer(t)
	f := &skillsFake{
		Server:   srv,
		skills:   srv.NewCollection("skill_"),
		versions: srv.NewCollection("skillver_").KeyedBy("version"),
	}
	f.Handle(routeCreateSkill, f.createSkill)
	f.skills.HandleGet(routeGetSkill)
	f.skills.HandleList(routeListSkills, sdktest.PageToken, pageSize, func(r *http.Request, s map[string]any) bool {
		src := r.URL.Query().Get("source")
		return src == "" || s["source"] == src
	})
	f.Handle(routeDeleteSkill, f.deleteSkill)
	f.Handle(routeCreateVersion, f.createVersion)
	f.versions.HandleGet(routeGetVersion)
	f.versions.HandleList(routeListVersions, sdktest.PageToken, pageSize, nil)
	f.Handle(routeDeleteVersion, f.deleteVersion)
	return f
}

// bundle is what an upload stores: its top-level directory, which the file
// names of the parts carry ("pilot-skill/SKILL.md"), and the name and
// description the API reads from its SKILL.md. The fake does not read the
// files: the name is the directory, as the live API requires
// (TestAccSkillResource_basic comment), and the description the next one a
// test set with describeNext, "" otherwise.
type bundle struct {
	dir, name, description string
	files                  []string
}

// describeNext sets the descriptions the next uploads' SKILL.md declare, in
// upload order.
func (f *skillsFake) describeNext(descriptions ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.descriptions = append(f.descriptions, descriptions...)
}

// readBundle reads an upload: "All files must be in the same top-level
// directory" (create reference), as provretry.MultipartUpload names the
// parts. It returns the form fields too.
func (f *skillsFake) readBundle(r *http.Request) (bundle, map[string]string, error) {
	parts, err := sdktest.ReadParts(r)
	if err != nil {
		return bundle{}, nil, err
	}
	var b bundle
	fields := map[string]string{}
	for _, p := range parts {
		if p.Filename == "" {
			fields[p.Field] = string(p.Content)
			continue
		}
		if p.Field != "files[]" {
			return bundle{}, nil, fmt.Errorf("unexpected file field %q", p.Field)
		}
		dir, _, ok := strings.Cut(p.Filename, "/")
		if !ok || dir == "" || (b.dir != "" && dir != b.dir) {
			return bundle{}, nil, fmt.Errorf("file %q is not in the bundle's top-level directory", p.Filename)
		}
		b.dir = dir
		b.files = append(b.files, p.Filename)
	}
	b.name = b.dir
	f.mu.Lock()
	if len(f.descriptions) > 0 {
		b.description, f.descriptions = f.descriptions[0], f.descriptions[1:]
	}
	f.mu.Unlock()
	return b, fields, nil
}

func (f *skillsFake) newVersion(skillID string, b bundle) map[string]any {
	v := f.versions.Create(map[string]any{
		"type":        "skill_version",
		"skill_id":    skillID,
		"version":     fmt.Sprintf("17591780106411%02d", f.nextVer.Add(1)),
		"name":        b.name,
		"description": b.description,
		"directory":   b.dir,
	})
	if _, ok := f.skills.Get(skillID); ok {
		f.skills.Update(skillID, map[string]any{"latest_version": v["version"]}, sdktest.Replace)
	}
	return v
}

func (f *skillsFake) createSkill(w http.ResponseWriter, r *http.Request) {
	b, fields, err := f.readBundle(r)
	if err != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	// "derived from the SKILL.md frontmatter when omitted" (create
	// reference), which mapSkillNewResponseToState maps back.
	title := b.name
	if v, ok := fields["display_title"]; ok {
		title = v
	}
	s := f.skills.Create(map[string]any{
		"type":           "skill",
		"display_title":  title,
		"source":         "custom",
		"latest_version": "",
	})
	// "Always set: a Skill holds at least one version" (list reference).
	f.newVersion(s["id"].(string), b)
	s, _ = f.skills.Get(s["id"].(string))
	sdktest.WriteJSON(w, http.StatusOK, s)
}

// deleteSkill refuses a skill that still has versions, as the API does: that
// is why SkillResource.Delete deletes the versions first when force_destroy
// is set, and surfaces the refusal otherwise.
func (f *skillsFake) deleteSkill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("skill_id")
	if !f.skills.Has(id) {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "skill "+id+" not found")
		return
	}
	if n := len(f.versionsOf(id)); n > 0 {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", fmt.Sprintf("skill %s still has %d versions; delete them first", id, n))
		return
	}
	f.skills.Delete(id)
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "type": "skill_deleted"})
}

func (f *skillsFake) createVersion(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("skill_id")
	if !f.skills.Has(id) {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "skill "+id+" not found")
		return
	}
	b, _, err := f.readBundle(r)
	if err != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, f.newVersion(id, b))
}

func (f *skillsFake) deleteVersion(w http.ResponseWriter, r *http.Request) {
	v, ok := f.versions.Get(r.PathValue("version"))
	if !ok || v["skill_id"] != r.PathValue("skill_id") {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "version "+r.PathValue("version")+" not found")
		return
	}
	f.versions.Delete(r.PathValue("version"))
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"id": v["id"], "type": "skill_version_deleted"})
}

func (f *skillsFake) versionsOf(skillID string) []map[string]any {
	return f.versions.List(func(v map[string]any) bool { return v["skill_id"] == skillID })
}

// deleteOutOfBand removes a skill and its versions behind Terraform's back.
func (f *skillsFake) deleteOutOfBand(skillID string) {
	for _, v := range f.versionsOf(skillID) {
		f.versions.Delete(v["version"].(string))
	}
	f.skills.Delete(skillID)
}

// checkEmpty is a CheckDestroy: nothing may be left in the fake.
func (f *skillsFake) checkEmpty() error {
	if n, m := f.skills.Len(), f.versions.Len(); n != 0 || m != 0 {
		return fmt.Errorf("%d skills and %d versions left after destroy", n, m)
	}
	return nil
}

// assertRequestShape checks what every request of a test must carry: the
// skills beta, the standard key and ?beta=true, on every route called.
func (f *skillsFake) assertRequestShape(t *testing.T) {
	t.Helper()
	f.AssertShape(sdktest.Shape{Credential: sdktest.APIKey, Beta: skillsBeta, BetaQuery: true}, allRoutes...)
}

// uploadedFiles returns the files ("field=filename") and the other fields
// of the n-th (0-based) multipart request to route.
func (f *skillsFake) uploadedFiles(t *testing.T, route string, n int) ([]string, map[string]string) {
	t.Helper()
	reqs := f.Requests(route)
	if len(reqs) <= n {
		t.Fatalf("%s called %d times, want at least %d", route, len(reqs), n+1)
	}
	parts, err := reqs[n].Parts()
	if err != nil {
		t.Fatalf("%s: %v", route, err)
	}
	var names []string
	fields := map[string]string{}
	for _, p := range parts {
		if p.Filename == "" {
			fields[p.Field] = string(p.Content)
			continue
		}
		names = append(names, p.Field+"="+p.Filename)
	}
	return names, fields
}

// writeBundle writes a skill bundle under a fresh directory named dir: a
// SKILL.md whose frontmatter name is dir, then the extra files (slash
// paths relative to dir). It returns the paths in that order.
func writeBundle(t *testing.T, dir, description string, extra ...string) []string {
	t.Helper()
	root := filepath.Join(t.TempDir(), dir)
	names := append([]string{"SKILL.md"}, extra...)
	paths := make([]string, len(names))
	for i, name := range names {
		content := "content of " + name
		if name == "SKILL.md" {
			content = fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n# %s\n", dir, description, dir)
		}
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		paths[i] = p
	}
	return paths
}

// hclPath returns p as the configuration writes it, and as the state keeps
// it: forward slashes, cleaned.
func hclPath(p string) string {
	return path.Clean(filepath.ToSlash(p))
}

// hclList renders paths as an HCL list of strings.
func hclList(paths []string) string {
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = fmt.Sprintf("%q", hclPath(p))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// writeFile replaces the content of p.
func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
