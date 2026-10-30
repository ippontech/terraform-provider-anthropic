// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// customToolNoParameters is what the request sends for a custom tool
// configured without an input_schema, which the API requires: once
// sentInputSchema adds the type, {"type": "object"}, a tool without parameters.
const customToolNoParameters = "{}"

// sentInputSchema is the input_schema the request sends for a configured
// one: the JSON object as configured, with "type": "object" added when it has
// none (the attribute's documented default, and the only type the API takes,
// https://platform.claude.com/docs/en/api/beta/agents/create). Anything but a
// JSON object is an error.
func sentInputSchema(configured string) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(configured))
	dec.UseNumber()
	var schema map[string]any
	if err := dec.Decode(&schema); err != nil {
		return nil, err
	}
	if schema == nil {
		return nil, errors.New("input_schema is null, not a JSON object")
	}
	if _, ok := schema["type"]; !ok {
		schema["type"] = "object"
	}
	return schema, nil
}

// customToolInputSchemaParam builds the input_schema of a request from the
// configured one (sentInputSchema). It is sent as a raw override: decoding
// it into the SDK struct dropped an empty schema (the SDK omits a zero
// struct) and one whose fields have other types than the struct's.
func customToolInputSchemaParam(configured string) (anthropic.BetaManagedAgentsCustomToolInputSchemaParam, error) {
	schema, err := sentInputSchema(configured)
	if err != nil {
		return anthropic.BetaManagedAgentsCustomToolInputSchemaParam{}, err
	}
	return param.Override[anthropic.BetaManagedAgentsCustomToolInputSchemaParam](schema), nil
}

// customToolInputSchemas returns the input_schema of each custom tool of the
// current (planned or prior) value, by name; none when it cannot be decoded.
func customToolInputSchemas(ctx context.Context, current types.List) map[string]jsontypes.Normalized {
	schemas := map[string]jsontypes.Normalized{}
	var tools []agentCustomToolModel
	if current.IsNull() || current.IsUnknown() || current.ElementsAs(ctx, &tools, false).HasError() {
		return schemas
	}
	for _, t := range tools {
		schemas[t.Name.ValueString()] = t.InputSchema
	}
	return schemas
}

// customToolInputSchemaState maps the input_schema of each custom tool the
// API returns, for the resource: customToolInputSchemaValue against the
// tool's current value, and the API's raw JSON for a tool the current value
// does not hold (import, or a tool added outside Terraform), so an imported
// {"type": "object"} reads as such rather than as a tool configured without
// a schema. A tool held with a null schema still reads null while the API
// returns {"type": "object"}.
func customToolInputSchemaState(current map[string]jsontypes.Normalized) func(name, raw string) jsontypes.Normalized {
	return func(name, raw string) jsontypes.Normalized {
		schema, ok := current[name]
		if !ok {
			return rawInputSchema(name, raw)
		}
		return customToolInputSchemaValue(raw, schema)
	}
}

// customToolInputSchemaValue is the state value of a custom tool's
// input_schema: the current value when the API returns the schema the
// request sent for it (with the type it added, or {"type": "object"} for a
// tool configured without one), so the configuration is kept as written,
// null included; else the API's raw JSON, null when absent.
func customToolInputSchemaValue(raw string, current jsontypes.Normalized) jsontypes.Normalized {
	if raw == "" || raw == "null" {
		return jsontypes.NewNormalizedNull()
	}
	if current.IsUnknown() {
		return jsontypes.NewNormalizedValue(raw)
	}
	configured := customToolNoParameters
	if !current.IsNull() {
		configured = current.ValueString()
	}
	if sent, err := sentInputSchema(configured); err == nil && sameJSON(sent, raw) {
		return current
	}
	return jsontypes.NewNormalizedValue(raw)
}

// sameJSON reports whether v and the JSON document raw hold the same value.
func sameJSON(v any, raw string) bool {
	encoded, err := json.Marshal(v)
	if err != nil {
		return false
	}
	var a, b any
	if json.Unmarshal(encoded, &a) != nil || json.Unmarshal([]byte(raw), &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}
