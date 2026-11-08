// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path"
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
	// root is the resolved source_dir, which a directory source lists and
	// reads its files through (os.Root): a path that resolves outside it, a
	// link included, is refused when it is opened, not only when it is
	// listed. Empty for an archive.
	root  string
	files []pluginSourceFile
}

type pluginSourceFile struct {
	// name is the multipart file name the API reads: the file's path within
	// the plugin, with forward slashes, or an archive's base name.
	name string
	// path is the file on disk, for messages and, for an archive, to open it.
	path string
	size int64
	// info is the file the listing saw. A read refuses any other file
	// (os.SameFile), so a file swapped for a link or another file between
	// the listing and the read is neither hashed nor uploaded.
	info os.FileInfo
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
		// The identity comes from an open handle: on Windows, a FileInfo from
		// os.Stat loads the file ID only when os.SameFile needs it, by opening
		// the path again, which would compare the file with itself.
		info, err := statOpen(archive)
		if err != nil {
			return pluginSource{}, fmt.Errorf("unable to read source_archive %q: %w", archive, err)
		}
		if !info.Mode().IsRegular() {
			return pluginSource{}, fmt.Errorf("source_archive %q is not a regular file", archive)
		}
		return pluginSource{
			archive: true,
			files:   []pluginSourceFile{{name: filepath.Base(archive), path: archive, size: info.Size(), info: info}},
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

	// The walk goes through an os.Root, and reads each directory from a
	// handle it has checked (walkPluginDir).
	r, err := os.OpenRoot(root)
	if err != nil {
		return pluginSource{}, fmt.Errorf("unable to read source_dir %q: %w", dir, err)
	}
	defer func() { _ = r.Close() }()

	var files []pluginSourceFile
	err = walkPluginDir(r, root, ".", nil, &files)
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

	return pluginSource{root: root, files: files}, nil
}

// walkPluginDir appends the regular files under dir (a path within the root,
// with forward slashes) to files, leaving out Git metadata: a .git directory
// (or the .git file of a worktree or submodule) is not plugin content, at any
// depth.
//
// Each directory is opened through the root and read from that handle, after
// checking that the handle is the directory its parent's listing saw (listed;
// nil for the root). A directory swapped for a link while the walk runs, to a
// directory outside source_dir or inside it (its .git, say), fails the
// listing instead of adding that directory's files under another name. The
// entries' FileInfo come from the same handle (lstatat).
//
// Limit: on Windows, Go reads the file IDs that os.SameFile compares with
// the directory listing on NTFS, but on a file system that does not report
// them (FAT, some network shares) it loads them later by opening the path
// again, so a swap there goes unnoticed. Taking the ID from an open handle
// at listing time would not help: Go has no way to open a file on Windows
// without following a link in its last component, so the handle could be a
// link's target.
func walkPluginDir(r *os.Root, root, dir string, listed os.FileInfo, files *[]pluginSourceFile) error {
	onDisk := filepath.Join(root, filepath.FromSlash(dir))
	f, err := r.OpenFile(filepath.FromSlash(dir), openFlags, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() || (listed != nil && !os.SameFile(info, listed)) {
		return fmt.Errorf("%q is no longer the directory that was listed: symbolic links cannot be uploaded", onDisk)
	}
	entries, err := f.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == gitMetadataName {
			continue
		}
		p := path.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			if err := walkPluginDir(r, root, p, info, files); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			*files = append(*files, pluginSourceFile{name: p, path: filepath.Join(root, filepath.FromSlash(p)), size: info.Size(), info: info})
		default:
			return fmt.Errorf("%q is not a regular file: symbolic links and special files cannot be uploaded", filepath.Join(root, filepath.FromSlash(p)))
		}
	}
	return nil
}

// statOpen returns the FileInfo of path read from an open handle.
func statOpen(path string) (os.FileInfo, error) {
	f, err := os.OpenFile(path, openFlags, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return f.Stat()
}

// hash returns the SHA-256 of the source, streaming the files from disk: for
// an archive, the hash of its bytes (the value `filesha256()` gives); for a
// directory, a hash over each file's upload name and content hash, so a
// renamed, added or removed file changes it as much as an edit does.
func (s pluginSource) hash() (string, error) {
	r, err := s.reader()
	if err != nil {
		return "", err
	}
	defer r.close()
	sums := make([][]byte, len(s.files))
	for i, f := range s.files {
		h := sha256.New()
		if err := r.copy(h, f); err != nil {
			return "", err
		}
		sums[i] = h.Sum(nil)
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

	r, err := s.reader()
	if err != nil {
		return admin.RawBody{}, "", err
	}
	defer r.close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	sums := make([][]byte, len(s.files))
	for i, f := range s.files {
		part, err := w.CreateFormFile("files[]", f.name)
		if err != nil {
			return admin.RawBody{}, "", fmt.Errorf("unable to encode %q: %w", f.path, err)
		}
		h := sha256.New()
		if err := r.copy(io.MultiWriter(part, h), f); err != nil {
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

// sourceReader reads the files of one source for one pass (a hash or an
// upload).
type sourceReader struct {
	// root is source_dir opened as an os.Root, nil for an archive.
	root *os.Root
}

func (s pluginSource) reader() (*sourceReader, error) {
	if s.archive {
		return &sourceReader{}, nil
	}
	r, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, fmt.Errorf("unable to read source_dir %q: %w", s.root, err)
	}
	return &sourceReader{root: r}, nil
}

func (r *sourceReader) close() {
	if r.root != nil {
		_ = r.root.Close()
	}
}

// copy writes the content of f to dst. A directory file is opened through the
// root, which refuses a path that resolves outside source_dir; any file must
// then be the regular file the listing saw, so a swap made after the listing
// fails the read instead of sending another file's bytes. The open does not
// block (openFlags), so a file swapped for a FIFO is refused instead of
// hanging the run, and at most the listed size is read, so a file that grew
// since cannot take the upload past the size checked before reading.
func (r *sourceReader) copy(dst io.Writer, f pluginSourceFile) error {
	var file *os.File
	var err error
	if r.root != nil {
		file, err = r.root.OpenFile(filepath.FromSlash(f.name), openFlags, 0)
	} else {
		file, err = os.OpenFile(f.path, openFlags, 0)
	}
	if err != nil {
		return fmt.Errorf("unable to open %q: %w", f.path, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("unable to read %q: %w", f.path, err)
	}
	if !info.Mode().IsRegular() || f.info == nil || !os.SameFile(info, f.info) {
		return fmt.Errorf("%q is not a regular file, or is no longer the file that was listed: symbolic links and "+
			"special files cannot be uploaded", f.path)
	}
	n, err := io.Copy(dst, io.LimitReader(file, f.size+1))
	if err != nil {
		return fmt.Errorf("unable to read %q: %w", f.path, err)
	}
	if n != f.size {
		return fmt.Errorf("%q changed size since it was listed (%d bytes, now %d or more): read the source again", f.path, f.size, n)
	}
	return nil
}
