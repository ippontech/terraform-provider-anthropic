// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Command coversummary turns a Go coverage profile into a Markdown summary:
// the total, then one row per file matching -match (by default every resource
// and data source file), least covered first. CI appends it to the job
// summary of the unit tests.
//
//	go test -coverprofile=coverage.out ./...
//	go run -C tools ./coversummary ../coverage.out
//
// It uses the standard library only, so it adds nothing to the tools module.
package main

import (
	"bufio"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

func main() {
	match := flag.String("match", `_(resource|data_source)\.go$`, "regular expression selecting the files listed one by one")
	trim := flag.String("trim", "github.com/ippontech/terraform-provider-anthropic/", "prefix removed from the file paths shown")
	flag.Usage = func() {
		_, _ = fmt.Fprintln(flag.CommandLine.Output(), "usage: coversummary [-match regexp] [-trim prefix] coverage.out")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	re, err := regexp.Compile(*match)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coversummary: invalid -match: %v\n", err)
		os.Exit(2)
	}
	if err := run(os.Stdout, flag.Arg(0), re, *trim); err != nil {
		fmt.Fprintf(os.Stderr, "coversummary: %v\n", err)
		os.Exit(1)
	}
}

func run(w io.Writer, path string, match *regexp.Regexp, trim string) error {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	files, err := parseProfile(f)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return writeSummary(w, files, match, trim)
}

// fileCoverage counts the statements of one source file.
type fileCoverage struct {
	Name    string
	Total   int
	Covered int
	blocks  map[string]int // block position -> index in counts
	counts  []blockCount
}

type blockCount struct {
	stmts   int
	covered bool
}

// percent returns the share of covered statements, 0 for an empty file.
func (c fileCoverage) percent() float64 {
	if c.Total == 0 {
		return 0
	}
	return 100 * float64(c.Covered) / float64(c.Total)
}

// parseProfile reads a profile written by go test -coverprofile, in any mode
// (set, count or atomic). A block listed more than once, as happens when
// several test binaries instrument the same package, counts once and is
// covered if any occurrence is.
func parseProfile(r io.Reader) (map[string]*fileCoverage, error) {
	files := map[string]*fileCoverage{}
	scanner := bufio.NewScanner(r)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if line == 1 {
			if !strings.HasPrefix(text, "mode: ") {
				return nil, errors.New("not a coverage profile: missing mode line")
			}
			continue
		}
		// name.go:line.column,line.column numberOfStatements count
		colon := strings.LastIndexByte(text, ':')
		fields := strings.Fields(text[colon+1:])
		if colon < 1 || len(fields) != 3 {
			return nil, fmt.Errorf("line %d: malformed block %q", line, text)
		}
		stmts, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("line %d: statement count: %w", line, err)
		}
		count, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: execution count: %w", line, err)
		}
		name := text[:colon]
		fc := files[name]
		if fc == nil {
			fc = &fileCoverage{Name: name, blocks: map[string]int{}}
			files[name] = fc
		}
		if i, seen := fc.blocks[fields[0]]; seen {
			fc.counts[i].covered = fc.counts[i].covered || count > 0
			continue
		}
		fc.blocks[fields[0]] = len(fc.counts)
		fc.counts = append(fc.counts, blockCount{stmts: stmts, covered: count > 0})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if line == 0 {
		return nil, errors.New("empty coverage profile")
	}
	for _, fc := range files {
		for _, b := range fc.counts {
			fc.Total += b.stmts
			if b.covered {
				fc.Covered += b.stmts
			}
		}
	}
	return files, nil
}

// writeSummary prints the Markdown summary: the total over every file of the
// profile, then the files matching match, least covered first.
func writeSummary(w io.Writer, files map[string]*fileCoverage, match *regexp.Regexp, trim string) error {
	var total fileCoverage
	var listed []fileCoverage
	for _, fc := range files {
		total.Total += fc.Total
		total.Covered += fc.Covered
		if fc.Total > 0 && match.MatchString(fc.Name) {
			c := *fc
			c.Name = strings.TrimPrefix(c.Name, trim)
			listed = append(listed, c)
		}
	}
	slices.SortFunc(listed, func(a, b fileCoverage) int {
		return cmp.Or(cmp.Compare(a.percent(), b.percent()), strings.Compare(a.Name, b.Name))
	})

	var b strings.Builder
	fmt.Fprintf(&b, "## Unit test coverage\n\n")
	fmt.Fprintf(&b, "**%.1f%%** of %d statements across %d files (`go test` without `TF_ACC`: acceptance tests are skipped).\n\n",
		total.percent(), total.Total, len(files))
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
