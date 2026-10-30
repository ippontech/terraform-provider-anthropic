# Memory Index

- [Go toolchain mismatch in worktrees](project_go_toolchain_mismatch.md): "does not match go tool version" in a worktree, and `mise trust` for the poutine hook (kept 2026-10-25, not re-observed)
- [Data source native tests call the API even under plan](project_data_source_plan_still_calls_api.md): `command = plan` still runs a data source's Read; use `mock_provider` for constant-input data-source tests (checked 2026-10-25)
- [SDK param.Opt: null versus omitted in tests](project_sdk_param_opt.md): use `param.IsNull` / `param.IsOmitted`, not `Valid()` (checked 2026-10-25)
