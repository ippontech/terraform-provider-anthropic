---
name: Environment resources architecture
description: Newly implemented environment resources follow specific patterns; track implementation details for consistency
type: project
---

## Environment Resources Implemented (2026-04-30)

Four feature branches introduced environment management resources and data sources:

### Resources
- `anthropic_environment` (feat/environment-resource)
  - CRUD operations for cloud environments (beta)
  - Supports nested config with networking (limited/unrestricted) and packages
  - Complex schema with nested objects requires careful attr.Type mapping
  - Pattern: Uses `mapEnvironmentResponseToState()` helper to convert API responses to state
  - Important: `buildEnvironmentConfigParams()` handles union types for networking config

- `anthropic_environment_archive` (feat/environment-archive-resource)
  - One-way archive operation (no unarchive)
  - Read checks if archived; auto-removes state if not archived
  - No-op Delete implementation (environment stays archived)
  - Uses `ImportStatePassthroughID` for import

### Data Sources
- `anthropic_environment` (feat/environment-data-source)
  - Read single environment by ID
  - Pattern: Example accepts var.environment_id for flexible documentation but tests must provide real ID

- `anthropic_environments` (feat/environments-data-source)
  - List all environments with auto-pagination via `ListAutoPaging()`
  - Supports `include_archived` optional filter
  - Returns list of nested objects with full environment details

## Issues Fixed During Review

1. **Version constraint**: The provider is now on major version 1.x (v1.14.0+). All examples must use `~> 1.0`, not `~> 0.1.0`.

2. **Data source test pattern issue**:
   - Original example required variable input, breaking Terraform native tests
   - Fixed by rewriting example to create a resource first, then read it via data source
   - This matches Go acceptance test pattern and works with tftest.hcl

## Attribute Type Patterns

All nested object schemas require explicit `attr.Type` maps defined at package level:
- `environmentNetworkingAttrTypes`, `environmentPackagesAttrTypes`, `environmentConfigAttrTypes` (resource)
- `envDSNetworkingAttrTypes`, `envDSPackagesAttrTypes`, etc. (data source variants)

Consider consolidating to reduce duplication if adding similar resources in future.

## Metadata Handling

Inconsistency noted: Resource sets empty metadata to `MapNull()` but data source uses `MapValueMust()` with empty map. Both work but could be standardized.
