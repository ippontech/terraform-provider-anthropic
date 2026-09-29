---
name: Multipart file upload retry
description: Resources that upload files must use provretry.MultipartUpload; the SDK cannot retry streaming multipart bodies on its own
type: project
---

## Use provretry.MultipartUpload for any file-uploading resource

The Anthropic Go SDK has built-in 5xx retry logic, but it skips retry when
`req.GetBody == nil`. Streaming multipart uploads always have `GetBody == nil`
(the body is a one-shot stream), so 5xx errors are silently not retried.

Use `provretry.MultipartUpload` from `internal/retry/` instead of opening files
manually:

```go
import provretry "github.com/ippontech/terraform-provider-anthropic/internal/retry"

result, err := provretry.MultipartUpload(ctx, filePaths, dirName,
    func(files []io.Reader) (*anthropic.BetaSkillNewResponse, error) {
        return r.client.Beta.Skills.New(ctx, anthropic.BetaSkillNewParams{Files: files})
    },
)
```

The helper re-opens files from disk on each attempt (up to 3 tries, 5s/10s
backoff) and returns immediately on non-5xx errors or file-open errors.

**How to apply:** Any new resource whose `Create` method opens `os.File` handles
and passes them to an API call must use `provretry.MultipartUpload`. Never
replicate the retry loop inline.

**Exception:** `provretry.MultipartUpload` is shaped for a *bundle* of files
uploaded together under one directory name (`[]io.Reader`, multipart name
`dirName + "/" + relPath`, e.g. skills). `anthropic_file`
(`internal/services/files/file_resource.go`, uploading via `client.Files.Upload`
— the GA service, see [[project_files_api_ga_but_sdk_pinned_beta]] — which takes
a single `File io.Reader` field, not a slice) doesn't fit that shape — the
upload needs one file with an explicit, user-controlled `filename`, not a
bundle-relative path. Reusing the helper there would force an artificial
`dirName/filename` multipart name unrelated to the schema's `filename`
attribute, so it instead has its own small local retry loop
(`uploadFileWithRetry`) that mirrors the open-fresh-per-attempt / 3-tries
shape but retries **only on 429**, not on 5xx: `POST /v1/files` is not
idempotent (each call creates a new file object), so — per the same
POST-retry caution documented in CLAUDE.md for `internal/admin`'s
`DoRequest` — a 5xx, a 409 or a dropped connection may mean the write already
landed, and retrying then would silently create a duplicate file. Only 429
guarantees the request was never processed. If a second single-file upload
resource shows up, consider factoring a `provretry.SingleFileUpload` helper
instead of a third copy — and give it the same 429-only retry policy, not
`provretry.MultipartUpload`'s 5xx one (that helper's own POST — skill
creation — is presumably safe to retry on 5xx only because skill names are
deduplicated server-side or the caller tolerates duplicates; verify before
assuming the same policy applies to a new single-file case).
