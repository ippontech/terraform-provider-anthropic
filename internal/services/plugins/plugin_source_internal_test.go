// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePluginDir writes a minimal plugin (manifest plus one skill) under a
// fresh temporary directory and returns its root.
func writePluginDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".claude-plugin", "plugin.json"), `{"name": "sales-toolkit", "version": "1.0.0"}`)
	writeFile(t, filepath.Join(root, "skills", "account-research", "SKILL.md"), "---\nname: account-research\ndescription: Researches an account.\n---\n")
	return root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// uploadParts decodes a multipart upload body into its file parts (by file
// name) and its plain fields, failing on a file part not named `files[]`.
func uploadParts(t *testing.T, contentType string, body []byte) (files, fields map[string]string) {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("content-type = %q, want multipart/form-data (%v)", contentType, err)
	}
	files, fields = map[string]string{}, map[string]string{}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return files, fields
		}
		if err != nil {
			t.Fatalf("reading multipart body: %v", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		// Part.FileName() keeps only the base name, so read the file name the
		// API receives (the path within the plugin) from the header itself.
		_, disposition, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if err != nil {
			t.Fatalf("part Content-Disposition: %v", err)
		}
		filename, isFile := disposition["filename"]
		if !isFile {
			fields[part.FormName()] = string(data)
			continue
		}
		if part.FormName() != "files[]" {
			t.Errorf("file part %q is named %q, want files[] (the API ignores any other name)", filename, part.FormName())
		}
		files[filename] = string(data)
	}
}

func TestListPluginSource_directory(t *testing.T) {
	t.Parallel()
	root := writePluginDir(t)

	src, err := listPluginSource(root, "")
	if err != nil {
		t.Fatalf("listPluginSource: %v", err)
	}
	if src.archive {
		t.Error("a directory source must not be flagged as an archive")
	}
	var names []string
	for _, f := range src.files {
		names = append(names, f.name)
	}
	want := []string{".claude-plugin/plugin.json", "skills/account-research/SKILL.md"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("upload names = %v, want %v (paths within the plugin, forward slashes, sorted)", names, want)
	}
}

func TestListPluginSource_directoryFollowsASymlinkedRoot(t *testing.T) {
	t.Parallel()
	root := writePluginDir(t)
	link := filepath.Join(t.TempDir(), "plugin")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	src, err := listPluginSource(link, "")
	if err != nil {
		t.Fatalf("listPluginSource: %v", err)
	}
	if len(src.files) != 2 || src.files[0].name != ".claude-plugin/plugin.json" {
		t.Errorf("files = %+v, want the plugin's two files relative to the link's target", src.files)
	}
}

func TestListPluginSource_rejects(t *testing.T) {
	t.Parallel()

	noManifest := t.TempDir()
	writeFile(t, filepath.Join(noManifest, "SKILL.md"), "---\nname: x\n---\n")

	withSymlink := writePluginDir(t)
	if err := os.Symlink(filepath.Join(withSymlink, ".claude-plugin", "plugin.json"), filepath.Join(withSymlink, "link.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	file := filepath.Join(t.TempDir(), "plugin.json")
	writeFile(t, file, "{}")

	for name, tc := range map[string]struct {
		dir, archive, want string
	}{
		"no manifest":       {dir: noManifest, want: ".claude-plugin/plugin.json"},
		"symlink inside":    {dir: withSymlink, want: "symbolic links"},
		"dir is a file":     {dir: file, want: "not a directory"},
		"missing dir":       {dir: filepath.Join(t.TempDir(), "missing"), want: "unable to read source_dir"},
		"missing archive":   {archive: filepath.Join(t.TempDir(), "missing.zip"), want: "unable to read source_archive"},
		"archive is a dir":  {archive: t.TempDir(), want: "not a regular file"},
		"archive wins over": {dir: noManifest, archive: filepath.Join(t.TempDir(), "x.zip"), want: "source_archive"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := listPluginSource(tc.dir, tc.archive)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

func TestPluginSourceHash_archiveIsTheFileHash(t *testing.T) {
	t.Parallel()
	archive := filepath.Join(t.TempDir(), "sales-toolkit.zip")
	writeFile(t, archive, "PK\x03\x04 not really a zip")

	src, err := listPluginSource("", archive)
	if err != nil {
		t.Fatalf("listPluginSource: %v", err)
	}
	if !src.archive || len(src.files) != 1 || src.files[0].name != "sales-toolkit.zip" {
		t.Fatalf("source = %+v, want one archive part named by its base name", src)
	}
	got, err := src.hash()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("PK\x03\x04 not really a zip"))
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Errorf("hash = %s, want the archive's own SHA-256 %s (what filesha256() gives)", got, want)
	}
}

func TestPluginSourceHash_directoryTracksContentAndPaths(t *testing.T) {
	t.Parallel()
	root := writePluginDir(t)
	hashOf := func() string {
		t.Helper()
		src, err := listPluginSource(root, "")
		if err != nil {
			t.Fatal(err)
		}
		h, err := src.hash()
		if err != nil {
			t.Fatal(err)
		}
		return h
	}

	base := hashOf()
	if again := hashOf(); again != base {
		t.Fatalf("hash is not stable: %s then %s", base, again)
	}

	skill := filepath.Join(root, "skills", "account-research", "SKILL.md")
	writeFile(t, skill, "---\nname: account-research\ndescription: Edited.\n---\n")
	edited := hashOf()
	if edited == base {
		t.Error("editing a file did not change the hash")
	}

	if err := os.Rename(skill, filepath.Join(root, "skills", "account-research", "skill.md")); err != nil {
		t.Fatal(err)
	}
	if renamed := hashOf(); renamed == edited {
		t.Error("renaming a file did not change the hash")
	}

	writeFile(t, filepath.Join(root, "README.md"), "hello")
	if added := hashOf(); added == edited {
		t.Error("adding a file did not change the hash")
	}
}

func TestPluginSourceUploadBody(t *testing.T) {
	t.Parallel()
	root := writePluginDir(t)
	src, err := listPluginSource(root, "")
	if err != nil {
		t.Fatal(err)
	}

	body, hash, err := src.uploadBody(
		formField{name: "marketplace_id", value: "marketplace_01"},
		formField{name: "release_notes", value: "First release"},
	)
	if err != nil {
		t.Fatalf("uploadBody: %v", err)
	}

	files, fields := uploadParts(t, body.ContentType, body.Data)
	if files[".claude-plugin/plugin.json"] != `{"name": "sales-toolkit", "version": "1.0.0"}` {
		t.Errorf("manifest part = %q, want the manifest under its path within the plugin", files[".claude-plugin/plugin.json"])
	}
	if _, ok := files["skills/account-research/SKILL.md"]; !ok || len(files) != 2 {
		t.Errorf("file parts = %v, want exactly the plugin's two files", files)
	}
	if fields["marketplace_id"] != "marketplace_01" || fields["release_notes"] != "First release" || len(fields) != 2 {
		t.Errorf("fields = %v, want marketplace_id and release_notes", fields)
	}

	planned, err := src.hash()
	if err != nil {
		t.Fatal(err)
	}
	if hash != planned {
		t.Errorf("upload hash = %s, want the plan-time hash %s of the same files", hash, planned)
	}
}

func TestPluginSourceUploadBody_refusesOversizedUploadsBeforeReading(t *testing.T) {
	t.Parallel()
	src := pluginSource{files: []pluginSourceFile{{name: "big.bin", path: "/does/not/exist", size: pluginUploadMaxBytes + 1}}}

	_, _, err := src.uploadBody()
	if err == nil || !strings.Contains(err.Error(), "200 MB") {
		t.Errorf("err = %v, want the 200 MB limit (and no attempt to open the file)", err)
	}
}

// A plugin whose root is a Git checkout holds a .git directory: uploading it
// published the repository's history and .git/config (where CI checkouts keep
// credentials) to every member, and its hash, which changes on every clone or
// fetch, made anthropic_plugin_version upload a new version on most applies.
// The walk leaves out .git, as a directory or as the file a worktree or a
// submodule has, at any depth, from the upload and from the hash alike. Other
// hidden paths stay: .claude-plugin/ and .mcp.json are plugin files.
func TestListPluginSource_leavesOutGitMetadata(t *testing.T) {
	t.Parallel()
	clean := writePluginDir(t)
	writeFile(t, filepath.Join(clean, ".mcp.json"), `{"mcpServers": {}}`)

	checkout := writePluginDir(t)
	writeFile(t, filepath.Join(checkout, ".mcp.json"), `{"mcpServers": {}}`)
	writeFile(t, filepath.Join(checkout, ".git", "config"), "[http]\n\textraheader = AUTHORIZATION: basic secret\n")
	writeFile(t, filepath.Join(checkout, ".git", "objects", "pack", "pack-1.pack"), "PACK")
	writeFile(t, filepath.Join(checkout, "skills", "vendored", ".git"), "gitdir: ../../.git/modules/vendored\n")

	src, err := listPluginSource(checkout, "")
	if err != nil {
		t.Fatalf("listPluginSource: %v", err)
	}
	var names []string
	for _, f := range src.files {
		names = append(names, f.name)
	}
	if got := strings.Join(names, ","); got != ".claude-plugin/plugin.json,.mcp.json,skills/account-research/SKILL.md" {
		t.Errorf("files = %s, want the plugin's files without any .git", got)
	}

	body, hash, err := src.uploadBody()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body.Data, []byte("secret")) || bytes.Contains(body.Data, []byte("gitdir")) {
		t.Error("the upload carries Git metadata")
	}
	if want := sourceHashOf(t, clean, ""); hash != want || sourceHashOf(t, checkout, "") != want {
		t.Errorf("hash = %s, want %s: the same plugin with or without .git must hash the same", hash, want)
	}
}

// A local user who can write under source_dir could swap a listed file, or
// its directory, for a symbolic link once the walk has checked it, and have
// any file the operator can read uploaded. Reads go through an os.Root on
// source_dir and check that the opened file is the one the listing saw, so
// each swap fails the hash and the upload, and the outside file is never
// sent.
func TestPluginSource_refusesAFileSwappedAfterTheListing(t *testing.T) {
	t.Parallel()
	const outsideContent = "outside-secret-content"

	for name, swap := range map[string]func(t *testing.T, root, outside string){
		"file for a link to an outside file": func(t *testing.T, root, outside string) {
			target := filepath.Join(root, "skills", "account-research", "SKILL.md")
			mustRemove(t, target)
			mustSymlink(t, filepath.Join(outside, "SKILL.md"), target)
		},
		"directory for a link to an outside directory": func(t *testing.T, root, outside string) {
			dir := filepath.Join(root, "skills", "account-research")
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
			mustSymlink(t, outside, dir)
		},
		"file for a link to another file inside": func(t *testing.T, root, _ string) {
			target := filepath.Join(root, "skills", "account-research", "SKILL.md")
			mustRemove(t, target)
			mustSymlink(t, filepath.Join(root, ".claude-plugin", "plugin.json"), target)
		},
		"file for another regular file": func(t *testing.T, root, outside string) {
			other := filepath.Join(root, "other.md")
			writeFile(t, other, outsideContent)
			if err := os.Rename(other, filepath.Join(root, "skills", "account-research", "SKILL.md")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := writePluginDir(t)
			outside := t.TempDir()
			writeFile(t, filepath.Join(outside, "SKILL.md"), outsideContent)

			src, err := listPluginSource(root, "")
			if err != nil {
				t.Fatalf("listPluginSource: %v", err)
			}
			swap(t, root, outside)

			if _, err := src.hash(); err == nil {
				t.Error("hash succeeded on a file swapped after the listing")
			}
			body, _, err := src.uploadBody()
			if err == nil {
				t.Error("uploadBody succeeded on a file swapped after the listing")
			}
			if bytes.Contains(body.Data, []byte(outsideContent)) {
				t.Error("the upload carries the swapped-in content")
			}
		})
	}
}

func TestPluginSource_refusesAnArchiveSwappedAfterTheListing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archive := filepath.Join(dir, "sales-toolkit.zip")
	writeFile(t, archive, "PK\x03\x04 the listed archive")
	src, err := listPluginSource("", archive)
	if err != nil {
		t.Fatalf("listPluginSource: %v", err)
	}
	other := filepath.Join(dir, "other")
	writeFile(t, other, "outside-secret-content")
	if err := os.Rename(other, archive); err != nil {
		t.Fatal(err)
	}

	if _, err := src.hash(); err == nil {
		t.Error("hash succeeded on an archive swapped after the listing")
	}
	if _, _, err := src.uploadBody(); err == nil {
		t.Error("uploadBody succeeded on an archive swapped after the listing")
	}
}

func mustRemove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// A directory swapped for a link to another directory inside source_dir (its
// .git, say) while the walk runs would bring that directory's files in under
// another name. Each directory is read from a handle checked against its
// parent's listing, so the walk of the swapped directory fails.
func TestWalkPluginDir_refusesADirectorySwappedForAnInRootLink(t *testing.T) {
	t.Parallel()
	root := writePluginDir(t)
	writeFile(t, filepath.Join(root, ".git", "config"), "[http]\n\textraheader = AUTHORIZATION: basic secret\n")
	sub := filepath.Join(root, "skills", "account-research")
	listed, err := os.Lstat(sub)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}
	// A relative link: os.Root already refuses an absolute one as escaping.
	mustSymlink(t, filepath.Join("..", ".git"), sub)

	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	var files []pluginSourceFile
	err = walkPluginDir(r, root, "skills/account-research", listed, &files)
	if err == nil || !strings.Contains(err.Error(), "no longer the directory that was listed") {
		t.Errorf("err = %v, want the swapped directory refused", err)
	}
	if len(files) != 0 {
		t.Errorf("files = %+v, want none from the link's target", files)
	}
}

// A file that grows after the listing would take the upload past the size
// checked before reading: the read stops at the listed size and fails.
func TestPluginSource_refusesAFileThatGrewAfterTheListing(t *testing.T) {
	t.Parallel()
	root := writePluginDir(t)
	src, err := listPluginSource(root, "")
	if err != nil {
		t.Fatalf("listPluginSource: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(root, "skills", "account-research", "SKILL.md"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("appended after the listing\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	if _, err := src.hash(); err == nil || !strings.Contains(err.Error(), "changed size") {
		t.Errorf("hash err = %v, want the size change refused", err)
	}
	if _, _, err := src.uploadBody(); err == nil || !strings.Contains(err.Error(), "changed size") {
		t.Errorf("uploadBody err = %v, want the size change refused", err)
	}
}
