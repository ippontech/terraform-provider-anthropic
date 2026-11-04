# Memory Index

- [Plan modifier patterns for Computed attributes](project_plan_modifier_patterns.md): a bare `Computed` `updated_at` keeps no-op plans empty; `UseStateForUnknown` only on values an update does not change (checked 2026-11-02)
- [tftest assertions on Required data source inputs are weak](project_tftest_required_inputs.md): assert on computed attributes or compare with the creating resource (checked 2026-10-25)
