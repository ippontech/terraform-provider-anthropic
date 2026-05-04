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
