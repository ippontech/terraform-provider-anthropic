---
name: Example resource directory naming
description: Resource example directories use resource name without provider prefix (workspace_member not anthropic_workspace_member)
type: project
---

The `examples/resources/` directory uses the resource name WITHOUT the `anthropic_` provider prefix.
e.g., `examples/resources/workspace_member/resource.tf` not `examples/resources/anthropic_workspace_member/resource.tf`.

This matches the pattern of all existing resources: `workspace/`, `agent/`, `skill/`, `environment/`, etc.

**Why:** The docs generator (tfplugindocs) maps resource names to example paths without the provider prefix.

**How to apply:** When creating a new resource, create the example at `examples/resources/<name>/resource.tf` (strip the `anthropic_` prefix).
