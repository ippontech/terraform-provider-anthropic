// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"
)

// packageCoverage counts the statements of one package: the files of a
// directory of the profile.
type packageCoverage struct {
	dir            string
	total, covered int
}

func (p packageCoverage) percent() float64 { return percent(p.covered, p.total) }

// packagesOf groups the files of the profile by package, outside the
// exclusions, least covered first. A package without any statement is left
// out: it has no coverage to speak of.
func packagesOf(files map[string]*fileCoverage, trim string, excluded func(string) bool) []packageCoverage {
	byDir := map[string]*packageCoverage{}
	for _, fc := range files {
		name := strings.TrimPrefix(fc.Name, trim)
		if excluded(name) || fc.Total == 0 {
			continue
		}
		dir := path.Dir(name)
		p := byDir[dir]
		if p == nil {
			p = &packageCoverage{dir: dir}
			byDir[dir] = p
		}
		p.total += fc.Total
		p.covered += fc.Covered
	}
	out := make([]packageCoverage, 0, len(byDir))
	for _, p := range byDir {
		out = append(out, *p)
	}
	slices.SortFunc(out, func(a, b packageCoverage) int {
		return cmp.Or(cmp.Compare(a.percent(), b.percent()), strings.Compare(a.dir, b.dir))
	})
	return out
}

// packageTarget is the coverage each package works towards: package_min once
// it is set, total_target until then.
func (c config) packageTarget() float64 {
	if c.PackageMin != nil {
		return *c.PackageMin
	}
	return c.TotalTarget
}

// packagesUnder counts the packages under the package target.
func (r gateResult) packagesUnder() int {
	target := r.in.config.packageTarget()
	n := 0
	for _, p := range r.packages {
		if p.percent() < target-1e-9 {
			n++
		}
	}
	return n
}

// packagesOK is the per-package rule: always true while package_min is not
// set (the table is monitored, not enforced).
func (r gateResult) packagesOK() bool {
	return r.in.config.PackageMin == nil || r.packagesUnder() == 0
}

// writePackageRule prints the rule row of the per-package floor.
func writePackageRule(b *strings.Builder, r gateResult) {
	cfg := r.in.config
	measured := "no package with statements"
	if len(r.packages) > 0 {
		lowest := r.packages[0]
		measured = fmt.Sprintf("lowest %.1f%% (`%s`); %d of %d under %g%%", lowest.percent(), lowest.dir, r.packagesUnder(), len(r.packages), cfg.packageTarget())
	}
	if cfg.PackageMin == nil {
		fmt.Fprintf(b, "| Packages: coverage of each package | %s | none yet: `package_min` is not set | monitored |\n", measured)
		return
	}
	fmt.Fprintf(b, "| Packages: coverage of each package | %s | at least %g%% per package | %s |\n", measured, *cfg.PackageMin, verdict(r.packagesOK()))
}

// writePackages prints every package, least covered first, with its distance
// to the package target.
func writePackages(b *strings.Builder, r gateResult) {
	if len(r.packages) == 0 {
		return
	}
	cfg := r.in.config
	target := cfg.packageTarget()
	b.WriteString("### Coverage per package\n\nEvery package outside the exclusions, least covered first. ")
	if cfg.PackageMin == nil {
		fmt.Fprintf(b, "Monitored, not enforced: `package_min` in `.coverage.json` is not set yet. Once it is set to %g, every package must stay at %g%% or more.\n\n", target, target)
	} else {
		fmt.Fprintf(b, "Enforced: every package must be at %g%% or more (`package_min`).\n\n", target)
	}
	if !r.packagesOK() {
		fmt.Fprintf(b, "Under the %g%% of `package_min`: %d of %d packages. Add tests to them; the table gives each package's distance.\n\n", target, r.packagesUnder(), len(r.packages))
	}
	fmt.Fprintf(b, "| Package | Statements | Covered | Coverage | Points to %g%% |\n|---|---:|---:|---:|---:|\n", target)
	for _, p := range r.packages {
		distance := "reached"
		if gap := target - p.percent(); gap > 1e-9 {
			distance = fmt.Sprintf("%.1f", gap)
		}
		fmt.Fprintf(b, "| `%s` | %d | %d | %.1f%% | %s |\n", p.dir, p.total, p.covered, p.percent(), distance)
	}
	b.WriteString("\n")
}
