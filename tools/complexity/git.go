// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// git runs git in dir ("" for the current directory) and returns its
// standard output.
func git(dir string, args ...string) ([]byte, error) {
	return gitInput(dir, nil, args...)
}

func gitInput(dir string, stdin io.Reader, args ...string) ([]byte, error) {
	//nolint:gosec // G204: always git; findMergeBase refuses a -base that git would read as an option.
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// findMergeBase returns the merge base of HEAD and base, a full commit hash.
func findMergeBase(root, base string) (string, error) {
	if strings.HasPrefix(base, "-") {
		return "", fmt.Errorf("-base %q is not a revision", base)
	}
	out, err := git(root, "merge-base", base, "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// commitTree reads the files of a commit that the scope includes, with one
// git cat-file process for all of them.
func commitTree(root, commit string, sc scope) (tree, error) {
	out, err := git(root, append([]string{"ls-tree", "-r", "-z", "--full-tree", commit, "--"}, sc.dirs...)...)
	if err != nil {
		return nil, err
	}
	var names, objects []string
	for entry := range strings.SplitSeq(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if entry == "" {
			continue
		}
		// <mode> SP <type> SP <object> TAB <path>
		meta, name, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("git ls-tree: unexpected entry %q", entry)
		}
		// Regular files only, as dirTree reads them: no symbolic link (120000)
		// and no submodule.
		if (fields[0] != "100644" && fields[0] != "100755") || !sc.includes(name) {
			continue
		}
		names = append(names, name)
		objects = append(objects, fields[2])
	}
	t := tree{}
	if len(names) == 0 {
		return t, nil
	}
	out, err = gitInput(root, strings.NewReader(strings.Join(objects, "\n")+"\n"), "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	r := bufio.NewReader(bytes.NewReader(out))
	for i, name := range names {
		content, err := readBatchObject(r, objects[i])
		if err != nil {
			return nil, fmt.Errorf("git cat-file %s (%s): %w", objects[i], name, err)
		}
		t[name] = content
	}
	return t, nil
}

// readBatchObject reads one "<object> <type> <size>\n<content>\n" record of
// git cat-file --batch.
func readBatchObject(r *bufio.Reader, object string) ([]byte, error) {
	header, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[0] != object || fields[1] != "blob" {
		return nil, fmt.Errorf("unexpected header %q", strings.TrimSpace(header))
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return nil, err
	}
	content := make([]byte, size+1)
	if _, err := io.ReadFull(r, content); err != nil {
		return nil, err
	}
	if content[size] != '\n' {
		return nil, errors.New("record not terminated by a newline")
	}
	return content[:size], nil
}

// dirTree reads the files of the working tree that the scope includes,
// through an os.Root so that no symbolic link leads out of the repository.
func dirTree(root string, sc scope) (tree, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	fsys := r.FS()
	t := tree{}
	for _, dir := range sc.dirs {
		if err := readDir(fsys, strings.TrimSuffix(dir, "/"), sc, t); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// readDir adds to t the files under the directory start that the scope
// includes.
func readDir(fsys fs.FS, start string, sc scope, t tree) error {
	return fs.WalkDir(fsys, start, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name != start && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !sc.includes(name) {
			return nil
		}
		content, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		t[name] = content
		return nil
	})
}
