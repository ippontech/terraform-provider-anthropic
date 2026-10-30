// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"os"
	"path/filepath"
	"sort"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

const (
	// pluginManifestPath is where every plugin keeps its manifest. An upload
	// must contain exactly one, at this path.
	pluginManifestPath = ".claude-plugin/plugin.json"

	// pluginUploadMaxBytes is the Plugins API's request body limit (200 MB).
	// It is checked on the files' total size before they are read into memory:
	// an upload that big would only earn a 413.
	pluginUploadMaxBytes = 200 << 20

	// pluginArchivePattern matches the archive formats the upload accepts.
	pluginArchivePattern = `(?i)\.(zip|plugin)$`

	// gitMetadataName is the directory (or, in a worktree or submodule, the
	// file) a Git checkout keeps its metadata in. A source_dir walk leaves it
	// out: it would publish the repository's history and .git/config, and its
	// content changes on every clone or fetch.
	gitMetadataName = ".git"
)

// pluginSource is the content of one upload: either every file under a plugin
// directory, each named by its path within the plugin, or a single `.zip` or
// `.plugin` archive named by its base name.
type pluginSource struct {
	archive bool
	files   []pluginSourceFile
}

type pluginSourceFile struct {
	// name is the multipart file name the API reads: the file's path within
	// the plugin, with forward slashes, or an archive's base name.
	name string
	path string
	size int64
}

// formField is a plain (non-file) multipart field.
type formField struct {
	name  string
	value string
}

// listPluginSource lists the files to upload from a plugin directory, or from
// a single archive when archive is set (dir is then ignored).
//
// A directory upload sends every regular file under it except Git metadata
// (gitMetadataName, at any depth), so point it at the plugin's root: a Git
// checkout whose root is the plugin's root is fine, a larger repository is
// not. Symbolic links and special files are refused rather than followed or
// skipped, the way the API refuses them inside an archive.
func listPluginSource(dir, archive string) (pluginSource, error) {
	if archive != "" {
		info, err := os.Stat(archive)
		if err != nil {
			return pluginSource{}, fmt.Errorf("unable to read source_archive %q: %w", archive, err)
		}
		if !info.Mode().IsRegular() {
			return pluginSource{}, fmt.Errorf("source_archive %q is not a regular file", archive)
		}
		return pluginSource{
			archive: true,
			files:   []pluginSourceFile{{name: filepath.Base(archive), path: archive, size: info.Size()}},
		}, nil
	}

	// Resolve a symlinked root once, so the walk below (which never follows
	// links) starts from the real directory.
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return pluginSource{}, fmt.Errorf("unable to read source_dir %q: %w", dir, err)
	}
	if info, err := os.Stat(root); err != nil {
		return pluginSource{}, fmt.Errorf("unable to read source_dir %q: %w", dir, err)
	} else if !info.IsDir() {
		return pluginSource{}, fmt.Errorf("source_dir %q is not a directory", dir)
	}

	var files []pluginSourceFile
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Git metadata is not plugin content: a .git directory (or the .git
		// file of a worktree or submodule) is left out of the upload and of the
		// hash, at any depth.
		if d.Name() == gitMetadataName && p != root {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%q is not a regular file: symbolic links and special files cannot be uploaded", p)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, pluginSourceFile{name: filepath.ToSlash(rel), path: p, size: info.Size()})
		return nil
	})
	if err != nil {
		return pluginSource{}, fmt.Errorf("unable to read source_dir %q: %w", dir, err)
	}

	// Sort by upload name, so the hash does not depend on the platform's path
	// separator or on the walk order.
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })

	hasManifest := false
	for _, f := range files {
		if f.name == pluginManifestPath {
			hasManifest = true
			break
		}
	}
	if !hasManifest {
		return pluginSource{}, fmt.Errorf("source_dir %q has no %s: every plugin needs its manifest there", dir, pluginManifestPath)
	}

	return pluginSource{files: files}, nil
}

// hash returns the SHA-256 of the source, streaming the files from disk: for
// an archive, the hash of its bytes (the value `filesha256()` gives); for a
// directory, a hash over each file's upload name and content hash, so a
// renamed, added or removed file changes it as much as an edit does.
func (s pluginSource) hash() (string, error) {
	sums := make([][]byte, len(s.files))
	for i, f := range s.files {
		sum, err := hashFile(f.path)
		if err != nil {
			return "", err
		}
		sums[i] = sum
	}
	return s.combineHashes(sums), nil
}

// uploadBody builds the multipart body of a plugin or version upload: one
// `files[]` part per file, named by its upload name (the encoding the SDK uses,
// and the only part name the API reads), then the given form fields. It returns
// the source hash of the bytes it actually read, so a caller can check that the
// content did not change since the plan.
func (s pluginSource) uploadBody(fields ...formField) (admin.RawBody, string, error) {
	var total int64
	for _, f := range s.files {
		total += f.size
	}
	if total > pluginUploadMaxBytes {
		return admin.RawBody{}, "", fmt.Errorf("the plugin's files total %d bytes, over the API's 200 MB upload limit", total)
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	sums := make([][]byte, len(s.files))
	for i, f := range s.files {
		part, err := w.CreateFormFile("files[]", f.name)
		if err != nil {
			return admin.RawBody{}, "", fmt.Errorf("unable to encode %q: %w", f.path, err)
		}
		h := sha256.New()
		if err := copyFile(io.MultiWriter(part, h), f.path); err != nil {
			return admin.RawBody{}, "", err
		}
		sums[i] = h.Sum(nil)
	}
	for _, field := range fields {
		if err := w.WriteField(field.name, field.value); err != nil {
			return admin.RawBody{}, "", fmt.Errorf("unable to encode %s: %w", field.name, err)
		}
	}
	if err := w.Close(); err != nil {
		return admin.RawBody{}, "", fmt.Errorf("unable to encode the upload: %w", err)
	}

	return admin.RawBody{ContentType: w.FormDataContentType(), Data: buf.Bytes()}, s.combineHashes(sums), nil
}

func (s pluginSource) combineHashes(sums [][]byte) string {
	if s.archive {
		return hex.EncodeToString(sums[0])
	}
	h := sha256.New()
	for i, f := range s.files {
		_, _ = fmt.Fprintf(h, "%s\x00%x\n", f.name, sums[i])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashFile(path string) ([]byte, error) {
	h := sha256.New()
	if err := copyFile(h, path); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func copyFile(dst io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("unable to open %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(dst, f); err != nil {
		return fmt.Errorf("unable to read %q: %w", path, err)
	}
	return nil
}
