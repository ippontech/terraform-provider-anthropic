---
name: JSON Schema string attributes — param.Override + configured-superset preservation
description: Do not Unmarshal JSON Schema into typed SDK params; send via param.Override and keep configured jsontypes.Normalized when the API payload is a subset
type: project
---

## Typed SDK params drop extra JSON Schema keywords

`BetaManagedAgentsCustomToolInputSchemaParam` only declares `type` / `properties` / `required`. `ExtraFields` is tagged `json:"-"`, so `encoding/json.Unmarshal` into that struct silently drops `additionalProperties`, `$schema`, `unevaluatedProperties`, etc. Terraform then fails apply with "Provider produced inconsistent result after apply" because the planned `jsontypes.Normalized` value still has those keys.

The SDK itself converts response → param with `param.Override[Param](json.RawMessage(r.RawJSON()))` (`ToParam`). Providers must do the same on the write path.

```go
func customToolInputSchemaParam(raw string) (anthropic.BetaManagedAgentsCustomToolInputSchemaParam, error) {
    var compact json.RawMessage
    if err := json.Unmarshal([]byte(raw), &compact); err != nil {
        return anthropic.BetaManagedAgentsCustomToolInputSchemaParam{}, err
    }
    return param.Override[anthropic.BetaManagedAgentsCustomToolInputSchemaParam](compact), nil
}
```

`param.Override` stores the bytes in param metadata. Parent marshal (`omitzero` on `input_schema`) still emits them because the metadata field is non-zero. Verified against SDK v1.67.0: extra keys survive both the param and the `BetaAgentNewParamsToolUnion` marshal.

## Map-to-state: keep configured JSON when it is a superset of the API payload

Read still uses `RawJSON()` into `jsontypes.Normalized` (never `json.Marshal` the SDK struct). After that, if prior plan/state has a schema for the same tool name and `jsonObjectCovers(configured, api)` (every API key/value is present in config; extra config keys allowed), keep the configured `jsontypes.Normalized` value — the exact planned value, not a re-serialisation.

Call this from `mapAgentResponseToState` using `data.CustomTools` *before* overwriting it:

- Create/Update: `data` is the plan → apply consistency
- Read/refresh: `data` is prior state → extra keywords survive refresh
- Import: CustomTools empty → store API payload (correct)

Do **not** apply this preservation on data sources; they should reflect the API.

## Tests

- Unit: marshal the param (and ideally the parent union) contains `additionalProperties`
- Unit: map-to-state keeps configured extras when API omits them
- Unit: map-to-state uses API when `type`/`properties`/`required` diverge
- Acc: `TestAccAgentResource_withCustomTools` should include `additionalProperties = false` so a live apply proves the original bug

## Pitfalls

- `jsonObjectCovers` iterating API keys means an empty API object `{}` or null schema is treated as a subset — that can hide remote deletion. Prefer preserving only when the API returned a non-null object.
- If the API *adds* keys (default `"type":"object"`), covers fails and the fallback stores the API payload without extras → inconsistent-result returns. Configs should include `type`.
- Key configured schemas by tool `name` (API may reorder the list). Names are documented unique; there is no `UniqueValues` validator yet.
- Internal-package tests for unexported helpers live in `*_internal_test.go` (`package agents`), not `_unit_test.go`.
