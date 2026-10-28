// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// config is .coverage.json.
type config struct {
	// DiffMin is the minimum covered share, in percent, of the statements a
	// change touches.
	DiffMin float64 `json:"diff_min"`
	// TotalTarget is the total the module works towards; once the baseline
	// reaches it, it is also a floor the total never goes under.
	TotalTarget float64 `json:"total_target"`
	// RatchetTolerance is how far, in points, the total may fall under the
	// baseline (coverage moves a little with timing-dependent branches).
	RatchetTolerance float64 `json:"ratchet_tolerance"`
	// BaselineFile holds the baseline, relative to the configuration file.
	BaselineFile string `json:"baseline_file"`
	// PackageMin is the coverage, in percent, each package outside the
	// exclusions must reach. While it is null, the summary lists every
	// package and its distance to total_target, but nothing fails.
	PackageMin *float64 `json:"package_min"`
	// Exclude lists module-relative paths left out of every rule: a
	// directory when it ends with "/", a .go file otherwise.
	Exclude []string `json:"exclude"`

	baselinePath string
}

func loadConfig(path string) (config, error) {
	var cfg config
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return cfg, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	var problems []string
	for name, v := range map[string]float64{"diff_min": cfg.DiffMin, "total_target": cfg.TotalTarget} {
		if v <= 0 || v > 100 {
			problems = append(problems, fmt.Sprintf("%s must be in (0, 100], got %v", name, v))
		}
	}
	if cfg.PackageMin != nil && (*cfg.PackageMin <= 0 || *cfg.PackageMin > 100) {
		problems = append(problems, fmt.Sprintf("package_min must be null or in (0, 100], got %v", *cfg.PackageMin))
	}
	if cfg.RatchetTolerance < 0 || cfg.RatchetTolerance >= 100 {
		problems = append(problems, fmt.Sprintf("ratchet_tolerance must be in [0, 100), got %v", cfg.RatchetTolerance))
	}
	if cfg.BaselineFile == "" {
		problems = append(problems, "baseline_file is missing")
	}
	for _, e := range cfg.Exclude {
		switch {
		case e == "" || strings.HasPrefix(e, "/") || strings.HasPrefix(e, "./") || slices.Contains(strings.Split(e, "/"), ".."):
			problems = append(problems, fmt.Sprintf("exclude entry %q is not a clean module-relative path", e))
		case !strings.HasSuffix(e, "/") && !strings.HasSuffix(e, ".go"):
			// Matched as a file name, a directory without its slash would
			// silently exclude nothing.
			problems = append(problems, fmt.Sprintf("exclude entry %q is neither a directory ending in / nor a .go file", e))
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return cfg, fmt.Errorf("%s: %s", path, strings.Join(problems, "; "))
	}
	cfg.baselinePath = filepath.Join(filepath.Dir(path), cfg.BaselineFile)
	return cfg, nil
}

// excluded reports whether a module-relative path is left out of the gate.
func (c config) excluded(name string) bool {
	for _, e := range c.Exclude {
		if strings.HasSuffix(e, "/") && strings.HasPrefix(name, e) || name == e {
			return true
		}
	}
	return false
}

// readBaseline reads a baseline file: one percentage, e.g. "34.08".
func readBaseline(path string) (float64, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil || v < 0 || v > 100 {
		return 0, fmt.Errorf("%s: want a percentage between 0 and 100, got %q", path, strings.TrimSpace(string(data)))
	}
	return v, nil
}

// readOptionalBaseline is readBaseline for the base branch's file, which
// does not exist before the gate lands.
func readOptionalBaseline(path string) (*float64, error) {
	v, err := readBaseline(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// lineRange is an inclusive range of line numbers.
type lineRange struct{ from, to int }

// parseDiff reads the output of git diff --unified=0 and returns, per file
// of the new tree, the line ranges the change adds or modifies. Deleted
// files and pure deletions add nothing, and neither do added lines that are
// blank or hold only a // comment: a coverage block spans the comment and
// blank lines inside it, which are no executable statement.
func parseDiff(r io.Reader) (map[string][]lineRange, error) {
	changes := map[string][]lineRange{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var file string
	oldLeft, newLeft := 0, 0 // content lines still expected in the current hunk
	newLine := 0             // line number, in the new file, of the next added or context line
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		if oldLeft > 0 || newLeft > 0 {
			switch {
			case strings.HasPrefix(text, "-"):
				oldLeft--
			case strings.HasPrefix(text, "+"):
				newLeft--
				addChangedLine(changes, file, text[1:], newLine)
				newLine++
			case strings.HasPrefix(text, " "):
				oldLeft--
				newLeft--
				newLine++
			case strings.HasPrefix(text, `\`): // "\ No newline at end of file"
			default:
				return nil, fmt.Errorf("line %d: hunk ends early at %q", line, text)
			}
			continue
		}
		switch {
		case strings.HasPrefix(text, "diff --git "):
			file = ""
		case strings.HasPrefix(text, "+++ "):
			name, err := diffPath(strings.TrimPrefix(text, "+++ "))
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", line, err)
			}
			file = name
		case strings.HasPrefix(text, "@@ "):
			oldCount, newStart, newCount, err := hunkHeader(text)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", line, err)
			}
			oldLeft, newLeft, newLine = oldCount, newCount, newStart
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if oldLeft > 0 || newLeft > 0 {
		return nil, errors.New("the diff ends inside a hunk")
	}
	return changes, nil
}

// addChangedLine records line n of file, added with the given text, unless
// the file is deleted ("") or the line is blank or holds only a // comment.
// Lines come in ascending order: one that follows the last range extends it.
func addChangedLine(changes map[string][]lineRange, file, text string, n int) {
	if trimmed := strings.TrimSpace(text); file == "" || trimmed == "" || strings.HasPrefix(trimmed, "//") {
		return
	}
	ranges := changes[file]
	if last := len(ranges) - 1; last >= 0 && ranges[last].to == n-1 {
		ranges[last].to = n
		return
	}
	changes[file] = append(ranges, lineRange{n, n})
}

// diffPath returns the path of a "+++ b/path" line, "" for /dev/null. Git
// ends a path holding a space with a tab, and quotes one holding a control or
// non-ASCII character, with C escapes ("b/\303\251.go").
func diffPath(s string) (string, error) {
	s = strings.TrimSuffix(s, "\t")
	if strings.HasPrefix(s, `"`) {
		unquoted, err := strconv.Unquote(s)
		if err != nil {
			return "", fmt.Errorf("quoted path %s: %w", s, err)
		}
		s = unquoted
	}
	if s == "/dev/null" {
		return "", nil
	}
	if !strings.HasPrefix(s, "b/") {
		return "", fmt.Errorf("new path %q does not start with b/", s)
	}
	return strings.TrimPrefix(s, "b/"), nil
}

// hunkHeader parses "@@ -a[,b] +c[,d] @@ ..." into b, c and d.
func hunkHeader(s string) (oldCount, newStart, newCount int, err error) {
	fields := strings.Fields(s)
	if len(fields) < 4 || fields[3] != "@@" || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, 0, fmt.Errorf("malformed hunk header %q", s)
	}
	if _, oldCount, err = hunkRange(fields[1][1:]); err != nil {
		return 0, 0, 0, fmt.Errorf("hunk header %q: %w", s, err)
	}
	if newStart, newCount, err = hunkRange(fields[2][1:]); err != nil {
		return 0, 0, 0, fmt.Errorf("hunk header %q: %w", s, err)
	}
	return oldCount, newStart, newCount, nil
}

func hunkRange(s string) (start, count int, err error) {
	startText, countText, hasCount := strings.Cut(s, ",")
	if start, err = strconv.Atoi(startText); err != nil {
		return 0, 0, err
	}
	count = 1
	if hasCount {
		if count, err = strconv.Atoi(countText); err != nil {
			return 0, 0, err
		}
	}
	return start, count, nil
}

// gateInput is what the rules read besides the profile.
type gateInput struct {
	config       config
	baseline     float64
	baseBaseline *float64               // nil when unknown
	changes      map[string][]lineRange // nil when no diff was given
}

// changedFile is the diff rule's view of one changed file.
type changedFile struct {
	name             string
	changed, covered int
	uncovered        []lineRange // blocks touched by the change that no test ran
}

type gateResult struct {
	in gateInput

	total, totalCovered int
	packages            []packageCoverage // least covered first

	diffChecked                bool
	diffChanged, diffCovered   int
	diffFiles                  []changedFile
	diffIgnored                int // changed Go files out of the rule: tests and exclusions
	floor                      float64
	floorAtTarget              bool
	baselineLowered            bool
	baselineUnderReachedTarget bool
}

func (r gateResult) totalPercent() float64 { return percent(r.totalCovered, r.total) }
func (r gateResult) diffPercent() float64  { return percent(r.diffCovered, r.diffChanged) }

func (r gateResult) diffOK() bool {
	return !r.diffChecked || r.diffChanged == 0 || r.diffPercent() >= r.in.config.DiffMin
}

func (r gateResult) totalOK() bool {
	// A hair of slack for the float arithmetic of baseline - tolerance.
	return r.totalPercent() >= r.floor-1e-9
}

func (r gateResult) baselineOK() bool { return !r.baselineUnderReachedTarget }

func (r gateResult) passed() bool {
	return r.diffOK() && r.totalOK() && r.baselineOK() && r.packagesOK()
}

// evaluate applies the three rules.
func evaluate(in gateInput, files map[string]*fileCoverage, trim string) gateResult {
	cfg := in.config
	r := gateResult{in: in}
	for _, fc := range files {
		if cfg.excluded(strings.TrimPrefix(fc.Name, trim)) {
			continue
		}
		r.total += fc.Total
		r.totalCovered += fc.Covered
	}
	r.packages = packagesOf(files, trim, cfg.excluded)

	r.floor = in.baseline - cfg.RatchetTolerance
	if in.baseline >= cfg.TotalTarget {
		r.floorAtTarget = true
		r.floor = math.Max(r.floor, cfg.TotalTarget)
	}
	if in.baseBaseline != nil {
		r.baselineLowered = in.baseline < *in.baseBaseline
		r.baselineUnderReachedTarget = *in.baseBaseline >= cfg.TotalTarget && in.baseline < cfg.TotalTarget
	}

	if in.changes == nil {
		return r
	}
	r.diffChecked = true
	names := make([]string, 0, len(in.changes))
	for name := range in.changes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") || cfg.excluded(name) {
			r.diffIgnored++
			continue
		}
		cf := changedFile{name: name}
		if fc := files[trim+name]; fc != nil {
			for _, b := range fc.counts {
				if !touches(in.changes[name], b) {
					continue
				}
				cf.changed += b.stmts
				if b.covered {
					cf.covered += b.stmts
				} else {
					cf.uncovered = append(cf.uncovered, lineRange{b.startLine, b.endLine})
				}
			}
		}
		if cf.changed == 0 {
			continue
		}
		slices.SortFunc(cf.uncovered, func(a, b lineRange) int { return a.from - b.from })
		r.diffChanged += cf.changed
		r.diffCovered += cf.covered
		r.diffFiles = append(r.diffFiles, cf)
	}
	return r
}

// touches reports whether one of the changed ranges overlaps the block.
func touches(ranges []lineRange, b blockCount) bool {
	for _, rg := range ranges {
		if rg.from <= b.endLine && b.startLine <= rg.to {
			return true
		}
	}
	return false
}

// floor2 truncates a percentage to two decimals, the precision of the
// baseline file.
func floor2(v float64) float64 { return math.Floor(v*100+1e-9) / 100 }

func verdict(ok bool) string {
	if ok {
		return "pass"
	}
	return "**FAIL**"
}

// writeGate prints the rules, their verdicts and what the author can do.
func writeGate(w io.Writer, r gateResult) error {
	cfg := r.in.config
	var b strings.Builder
	b.WriteString("## Coverage gate\n\n")
	if r.passed() {
		b.WriteString("All rules pass.\n\n")
	} else {
		b.WriteString("**The gate fails**: see the rules marked FAIL.\n\n")
	}
	b.WriteString("| Rule | Measured | Required | Result |\n|---|---|---|---|\n")

	switch {
	case !r.diffChecked:
		fmt.Fprintf(&b, "| Diff: covered share of the statements the change touches | not measured (no diff given) | at least %g%% | skipped |\n", cfg.DiffMin)
	case r.diffChanged == 0:
		fmt.Fprintf(&b, "| Diff: covered share of the statements the change touches | no executable statement changed | at least %g%% | pass |\n", cfg.DiffMin)
	default:
		fmt.Fprintf(&b, "| Diff: covered share of the statements the change touches | %.1f%% (%d of %d) | at least %g%% | %s |\n",
			r.diffPercent(), r.diffCovered, r.diffChanged, cfg.DiffMin, verdict(r.diffOK()))
	}

	required := fmt.Sprintf("at least %.2f%% (baseline %.2f minus %g)", r.floor, r.in.baseline, cfg.RatchetTolerance)
	if r.floorAtTarget {
		required = fmt.Sprintf("at least %.2f%% (baseline %.2f minus %g, never under the %g%% floor)", r.floor, r.in.baseline, cfg.RatchetTolerance, cfg.TotalTarget)
	}
	fmt.Fprintf(&b, "| Ratchet: total outside the exclusions | %.2f%% (%d of %d) | %s | %s |\n",
		r.totalPercent(), r.totalCovered, r.total, required, verdict(r.totalOK()))

	base := "base branch baseline unknown"
	if r.in.baseBaseline != nil {
		base = fmt.Sprintf("base branch: %.2f", *r.in.baseBaseline)
	}
	fmt.Fprintf(&b, "| Floor: the baseline never goes back under %g%% once reached | %.2f (%s) | not under %g once the base reached it | %s |\n",
		cfg.TotalTarget, r.in.baseline, base, cfg.TotalTarget, verdict(r.baselineOK()))
	writePackageRule(&b, r)
	b.WriteString("\n")

	if !r.floorAtTarget {
		fmt.Fprintf(&b, "The target is %g%%: the baseline is %.2f points away. Once the baseline reaches it, the total may never drop below %g%%.\n\n",
			cfg.TotalTarget, cfg.TotalTarget-r.in.baseline, cfg.TotalTarget)
	}
	if measured := floor2(r.totalPercent()); r.totalOK() && measured > r.in.baseline {
		fmt.Fprintf(&b, "This change raises the total to %.2f%% (%d of %d), above the baseline %.2f%%: set `%s` to `%.2f` (the total, truncated) in this pull request to lock the gain in.\n\n",
			r.totalPercent(), r.totalCovered, r.total, r.in.baseline, cfg.BaselineFile, measured)
	}
	if r.baselineLowered && r.baselineOK() {
		fmt.Fprintf(&b, "This change lowers the baseline from %.2f to %.2f: reviewers must agree.\n\n", *r.in.baseBaseline, r.in.baseline)
	}
	if !r.totalOK() {
		fmt.Fprintf(&b, "The total fell under the ratchet: add tests, or, when the drop is deliberate (removed code that was well tested), lower `%s` in this pull request and say why.\n\n", cfg.BaselineFile)
	}
	if !r.baselineOK() {
		fmt.Fprintf(&b, "`%s` was lowered under %g%% after the base branch reached it; restore it.\n\n", cfg.BaselineFile, cfg.TotalTarget)
	}

	if len(r.diffFiles) > 0 {
		b.WriteString("### Statements the change touches\n\nA coverage block the diff touches counts with all its statements; blank lines and lines holding only a `//` comment do not touch it. Test files and the exclusions of `.coverage.json` are left out")
		if r.diffIgnored > 0 {
			fmt.Fprintf(&b, " (%d changed Go files)", r.diffIgnored)
		}
		b.WriteString(".\n\n| File | Changed statements | Covered | Not covered (lines) |\n|---|---:|---:|---|\n")
		for _, f := range r.diffFiles {
			var lines []string
			for _, rg := range f.uncovered {
				if rg.from == rg.to {
					lines = append(lines, strconv.Itoa(rg.from))
				} else {
					lines = append(lines, fmt.Sprintf("%d-%d", rg.from, rg.to))
				}
			}
			fmt.Fprintf(&b, "| `%s` | %d | %d | %s |\n", f.name, f.changed, f.covered, strings.Join(lines, ", "))
		}
		b.WriteString("\n")
	}

	writePackages(&b, r)

	excluded := "nothing"
	if len(cfg.Exclude) > 0 {
		excluded = "`" + strings.Join(cfg.Exclude, "`, `") + "`"
	}
	fmt.Fprintf(&b, "Excluded from every rule: %s.\n\n", excluded)
	_, err := io.WriteString(w, b.String())
	return err
}
