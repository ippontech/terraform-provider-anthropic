// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// fileCoverage counts the statements of one source file.
type fileCoverage struct {
	Name    string
	Total   int
	Covered int
	blocks  map[string]int // block position -> index in counts
	counts  []blockCount
}

// blockCount is one block of the profile: the lines it spans, its statements
// and whether any test ran it.
type blockCount struct {
	startLine, endLine int
	stmts              int
	covered            bool
}

// percent returns the share of covered statements, 0 for an empty file.
func (c fileCoverage) percent() float64 {
	return percent(c.Covered, c.Total)
}

func percent(covered, total int) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(covered) / float64(total)
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
		name, pos, block, err := parseBlock(text)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		fc := files[name]
		if fc == nil {
			fc = &fileCoverage{Name: name, blocks: map[string]int{}}
			files[name] = fc
		}
		fc.add(pos, block)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if line == 0 {
		return nil, errors.New("empty coverage profile")
	}
	for _, fc := range files {
		fc.sum()
	}
	return files, nil
}

// parseBlock parses one block line of a profile,
// "name.go:line.column,line.column numberOfStatements count", into the file
// name, the block position and the block.
func parseBlock(text string) (name, pos string, b blockCount, err error) {
	colon := strings.LastIndexByte(text, ':')
	fields := strings.Fields(text[colon+1:])
	if colon < 1 || len(fields) != 3 {
		return "", "", b, fmt.Errorf("malformed block %q", text)
	}
	pos = fields[0]
	if b.startLine, b.endLine, err = blockLines(pos); err != nil {
		return "", "", b, err
	}
	if b.stmts, err = strconv.Atoi(fields[1]); err != nil {
		return "", "", b, fmt.Errorf("statement count: %w", err)
	}
	count, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return "", "", b, fmt.Errorf("execution count: %w", err)
	}
	b.covered = count > 0
	return text[:colon], pos, b, nil
}

// add records a block of the file; a block seen before stays counted once,
// covered if either occurrence is.
func (c *fileCoverage) add(pos string, b blockCount) {
	if i, seen := c.blocks[pos]; seen {
		c.counts[i].covered = c.counts[i].covered || b.covered
		return
	}
	c.blocks[pos] = len(c.counts)
	c.counts = append(c.counts, b)
}

// sum sets Total and Covered from the blocks.
func (c *fileCoverage) sum() {
	for _, b := range c.counts {
		c.Total += b.stmts
		if b.covered {
			c.Covered += b.stmts
		}
	}
}

// blockLines parses "startLine.startCol,endLine.endCol".
func blockLines(pos string) (int, int, error) {
	start, end, ok := strings.Cut(pos, ",")
	if !ok {
		return 0, 0, fmt.Errorf("malformed block position %q", pos)
	}
	startLine, err := lineOf(start)
	if err != nil {
		return 0, 0, fmt.Errorf("block position %q: %w", pos, err)
	}
	endLine, err := lineOf(end)
	if err != nil {
		return 0, 0, fmt.Errorf("block position %q: %w", pos, err)
	}
	if endLine < startLine {
		return 0, 0, fmt.Errorf("block position %q ends before it starts", pos)
	}
	return startLine, endLine, nil
}

func lineOf(lineCol string) (int, error) {
	l, _, ok := strings.Cut(lineCol, ".")
	if !ok {
		return 0, fmt.Errorf("%q is not line.column", lineCol)
	}
	return strconv.Atoi(l)
}
