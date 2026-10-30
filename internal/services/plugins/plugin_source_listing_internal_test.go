// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Characterization tests of listPluginSource: they pin the exact sources and
// error messages it produces, so that splitting the function cannot change
// what an apply sends or reports.

// TestListPluginSource_archiveExact: an archive source is the archive alone,
// named by its base name, with its size, whatever source_dir says.
func TestListPluginSource_archiveExact(t *testing.T) {
	t.Parallel()
	archive := filepath.Join(t.TempDir(), "Sales-Toolkit.PLUGIN")
	const content = "PK\x03\x04 not really a zip"
	writeFile(t, archive, content)

	got, err := listPluginSource(filepath.Join(t.TempDir(), "ignored"), archive)
	if err != nil {
		t.Fatalf("listPluginSource: %v", err)
	}
	want := pluginSource{archive: true, files: []pluginSourceFile{{name: "Sales-Toolkit.PLUGIN", path: archive, size: int64(len(content))}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("source = %+v, want %+v", got, want)
	}
}

// TestListPluginSource_directoryExact: a directory source lists every
// regular file at any depth, named by its slash-separated path within the
// plugin, with its path on disk and size, sorted by name (not by walk order).
func TestListPluginSource_directoryExact(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for name, content := range map[string]string{
		".claude-plugin/plugin.json":    `{"name":"p"}`,
		"a-b.md":                        "1",
		"a/b.md":                        "22",
		"skills/x/SKILL.md":             "333",
		"skills/x/scripts/run.sh":       "4444",
		"commands/deeply/nested/cmd.md": "",
	} {
		writeFile(t, filepath.Join(root, filepath.FromSlash(name)), content)
	}
	if err := os.MkdirAll(filepath.Join(root, "empty", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := listPluginSource(root, "")
	if err != nil {
		t.Fatalf("listPluginSource: %v", err)
	}
	file := func(name string, size int64) pluginSourceFile {
		return pluginSourceFile{name: name, path: filepath.Join(root, filepath.FromSlash(name)), size: size}
	}
	want := pluginSource{files: []pluginSourceFile{
		file(".claude-plugin/plugin.json", 12),
		file("a-b.md", 1),
		file("a/b.md", 2),
		file("commands/deeply/nested/cmd.md", 0),
		file("skills/x/SKILL.md", 3),
		file("skills/x/scripts/run.sh", 4),
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("source =\n %+v\nwant\n %+v", got, want)
	}
}

// TestListPluginSource_exactErrors pins every error message, wrapping
// included. The OS's own error text comes from os.Lstat and os.Stat, so the
// expectations hold on every platform (on Linux: "lstat <path>: no such file
// or directory"); the symlink and named pipe cases run where the platform
// can make one.
func TestListPluginSource_exactErrors(t *testing.T) {
	t.Parallel()
	noManifest := t.TempDir()
	writeFile(t, filepath.Join(noManifest, "README.md"), "x")
	file := filepath.Join(t.TempDir(), "plugin.json")
	writeFile(t, file, "{}")
	missingDir := filepath.Join(t.TempDir(), "missing")
	_, lstatErr := os.Lstat(missingDir)
	missingArchive := filepath.Join(t.TempDir(), "missing.zip")
	_, statErr := os.Stat(missingArchive)
	dirArchive := t.TempDir()

	type errorCase struct {
		dir, archive, want string
		wantIs             error
	}
	cases := map[string]errorCase{
		"no manifest": {dir: noManifest,
			want: fmt.Sprintf("source_dir %q has no .claude-plugin/plugin.json: every plugin needs its manifest there", noManifest)},
		"dir is a file": {dir: file,
			want: fmt.Sprintf("source_dir %q is not a directory", file)},
		"missing dir": {dir: missingDir, wantIs: fs.ErrNotExist,
			want: fmt.Sprintf("unable to read source_dir %q: %v", missingDir, lstatErr)},
		"missing archive": {archive: missingArchive, wantIs: fs.ErrNotExist,
			want: fmt.Sprintf("unable to read source_archive %q: %v", missingArchive, statErr)},
		"archive is a dir": {archive: dirArchive,
			want: fmt.Sprintf("source_archive %q is not a regular file", dirArchive)},
	}

	const notRegular = "unable to read source_dir %q: %q is not a regular file: symbolic links and special files cannot be uploaded"
	withLink := writePluginDir(t)
	link := filepath.Join(withLink, "link.json")
	if err := os.Symlink(filepath.Join(withLink, ".claude-plugin", "plugin.json"), link); err != nil {
		t.Logf("symlink case not run: %v", err)
	} else {
		cases["symlink inside"] = errorCase{dir: withLink, want: fmt.Sprintf(notRegular, withLink, link)}
	}
	withFIFO := writePluginDir(t)
	fifo := filepath.Join(withFIFO, "pipe")
	if err := mkfifo(fifo); err != nil {
		t.Logf("named pipe case not run: %v", err)
	} else {
		cases["named pipe inside"] = errorCase{dir: withFIFO, want: fmt.Sprintf(notRegular, withFIFO, fifo)}
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src, err := listPluginSource(tc.dir, tc.archive)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v\nwant %s", err, tc.want)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("err does not wrap %v", tc.wantIs)
			}
			if !reflect.DeepEqual(src, pluginSource{}) {
				t.Errorf("source = %+v, want the zero value with an error", src)
			}
		})
	}
}
