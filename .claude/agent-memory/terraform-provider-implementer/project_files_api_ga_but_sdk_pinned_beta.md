---
name: Files API is GA in the pinned SDK — use client.Files, not client.Beta.Files
description: v1.67.0 already ships a non-beta client.Files (file.go); anthropic_file must use it, not client.Beta.Files
type: project
---

Corrected 2026-09-29: an earlier version of this note claimed v1.67.0 only ships `client.Beta.Files` and that `client.Files` doesn't exist until a later SDK version. That was wrong — checked directly against the vendored module (`$(go env GOPATH)/pkg/mod/github.com/anthropics/anthropic-sdk-go@v1.67.0/file.go`): `FileService` (`client.Files`, GA, no beta header) is present in v1.67.0 alongside `betafile.go` (`client.Beta.Files`, the pre-GA shape). The client struct (`client.go`) wires both: `Files FileService` and `Beta.Files BetaFileService`.

The two services differ in real, user-facing ways, not just naming:
- `client.Files.Upload` (`FileUploadParams`) supports `ExpiresInSeconds` (3600-7776000); `client.Beta.Files.Upload` (`BetaFileUploadParams`) does not.
- `FileMetadata` has `ExpiresAt`; `BetaFileMetadata` does not.
- `client.Files.List` uses GA pagination (`page`/`next_page`, plus an `ids[]` filter, ≤100 entries, mutually exclusive with `page`/`limit`); `client.Beta.Files.List` uses the older `after_id`/`before_id` cursor shape and requires the beta header or gets a 400 (relevant to `anthropic_files`, issue #87, not yet implemented).
- `client.Files.Delete`/`GetMetadata`/`Download` take no extra params struct (`ctx, fileID`); the Beta equivalents take an extra empty params struct (`ctx, fileID, BetaFileDeleteParams{}` etc.) even though the Beta struct doesn't have a body field.

**How to apply:** `anthropic_file` (`internal/services/files/file_resource.go`) uses `client.Files.*` and `anthropic.FileMetadata`/`anthropic.FileUploadParams`, never `Beta.Files`/`BetaFile*`. When implementing `anthropic_files` (issue #87), use `client.Files.List` (GA) too, not `client.Beta.Files.List` — same reasoning applies. See [[project_sdk_v168_skills_break]] for why the SDK is pinned to v1.67.0 at all; that pin is about *skills*, not files — it does not force the Beta Files service.
