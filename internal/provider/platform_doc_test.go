// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
)

// documentedPlatforms are the columns of the availability table in the
// provider documentation, in order.
var documentedPlatforms = []platform.Platform{platform.AWS}

const (
	availabilityStart = "<!-- platform-availability:start -->"
	availabilityEnd   = "<!-- platform-availability:end -->"
)

func stateWord(s platform.State) string {
	switch s {
	case platform.StateSupported:
		return "Supported"
	case platform.StateUnverified:
		return "Unverified"
	case platform.StateUnavailable:
		return "Unavailable"
	default:
		return "Undeclared"
	}
}

// availabilityTable renders, from the registry, the table of every type that
// is not supported on every documented platform.
func availabilityTable(t *testing.T) string {
	t.Helper()
	type row struct {
		kinds []string
		cells []string
	}
	rows := map[string]*row{}
	add := func(name, kind string, on func(platform.Platform) platform.Support) {
		var cells []string
		supported := true
		for _, p := range documentedPlatforms {
			s := on(p)
			cells = append(cells, stateWord(s.State))
			supported = supported && s.State == platform.StateSupported
		}
		if supported {
			return
		}
		if r, ok := rows[name]; ok {
			if strings.Join(r.cells, "|") != strings.Join(cells, "|") {
				t.Fatalf("%s: the resource and the data source differ; give them separate rows", name)
			}
			r.kinds = append(r.kinds, kind)
			return
		}
		rows[name] = &row{kinds: []string{kind}, cells: cells}
	}
	for _, reg := range resourceRegistrations() {
		add(resourceTypeName(reg.New()), "resource", reg.on)
	}
	for _, reg := range dataSourceRegistrations() {
		add(dataSourceTypeName(reg.New()), "data source", reg.on)
	}

	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	sort.Strings(names)

	header := []string{"Type", "Kind"}
	for _, p := range documentedPlatforms {
		header = append(header, p.DisplayName())
	}
	var b strings.Builder
	b.WriteString("| " + strings.Join(header, " | ") + " |\n")
	b.WriteString("|" + strings.Repeat("---|", len(header)) + "\n")
	for _, name := range names {
		r := rows[name]
		b.WriteString("| `" + name + "` | " + strings.Join(r.kinds, ", ") + " | " + strings.Join(r.cells, " | ") + " |\n")
	}
	return b.String()
}

// TestIndexDocumentsPlatformAvailability keeps the availability table of the
// provider documentation equal to the registry. On a mismatch, paste the
// printed table between the markers of templates/index.md.tmpl and run
// `make generate`.
func TestIndexDocumentsPlatformAvailability(t *testing.T) {
	raw, err := os.ReadFile("../../templates/index.md.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	start, end := strings.Index(doc, availabilityStart), strings.Index(doc, availabilityEnd)
	if start < 0 || end < start {
		t.Fatalf("templates/index.md.tmpl lacks the %s ... %s markers", availabilityStart, availabilityEnd)
	}
	got := strings.TrimSpace(doc[start+len(availabilityStart) : end])
	want := strings.TrimSpace(availabilityTable(t))
	if got != want {
		t.Errorf("the availability table in templates/index.md.tmpl differs from the registry. Expected:\n\n%s\n", want)
	}
}
