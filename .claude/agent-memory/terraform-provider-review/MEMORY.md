# Memory Index

- [Data source Go test convention](project_datasource_test_pattern.md) — All data sources historically ship a *_data_source_test.go; flag PRs that omit one
- [tftest assertions on Required inputs](project_tftest_required_inputs.md) — Asserting non-empty on a Required data source input is vacuous; suggest Computed fields instead
- [Environment resources architecture](project_environment_resources.md) — Four branches added environment CRUD + archive + data sources; tracks patterns and issues fixed
- [Configure method providerrors pattern](project_configure_pattern.md) — Flag inline nil checks on pd.Client/pd.AdminClient; must use providerrors.Require* helpers from internal/errors/
- [Test file placement](project_test_placement.md) — Tests must live next to the code they test; admin package tests belong in admin_client_test.go, not workspace files
- [Plan modifier patterns](project_plan_modifier_patterns.md) — UseStateForUnknown for stable Computed attrs; ModifyPlan for timestamps that change on updates
