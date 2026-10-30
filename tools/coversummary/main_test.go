// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"testing/iotest"
)

const profile = `mode: atomic
example.com/m/internal/services/a/a_resource.go:10.2,12.3 2 1
example.com/m/internal/services/a/a_resource.go:14.2,15.3 3 0
example.com/m/internal/services/a/a_data_source.go:5.1,6.2 4 0
example.com/m/internal/services/a/helpers.go:1.1,2.2 1 5
example.com/m/internal/services/a/a_resource.go:14.2,15.3 3 7
example.com/m/internal/services/a/a_data_source.go:5.1,6.2 4 0
`

func TestParseProfile(t *testing.T) {
	files, err := parseProfile(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct{ total, covered int }{
		// The second occurrence of 14.2,15.3 is covered, so the block counts as covered once.
		"example.com/m/internal/services/a/a_resource.go":    {5, 5},
		"example.com/m/internal/services/a/a_data_source.go": {4, 0},
		"example.com/m/internal/services/a/helpers.go":       {1, 1},
	}
	if len(files) != len(tests) {
		t.Fatalf("got %d files, want %d", len(files), len(tests))
	}
	for name, want := range tests {
		got := files[name]
		if got == nil {
			t.Fatalf("%s: missing", name)
		}
		if got.Total != want.total || got.Covered != want.covered {
			t.Errorf("%s: got %d/%d statements covered, want %d/%d", name, got.Covered, got.Total, want.covered, want.total)
		}
	}
}

func TestParseProfileErrors(t *testing.T) {
	for name, input := range map[string]string{
		"empty":      "",
		"no mode":    "example.com/m/a.go:1.1,2.2 1 1\n",
		"malformed":  "mode: set\nexample.com/m/a.go:1.1,2.2 1\n",
		"bad stmts":  "mode: set\nexample.com/m/a.go:1.1,2.2 x 1\n",
		"bad count":  "mode: set\nexample.com/m/a.go:1.1,2.2 1 x\n",
		"no colon":   "mode: set\nexample.com/m/a.go 1 1\n",
		"empty name": "mode: set\n:1.1,2.2 1 1\n",
		"no comma":   "mode: set\nexample.com/m/a.go:1.1 1 1\n",
		"no column":  "mode: set\nexample.com/m/a.go:1,2.2 1 1\n",
		"bad end":    "mode: set\nexample.com/m/a.go:1.1,x.2 1 1\n",
		"backwards":  "mode: set\nexample.com/m/a.go:5.1,2.2 1 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseProfile(strings.NewReader(input)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestWriteSummary(t *testing.T) {
	files, err := parseProfile(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := writeSummary(&b, files, regexp.MustCompile(`_(resource|data_source)\.go$`), "example.com/m/", nil); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{
		"**60.0%** of 10 statements across 3 files",
		"(2), least covered first",
		"| `internal/services/a/a_data_source.go` | 4 | 0 | 0.0% |\n| `internal/services/a/a_resource.go` | 5 | 5 | 100.0% |\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "helpers.go") {
		t.Errorf("summary lists a file -match excludes:\n%s", got)
	}
}

func TestParseProfileSkipsBlankLines(t *testing.T) {
	files, err := parseProfile(strings.NewReader("mode: set\n\nexample.com/m/a.go:1.1,2.2 2 1\n  \nexample.com/m/a.go:3.1,4.2 1 0\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := files["example.com/m/a.go"]; got == nil || got.Total != 3 || got.Covered != 2 {
		t.Errorf("a.go: %+v, want 2 of 3 statements covered", got)
	}
}

func TestParseProfileErrorMessages(t *testing.T) {
	for input, want := range map[string]string{
		"":                                 "empty coverage profile",
		"example.com/m/a.go:1.1,2.2 1 1\n": "not a coverage profile: missing mode line",
		"mode: set\nexample.com/m/a.go:1.1,2.2 1\n":     `line 2: malformed block "example.com/m/a.go:1.1,2.2 1"`,
		"mode: set\nexample.com/m/a.go:1.1,2.2 x 1\n":   `line 2: statement count: strconv.Atoi: parsing "x": invalid syntax`,
		"mode: set\nexample.com/m/a.go:1.1,2.2 1 x\n":   `line 2: execution count: strconv.ParseInt: parsing "x": invalid syntax`,
		"mode: set\n\nexample.com/m/a.go:5.1,2.2 1 1\n": `line 3: block position "5.1,2.2" ends before it starts`,
	} {
		if _, err := parseProfile(strings.NewReader(input)); err == nil || err.Error() != want {
			t.Errorf("parseProfile(%q) error = %v, want %q", input, err, want)
		}
	}
	readErr := errors.New("read failed")
	if _, err := parseProfile(iotest.ErrReader(readErr)); !errors.Is(err, readErr) {
		t.Errorf("parseProfile on a failing reader: error = %v, want %v", err, readErr)
	}
}
