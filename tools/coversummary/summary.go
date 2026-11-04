// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"cmp"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

// writeSummary prints the Markdown summary: the total over the files of the
// profile that excluded does not reject (every file when it is nil), then
// those of them matching match, least covered first.
func writeSummary(w io.Writer, files map[string]*fileCoverage, match *regexp.Regexp, trim string, excluded func(string) bool) error {
	var total fileCoverage
	var listed []fileCoverage
	counted := 0
	for _, fc := range files {
		name := strings.TrimPrefix(fc.Name, trim)
		if excluded != nil && excluded(name) {
			continue
		}
		counted++
		total.Total += fc.Total
		total.Covered += fc.Covered
		if fc.Total > 0 && match.MatchString(fc.Name) {
			c := *fc
			c.Name = name
			listed = append(listed, c)
		}
	}
	slices.SortFunc(listed, func(a, b fileCoverage) int {
		return cmp.Or(cmp.Compare(a.percent(), b.percent()), strings.Compare(a.Name, b.Name))
	})

	var b strings.Builder
	fmt.Fprintf(&b, "## Unit test coverage\n\n")
	scope := ""
	if excluded != nil {
		scope = ", outside the exclusions of `.coverage.json`"
	}
	fmt.Fprintf(&b, "**%.1f%%** of %d statements across %d files%s (`go test` without `TF_ACC`: acceptance tests are skipped).\n\n",
		total.percent(), total.Total, counted, scope)
	fmt.Fprintf(&b, "### Files matching `%s` (%d), least covered first\n\n", match, len(listed))
	if len(listed) == 0 {
		b.WriteString("None.\n")
	} else {
		b.WriteString("| File | Statements | Covered | Coverage |\n|---|---:|---:|---:|\n")
		for _, c := range listed {
			fmt.Fprintf(&b, "| `%s` | %d | %d | %.1f%% |\n", c.Name, c.Total, c.Covered, c.percent())
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
