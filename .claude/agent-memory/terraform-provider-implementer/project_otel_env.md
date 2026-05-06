---
name: OTEL_TRACES_EXPORTER blocks make generate
description: OTEL_TRACES_EXPORTER env var causes make generate to fail; unset it before running
type: project
---

`make generate` calls tfplugindocs which calls `terraform init`. If `OTEL_TRACES_EXPORTER` is set to an invalid value (like empty string), Terraform errors with "invalid OTLP protocol".

**Why:** The `.env` file sets `OTEL_TRACES_EXPORTER=` (empty), which is invalid. Sourcing `.env` breaks `make generate`.

**How to apply:** Run `unset OTEL_TRACES_EXPORTER && make` (or just `make` without sourcing `.env`) when running make. The `.env` file was gitignored and machine-specific — do not source it if it sets OTEL vars.
