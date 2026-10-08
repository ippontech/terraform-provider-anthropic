# Memory Index

- [Multipart upload retry](project_multipart_upload_retry.md) — File-uploading resources must use provretry.MultipartUpload; SDK cannot retry streaming multipart bodies; why anthropic_file has its own 429-only loop instead
- [Go toolchain mismatch in worktrees](project_go_toolchain_mismatch.md) — "does not match go tool version": GOTOOLCHAIN=local + prepend mise go bin + unset GOROOT; also `mise trust` for poutine hook; never read .env (mise loads it)
- [OAuth/WIF resource series (#137)](project_oauth_wif_series.md) — PreCheckOAuth skips (not Fatal); command=plan native test needs no assert block when Computed attrs are unknown; param.IsNull/IsOmitted for testing param.Opt
- [Data source native tests call the API even under plan](project_data_source_plan_still_calls_api.md) — command=plan doesn't skip a data source's Read; use mock_provider for constant-input data-source-only tests
