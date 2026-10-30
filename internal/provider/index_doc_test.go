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

// The tables of templates/index.md.tmpl rendered from the registry, between
// their markers.
const (
	credentialsStart = "<!-- credentials:start -->"
	credentialsEnd   = "<!-- credentials:end -->"
)

// registeredType is one registered resource or data source.
type registeredType struct {
	name, kind string
	access     access
	on         func(platform.Platform) platform.Support
}

func registeredTypes() []registeredType {
	var types []registeredType
	for _, reg := range resourceRegistrations() {
		types = append(types, registeredType{resourceTypeName(reg.New()), "resource", reg.Access, reg.on})
	}
	for _, reg := range dataSourceRegistrations() {
		types = append(types, registeredType{dataSourceTypeName(reg.New()), "data source", reg.Access, reg.on})
	}
	return types
}

// typeTable renders a Markdown table with one row per type name: the resource
// and the data source of a name share a row, which lists both kinds, and must
// have the same cells. A type for which cells returns nil has no row.
func typeTable(t *testing.T, header []string, cells func(registeredType) []string) string {
	t.Helper()
	type row struct {
		kinds []string
		cells []string
	}
	rows := map[string]*row{}
	for _, typ := range registeredTypes() {
		c := cells(typ)
		if c == nil {
			continue
		}
		if r, ok := rows[typ.name]; ok {
			if strings.Join(r.cells, "|") != strings.Join(c, "|") {
				t.Fatalf("%s: the resource and the data source differ; give them separate rows", typ.name)
			}
			r.kinds = append(r.kinds, typ.kind)
			continue
		}
		rows[typ.name] = &row{kinds: []string{typ.kind}, cells: c}
	}

	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	sort.Strings(names)

	header = append([]string{"Type", "Kind"}, header...)
	var b strings.Builder
	b.WriteString("| " + strings.Join(header, " | ") + " |\n")
	b.WriteString("|" + strings.Repeat("---|", len(header)) + "\n")
	for _, name := range names {
		r := rows[name]
		b.WriteString("| `" + name + "` | " + strings.Join(r.kinds, ", ") + " | " + strings.Join(r.cells, " | ") + " |\n")
	}
	return b.String()
}

// assertIndexTable fails when the text between start and end in
// templates/index.md.tmpl is not want, and prints want.
func assertIndexTable(t *testing.T, start, end, want string) {
	t.Helper()
	raw, err := os.ReadFile("../../templates/index.md.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	i, j := strings.Index(doc, start), strings.Index(doc, end)
	if i < 0 || j < i {
		t.Fatalf("templates/index.md.tmpl lacks the %s ... %s markers", start, end)
	}
	if got := strings.TrimSpace(doc[i+len(start) : j]); got != strings.TrimSpace(want) {
		t.Errorf("the table between %s and %s in templates/index.md.tmpl differs from the registry. "+
			"Paste this one and run `make generate`:\n\n%s\n", start, end, want)
	}
}

// TestRegistrationsDeclareAccess: every type says which organization can call
// it and which credential it uses, so the credential table has no gap.
func TestRegistrationsDeclareAccess(t *testing.T) {
	for _, typ := range registeredTypes() {
		if typ.access.Organization == "" || typ.access.Credential == "" {
			t.Errorf("%s %s: no Access in internal/provider/registry.go", typ.kind, typ.name)
		}
	}
}

// credentialTable renders, from the registry, the table of every type with
// the organization that can call it and its credential.
func credentialTable(t *testing.T) string {
	t.Helper()
	return typeTable(t, []string{"Organization", "Credential"}, func(typ registeredType) []string {
		return []string{typ.access.Organization, typ.access.Credential}
	})
}

// TestIndexDocumentsCredentials keeps the credential table of the provider
// documentation equal to the registry. On a mismatch, paste the printed table
// between the markers of templates/index.md.tmpl and run `make generate`.
func TestIndexDocumentsCredentials(t *testing.T) {
	assertIndexTable(t, credentialsStart, credentialsEnd, credentialTable(t))
}
