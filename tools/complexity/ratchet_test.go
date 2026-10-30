// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The fixtures in testdata are two trees of a module, base and head. In
// internal/a, every function scores the number of if statements it holds,
// and the tests cap the score at 3. The head tree also holds files out of
// scope, each with a function scoring 6: a test file, a generated file, a
// file under testdata, a test-helper package (internal/sdktest) and a file
// outside internal/.
const fixtureLimit = 3

func fixtureScope() scope {
	return scope{dirs: []string{"internal/"}, exclude: regexp.MustCompile(defaultExclude)}
}

func loadFixture(t *testing.T, name string) map[string]function {
	t.Helper()
	tr, err := dirTree(filepath.Join("testdata", name), fixtureScope())
	if err != nil {
		t.Fatal(err)
	}
	fns, err := scoreTree(tr)
	if err != nil {
		t.Fatal(err)
	}
	return fns
}

func TestScopeIncludes(t *testing.T) {
	sc := fixtureScope()
	for name, want := range map[string]bool{
		"internal/a/a.go":                   true,
		"internal/services/agents/x.go":     true,
		"internal/a/a_test.go":              false,
		"internal/a/a.txt":                  false,
		"internal/a/testdata/x.go":          false,
		"internal/a/.hidden/x.go":           false,
		"internal/a/_skip/x.go":             false,
		"internal/sdktest/server.go":        false,
		"internal/acctest/acctest.go":       false,
		"internal/sdktestextra/x.go":        true,
		"main.go":                           false,
		"tools/complexity/main.go":          false,
		"cmd/internal/a.go":                 false,
		"internal/wifprobetest/probe.go":    false,
		"internal/providertest/provider.go": false,
	} {
		if got := sc.includes(name); got != want {
			t.Errorf("includes(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestScoreTree(t *testing.T) {
	head := loadFixture(t, "head")
	want := map[string]int{
		"internal/a.Untouched":    5,
		"internal/a.Touched":      5,
		"internal/a.TouchedLower": 4,
		"internal/a.Rose":         5,
		"internal/a.Crossed":      4,
		"internal/a.Lowered":      2,
		"internal/a.(*T).Method":  4,
		"internal/a.New":          4,
		"internal/a.Small":        1,
		// gocognit itself skips a function with this directive; the ratchet
		// does not.
		"internal/a.Ignored": 4,
		"internal/a.Moved":   5,
		// Two init functions: told apart by file.
		"internal/a.init in internal/a/a.go": 1,
		"internal/a.init in internal/a/c.go": 1,
	}
	if len(head) != len(want) {
		t.Errorf("scored %d functions, want %d: %v", len(head), len(want), keys(head))
	}
	for key, score := range want {
		fn, ok := head[key]
		if !ok {
			t.Errorf("%s not scored", key)
			continue
		}
		if fn.score != score {
			t.Errorf("%s scores %d, want %d", key, fn.score, score)
		}
	}
	if fn := head["internal/a.Moved"]; fn.file != "internal/a/b.go" || fn.line != 6 {
		t.Errorf("Moved at %s:%d, want internal/a/b.go:6", fn.file, fn.line)
	}
}

func keys(m map[string]function) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestScoreTreeRejectsInvalidGo(t *testing.T) {
	if _, err := scoreTree(tree{"internal/a/a.go": []byte("package a\nfunc {")}); err == nil {
		t.Fatal("no error for a file that does not parse")
	}
}

func TestEvaluate(t *testing.T) {
	r := evaluate(loadFixture(t, "base"), loadFixture(t, "head"), fixtureLimit)
	type want struct {
		score, base int
		state       state
	}
	wants := map[string]want{
		"internal/a.New":          {4, 0, added},
		"internal/a.Ignored":      {4, 0, added},
		"internal/a.Crossed":      {4, 2, crossed},
		"internal/a.Rose":         {5, 4, rose},
		"internal/a.Touched":      {5, 5, touched},
		"internal/a.TouchedLower": {4, 5, touched},
		"internal/a.(*T).Method":  {4, 4, touched},
		"internal/a.Lowered":      {2, 5, lowered},
		"internal/a.Removed":      {0, 5, removed},
		"internal/a.Untouched":    {5, 5, unchanged},
		// Moved to another file of the package, unchanged.
		"internal/a.Moved": {5, 5, unchanged},
	}
	if len(r.entries) != len(wants) {
		t.Errorf("%d entries, want %d: %+v", len(r.entries), len(wants), r.entries)
	}
	for i, e := range r.entries {
		w, ok := wants[e.key]
		if !ok {
			t.Errorf("unexpected entry %+v", e)
			continue
		}
		if got := (want{e.score, e.baseScore, e.state}); got != w {
			t.Errorf("%s: got %+v, want %+v", e.key, got, w)
		}
		if i > 0 && r.entries[i-1].state > e.state {
			t.Errorf("entries not sorted by state: %s before %s", r.entries[i-1].key, e.key)
		}
	}
	if r.passed() || r.failures() != 4 {
		t.Errorf("passed=%v failures=%d, want false and 4", r.passed(), r.failures())
	}
	if r.baseAbove != 8 || r.checked != 13 {
		t.Errorf("baseAbove=%d checked=%d, want 8 and 13", r.baseAbove, r.checked)
	}
}

func TestEvaluateRaisedByRecursion(t *testing.T) {
	// gocognit adds one point to a function that takes part in a recursion
	// cycle: the score of an unchanged function can rise when a change adds
	// the other half of the cycle, and the ratchet catches it.
	base := mustScore(t, tree{"internal/a/a.go": []byte(`package a

func f(x int) int {
	if x > 0 {
		if x > 1 {
			return 1
		}
	}
	return g(x)
}

func g(x int) int { return x }
`)})
	head := mustScore(t, tree{"internal/a/a.go": []byte(`package a

func f(x int) int {
	if x > 0 {
		if x > 1 {
			return 1
		}
	}
	return g(x)
}

func g(x int) int { return f(x - 1) }
`)})
	r := evaluate(base, head, 2)
	if len(r.entries) != 1 || r.entries[0].key != "internal/a.f" || r.entries[0].state != rose {
		t.Fatalf("entries %+v, want f rising", r.entries)
	}
}

func TestEvaluateSameNameSibling(t *testing.T) {
	// A function above the cap and alone of its name at the merge base (an
	// init here) keeps its identity when a change adds a second one in
	// another file, or removes the second one: it reads neither as new nor
	// as gone.
	legacy := []byte(`package a

var x int

func init() {
	if x > 0 {
		if x > 1 {
			if x > 2 {
				x = 0
			}
		}
	}
}
`)
	small := []byte("package a\n\nfunc init() { x = 1 }\n")
	big := []byte("package a\n\nfunc init() {\n\tif x > 0 {\n\t\tif x > 1 {\n\t\t\tx = 2\n\t\t}\n\t}\n}\n")
	alone := mustScore(t, tree{"internal/a/a.go": legacy})
	withSmall := mustScore(t, tree{"internal/a/a.go": legacy, "internal/a/b.go": small})
	for name, tc := range map[string]struct{ base, head map[string]function }{
		"sibling added":   {alone, withSmall},
		"sibling removed": {withSmall, alone},
	} {
		r := evaluate(tc.base, tc.head, 2)
		if !r.passed() || len(r.entries) != 1 || r.entries[0].state != unchanged || r.entries[0].file != "internal/a/a.go" {
			t.Errorf("%s: entries %+v, want only the legacy init, unchanged", name, r.entries)
		}
	}

	// A new sibling above the cap is still new.
	r := evaluate(alone, mustScore(t, tree{"internal/a/a.go": legacy, "internal/a/b.go": big}), 2)
	if r.failures() != 1 || r.count(added) != 1 || r.count(unchanged) != 1 {
		t.Errorf("entries %+v, want the new init added and the legacy one unchanged", r.entries)
	}
}

func mustScore(t *testing.T, tr tree) map[string]function {
	t.Helper()
	fns, err := scoreTree(tr)
	if err != nil {
		t.Fatal(err)
	}
	return fns
}

func TestEvaluateUnchangedTreePasses(t *testing.T) {
	base := loadFixture(t, "base")
	r := evaluate(base, base, fixtureLimit)
	if !r.passed() || len(r.entries) != r.count(unchanged) || r.count(unchanged) != 8 {
		t.Errorf("passed=%v entries=%+v, want 8 unchanged", r.passed(), r.entries)
	}
}

func reportOf(t *testing.T, r result) string {
	t.Helper()
	var out strings.Builder
	rep := report{mergeBase: "0123456789ab", dirs: []string{"internal/"}, exclude: defaultExclude, gocognit: "v1.3.0"}
	if err := writeReport(&out, r, rep); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestReport(t *testing.T) {
	for _, tc := range []struct {
		name          string
		base, head    string // fixture trees, "" for none
		limit         int
		want, notWant []string
	}{
		{
			name: "failures", base: "base", head: "head", limit: fixtureLimit,
			want: []string{
				"**The ratchet fails**: see the functions marked FAIL.",
				"| A new function scores 3 or less | 2 above | **FAIL** |",
				"| A function at 3 or less at the merge base stays at 3 or less | 1 above | **FAIL** |",
				"| A function above 3 at the merge base may change, its score may not rise | 1 rose, 3 changed without rising, 1 brought to 3 or less | **FAIL** |",
				"8 functions score above 3 at the merge base (`0123456789ab`). Once none is left, the cap is absolute.",
				"To fix a failure, split the function",
				"| `internal/a.New` | `internal/a/a.go:",
				" | 4 | - | **FAIL**: new function above 3 |",
				" | 4 | 2 | **FAIL**: was 3 or less at the merge base |",
				" | 5 | 4 | **FAIL**: rose from 4 |",
				" | 5 | 5 | changed, not higher (pass) |",
				" | 4 | 5 | changed, lowered from 5 (pass) |",
				" | 2 | 5 | brought to 3 or less |",
				" | - | 5 | gone: deleted, renamed or moved to another package |",
				"<details><summary>2 other functions above 3 at the merge base, unchanged</summary>",
				"| `internal/a.Moved` | `internal/a/b.go:6` | 5 | 5 | unchanged |",
				"Scope: 13 functions in the non-test Go files under `internal/`, outside `" + defaultExclude + "` and generated files. Scores from gocognit v1.3.0",
			},
			notWant: []string{"Small", "init", "Generated", "testHelper", "InTestdata", "Helper", "OutsideInternal", "All rules pass."},
		},
		{
			name: "nothing changed", base: "base", head: "base", limit: fixtureLimit,
			want:    []string{"All rules pass.", "| 0 rose, 0 changed without rising, 0 brought to 3 or less | pass |", "<details><summary>8 other functions"},
			notWant: []string{"### Functions this change concerns", "To fix a failure"},
		},
		{
			name: "nothing above the cap at the merge base: absolute cap", base: "base", head: "head", limit: 5,
			want:    []string{"All rules pass.", "No function scores above 5 at the merge base (`0123456789ab`): the cap is absolute."},
			notWant: []string{"<details>", "### Functions this change concerns"},
		},
		{
			name: "absolute cap, a new function above it", base: "", head: "head", limit: 4,
			want: []string{
				"**The ratchet fails**",
				"No function scores above 4 at the merge base (`0123456789ab`): the cap is absolute.",
				"| A new function scores 4 or less | 4 above | **FAIL** |",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := map[string]function{}
			if tc.base != "" {
				base = loadFixture(t, tc.base)
			}
			out := reportOf(t, evaluate(base, loadFixture(t, tc.head), tc.limit))
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("report lacks %q:\n%s", w, out)
				}
			}
			for _, nw := range tc.notWant {
				if strings.Contains(out, nw) {
					t.Errorf("report holds %q:\n%s", nw, out)
				}
			}
		})
	}
}

func TestScopeIncludesSeveralDirs(t *testing.T) {
	sc := scope{dirs: []string{"internal/", "tools/"}, exclude: regexp.MustCompile(defaultExclude)}
	for name, want := range map[string]bool{
		"internal/a/a.go":                                true,
		"tools/complexity/main.go":                       true,
		"tools/coversummary/gate.go":                     true,
		"tools/complexity/main_test.go":                  false,
		"tools/complexity/testdata/head/internal/a/a.go": false,
		"tools/complexity/testdata/head/cmd/main.go":     false,
		"internal/sdktest/server.go":                     false,
		"main.go":                                        false,
		"toolsextra/x.go":                                false,
	} {
		if got := sc.includes(name); got != want {
			t.Errorf("includes(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestDirTreeSeveralDirs(t *testing.T) {
	sc := scope{dirs: []string{"internal/", "cmd/"}, exclude: regexp.MustCompile(defaultExclude)}
	tr, err := dirTree(filepath.Join("testdata", "head"), sc)
	if err != nil {
		t.Fatal(err)
	}
	fns, err := scoreTree(tr)
	if err != nil {
		t.Fatal(err)
	}
	if got := fns["cmd.OutsideInternal"].score; got != 6 {
		t.Errorf("cmd.OutsideInternal scores %d, want 6 (functions: %d)", got, len(fns))
	}
	if _, ok := fns["internal/a.New"]; !ok {
		t.Error("internal/a.New is missing")
	}
	if _, err := dirTree(filepath.Join("testdata", "head"), scope{dirs: []string{"absent/"}}); err == nil {
		t.Error("a directory that does not exist is not an error")
	}
}

func TestCodeList(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"internal/"}, "`internal/`"},
		{[]string{"internal/", "tools/"}, "`internal/` and `tools/`"},
		{[]string{"a/", "b/", "c/"}, "`a/`, `b/` and `c/`"},
	} {
		if got := codeList(tc.in); got != tc.want {
			t.Errorf("codeList(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
