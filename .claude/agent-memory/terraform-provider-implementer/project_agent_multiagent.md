---
name: Agent model_effort and multiagent roster
description: Fork-only anthropic_agent fields; roster uses a type discriminator with object+list validators
type: project
---

`anthropic_agent` on this fork has two fields the parent does not:

- `model_effort` — optional+computed string, one of `low`/`medium`/`high`/`xhigh`/`max`
- `multiagent` — optional nested object, currently only `type = "coordinator"`, with an `agents` roster (1–20)

Roster entry contract (enforced by `multiagentRosterEntryValidator` + `multiagentRosterValidator` in `agent_resource.go`, not by attribute-level validators alone):

- `type = "agent"`: `id` required and non-empty; `version` optional+computed (API pins latest when omitted)
- `type = "self"`: `id` and `version` must be unset (self is a copy of the coordinator)
- At most one `self`; agent IDs unique across the roster

Unknown values at plan time are neither set nor missing — skip those entries in uniqueness checks.

Map `model_effort` and `multiagent` through Create/Read/Update **and** both agent data sources. Import must not drop a computed `version` the API returned.
