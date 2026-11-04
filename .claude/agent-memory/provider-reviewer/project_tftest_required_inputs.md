---
name: tftest assertions on Required data source inputs are weak
description: Asserting non-empty on a Required input in a .tftest.hcl is vacuous — flag these and suggest asserting on Computed fields instead
type: project
---

Checked on 2026-10-25. Re-check a test with `grep -n condition tests/<name>.tftest.hcl` against the data source's Required attributes.

In `tests/*.tftest.hcl`, assertions like `output.foo.required_input != ""` are always true when the config provides a non-empty value, regardless of what the API returned. They do not validate API round-trips.

**Why:** In tftest, `data "anthropic_x" "y" { required_input = ... }` populates `required_input` directly from config. The data source Read often does not re-assign Required inputs from the API response, so the tftest assertion just echoes config.

**How to apply:** When a tftest asserts on fields that are Required inputs to the data source, recommend the user either (a) assert only on Computed attributes (id, created_at, name) to validate the API response, or (b) have the data source's Read explicitly overwrite input fields with the API response value (then the tftest is meaningful). A third fix, used by `tests/skill_version_data_source.tftest.hcl` since this note was first written: compare the data source's attributes with the resource that created the object (`output.skill_version.version == anthropic_skill_version.example.version`).
