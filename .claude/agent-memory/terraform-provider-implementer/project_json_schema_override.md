---
name: JSON Schema input_schema must use param.Override
description: Do not Unmarshal JSON Schema into typed SDK params; extra keywords like additionalProperties are dropped
type: project
---

`BetaManagedAgentsCustomToolInputSchemaParam` only has `type` / `properties` / `required`. `ExtraFields` is `json:"-"`, so `encoding/json.Unmarshal` silently drops `additionalProperties` and Terraform fails apply with "Provider produced inconsistent result after apply".

Send the user's JSON with `param.Override[Param](json.RawMessage(raw))`. On map-to-state, keep the configured `jsontypes.Normalized` value when it is a JSON-object superset of the API payload (`jsonObjectCovers`). Do not apply that preservation on data sources.

See `customToolInputSchemaParam` and `preserveConfiguredInputSchema` in `internal/services/agents/agent_resource.go`.
