// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package files_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the Files API (GA: no beta header, no ?beta=true, standard key).
// References: https://platform.claude.com/docs/en/api/files/upload,
// .../files/list, .../files/retrieve_metadata and .../files/delete.
const (
	routeUploadFile = "POST /v1/files"
	routeListFiles  = "GET /v1/files"
	routeGetFile    = "GET /v1/files/{file_id}"
	routeDeleteFile = "DELETE /v1/files/{file_id}"
)

var allRoutes = []string{routeUploadFile, routeListFiles, routeGetFile, routeDeleteFile}

// filesFake is the Files API on an sdktest collection.
type filesFake struct {
	*sdktest.Server
	files *sdktest.Collection
}

func newFilesFake(t *testing.T, pageSize int) *filesFake {
	t.Helper()
	srv := sdktest.NewServer(t)
	f := &filesFake{Server: srv, files: srv.NewCollection("file_")}
	f.Handle(routeUploadFile, f.upload)
	f.Handle(routeListFiles, func(w http.ResponseWriter, r *http.Request) { f.list(w, r, pageSize) })
	f.files.HandleGet(routeGetFile)
	f.files.HandleDelete(routeDeleteFile, "file_deleted")
	return f
}

// newFile stores a file as the upload reference describes it
// (https://platform.claude.com/docs/en/api/files/upload): never
// downloadable, and expiring expiresIn seconds after its creation, or never
// when expiresIn is 0; mapFileToState maps that expires_at.
func (f *filesFake) newFile(filename, mimeType string, size, expiresIn int) map[string]any {
	file := f.files.Create(map[string]any{
		"type": "file", "filename": filename, "mime_type": mimeType, "size_bytes": size, "downloadable": false, "expires_at": nil,
	})
	if expiresIn > 0 {
		created, _ := time.Parse(time.RFC3339, file["created_at"].(string))
		file["expires_at"] = created.Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339)
		f.files.Put(file)
	}
	return file
}

// upload reads the multipart form: the "file" part, of which only the final
// path component of the filename is kept (mime/multipart's FileName does
// that) and whose Content-Type becomes mime_type, and expires_in_seconds
// (the schema checks its range at plan).
func (f *filesFake) upload(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	var filename, mimeType string
	size, expiresIn := -1, 0
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			sdktest.WriteAPIError(w, http.StatusBadRequest, "", err.Error())
			return
		}
		content, _ := io.ReadAll(p)
		switch p.FormName() {
		case "file":
			filename, mimeType, size = p.FileName(), p.Header.Get("Content-Type"), len(content)
		case "expires_in_seconds":
			expiresIn, _ = strconv.Atoi(string(content))
		}
	}
	if size < 0 {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "file: field required")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, f.newFile(filename, mimeType, size, expiresIn))
}

// list pages with page/next_page, or, with ids[], answers the files named
// that exist in one page; ids is mutually exclusive with page and limit
// (https://platform.claude.com/docs/en/api/files/list), which is why
// FilesDataSource.Read sends neither with ids and reads a single page.
func (f *filesFake) list(w http.ResponseWriter, r *http.Request, pageSize int) {
	q := r.URL.Query()
	ids := q["ids[]"]
	if len(ids) == 0 {
		f.Paginate(w, r, sdktest.PageToken, pageSize, f.files.List(nil))
		return
	}
	if q.Has("page") || q.Has("limit") {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "ids is mutually exclusive with page and limit")
		return
	}
	found := f.files.List(func(file map[string]any) bool { return slices.Contains(ids, file["id"].(string)) })
	if found == nil {
		found = []map[string]any{}
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"data": found, "next_page": nil})
}

// assertRequestShape checks what every request of a test must carry: the
// standard key, and neither a beta header nor ?beta=true (GA endpoints).
func (f *filesFake) assertRequestShape(t *testing.T) {
	t.Helper()
	for _, route := range allRoutes {
		if len(f.Requests(route)) == 0 {
			continue
		}
		f.AssertNoBeta(route)
		f.AssertCredential(route, sdktest.APIKey)
		for _, r := range f.Requests(route) {
			if r.Query.Has("beta") {
				t.Errorf("%s %s with ?beta=", r.Method, r.Path)
			}
		}
	}
}

// uploads returns, for each upload request, its "file" part as
// "file=filename|content" followed by its other fields.
func (f *filesFake) uploads(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, r := range f.Requests(routeUploadFile) {
		parts, err := r.Parts()
		if err != nil {
			t.Fatalf("upload: %v", err)
		}
		file, fields := "", ""
		for _, p := range parts {
			if p.Field == "file" {
				file = fmt.Sprintf("file=%s|%s", p.Filename, p.Content)
				continue
			}
			fields += fmt.Sprintf(" %s=%s", p.Field, p.Content)
		}
		out = append(out, file+fields)
	}
	return out
}
