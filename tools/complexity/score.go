// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/uudashr/gocognit"
)

// scope selects the files the ratchet checks.
type scope struct {
	// dir is the module-relative directory checked, with a trailing slash.
	dir string
	// exclude matches the module-relative paths left out (the test-helper
	// packages).
	exclude *regexp.Regexp
}

// includes reports whether a module-relative, slash-separated path is a
// non-test Go file the ratchet checks. Like the go command, it skips testdata
// directories and those whose name starts with "." or "_".
func (s scope) includes(name string) bool {
	if !strings.HasPrefix(name, s.dir) || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
		return false
	}
	if s.exclude != nil && s.exclude.MatchString(name) {
		return false
	}
	for d := range strings.SplitSeq(path.Dir(name), "/") {
		if d == "testdata" || strings.HasPrefix(d, ".") || strings.HasPrefix(d, "_") {
			return false
		}
	}
	return true
}

// tree maps the module-relative, slash-separated paths of Go files to their
// content.
type tree map[string][]byte

// function is one function or method declaration and its score.
type function struct {
	// key identifies the function across the two trees: its package
	// directory, then its name as gocognit prints it, e.g.
	// "internal/services/agents.(*AgentResource).Update". Functions that
	// share a name in a package (init functions, build-tagged twins) are
	// told apart by file: "<name> in <file>".
	key string
	// name is the key before that: package directory and name only.
	name  string
	file  string
	line  int
	score int
	// src is the declaration from `func` to the closing brace: when it
	// differs between the two trees, the change touched the function.
	src []byte
}

// scoreTree scores every function declared in the files of t, generated
// files aside. The scores are gocognit's, computed file by file as
// golangci-lint's gocognit linter does, except that the //gocognit:ignore
// directive is not honoured.
func scoreTree(t tree) (map[string]function, error) {
	byKey := map[string][]function{}
	for _, name := range slices.Sorted(maps.Keys(t)) {
		fns, err := scoreFile(name, t[name])
		if err != nil {
			return nil, err
		}
		for _, fn := range fns {
			byKey[fn.key] = append(byKey[fn.key], fn)
		}
	}
	out := make(map[string]function, len(byKey))
	for key, fns := range byKey {
		if len(fns) == 1 {
			out[key] = fns[0]
			continue
		}
		// init functions, or one function per build-tagged file: tell them
		// apart by file, then by rank within the file.
		rank := map[string]int{}
		for _, fn := range fns {
			rank[fn.file]++
			fn.key = inFile(key, fn.file)
			if rank[fn.file] > 1 {
				fn.key = fmt.Sprintf("%s#%d", fn.key, rank[fn.file])
			}
			out[fn.key] = fn
		}
	}
	return out, nil
}

// inFile is the key of a function that shares its name with another one of
// its package.
func inFile(name, file string) string {
	return fmt.Sprintf("%s in %s", name, file)
}

func scoreFile(name string, src []byte) ([]function, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	if ast.IsGenerated(f) {
		return nil, nil
	}
	recursive := gocognit.RecursiveFuncs([]*ast.File{f}, nil)
	var fns []function
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		start, end := fset.Position(fd.Pos()), fset.Position(fd.End())
		key := path.Dir(name) + "." + funcName(fd)
		fns = append(fns, function{
			key:   key,
			name:  key,
			file:  name,
			line:  start.Line,
			score: gocognit.ScanComplexityWithRecursion(fd, recursive[fd], false).Complexity,
			src:   bytes.Clone(src[start.Offset:end.Offset]),
		})
	}
	return fns, nil
}

// funcName returns "Name" for a function and "(T).Name" or "(*T).Name" for a
// method, type parameters left out, as gocognit and golangci-lint print it.
func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	return fmt.Sprintf("(%s).%s", recvString(fd.Recv.List[0].Type), fd.Name.Name)
}

func recvString(x ast.Expr) string {
	switch t := x.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + recvString(t.X)
	case *ast.IndexExpr:
		return recvString(t.X)
	case *ast.IndexListExpr:
		return recvString(t.X)
	case *ast.ParenExpr:
		return recvString(t.X)
	}
	return "BADRECV"
}
