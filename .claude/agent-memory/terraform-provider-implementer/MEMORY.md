# Memory Index

- [internal/errors providerrors pattern](project_errors_package.md) — Use providerrors.Require* helpers in Configure; never write inline nil checks on pd.Client/pd.AdminClient
- [Two-key provider model](project_api_key_model.md) — Standard resources use pd.Client (ANTHROPIC_API_KEY); admin/org resources use pd.AdminClient (ANTHROPIC_ADMIN_API_KEY)
- [Multipart upload retry](project_multipart_upload_retry.md) — File-uploading resources must use provretry.MultipartUpload; SDK cannot retry streaming multipart bodies
- [Example resource directory naming](project_example_naming.md) — Use resource name WITHOUT anthropic_ prefix: examples/resources/workspace_member/ not anthropic_workspace_member/
- [OTEL_TRACES_EXPORTER blocks make generate](project_otel_env.md) — Unset OTEL_TRACES_EXPORTER before running make; .env may set it to empty/invalid value
