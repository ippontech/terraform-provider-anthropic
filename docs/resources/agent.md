---
page_title: "anthropic_agent Resource - anthropic"
subcategory: "Managed Agents"
description: |-
  Creates and manages a Managed Agent on the Anthropic platform (beta). Agents are persistent configurations that can be used to run sessions with tools, MCP servers, and skills.
---

# anthropic_agent (Resource)

Creates and manages a Managed Agent on the Anthropic platform (beta). Agents are persistent configurations that can be used to run sessions with tools, MCP servers, and skills.

> **API**: `POST/GET/PATCH/POST /v1/agents[/{id}][/archive]` (Managed Agents API).
> **Auth**: Standard API key (`api_key` / `ANTHROPIC_API_KEY`).
> **Beta header**: `anthropic-beta: managed-agents-2026-04-01`.
> **Cost**: Free for management, billable at runtime.

## Example Usage

```hcl
# Minimal agent. model_effort is optional: when omitted, the API resolves a
# per-model default and stores it in state.
resource "anthropic_agent" "simple" {
  model        = "claude-sonnet-4-6"
  name         = "Simple Agent"
  model_effort = "high"
}

# Agent with system prompt and description
resource "anthropic_agent" "assistant" {
  model       = "claude-sonnet-4-6"
  name        = "Customer Support Agent"
  description = "Handles customer support inquiries"
  system      = "You are a helpful customer support agent. Be concise and friendly."

  metadata = {
    team = "support"
    env  = "production"
  }
}

# Agent with built-in toolset configuration. The web tools take domain lists
# (allowed_domains or blocked_domains), web_search a user_location and
# web_fetch a max_content_tokens cap. With `limited` environment networking,
# allowed_domains entries must also be within the environment's allowed_hosts.
# web_fetch only fetches URLs already seen in the session; url_sources narrows
# where they may come from, here user messages and web_search results only, so
# a URL planted in a custom tool result is never fetched.
resource "anthropic_agent" "developer" {
  model       = "claude-sonnet-4-6"
  name        = "Developer Agent"
  description = "A coding assistant with file and web access"

  agent_toolset = {
    default_enabled           = true
    default_permission_policy = "always_allow"
    configs = [
      {
        name              = "bash"
        enabled           = true
        permission_policy = "always_ask"
      },
      {
        name            = "web_search"
        allowed_domains = ["docs.example.com", "arxiv.org"]
        user_location = {
          country  = "US"
          timezone = "America/Los_Angeles"
        }
      },
      {
        name               = "web_fetch"
        blocked_domains    = ["ads.example.com"]
        max_content_tokens = 50000
        url_sources = {
          client_tool_results = { type = "none" }
          server_tool_results = { type = "only", tools = ["web_search"] }
        }
      }
    ]
  }
}

# Agent with MCP server and skills. With the `auto` permission policy the
# server evaluates each call and runs it, denies it or waits for approval; a
# call it judges safe runs before anyone sees it, so tools whose calls a
# person must review keep `always_ask`.
resource "anthropic_agent" "mcp_agent" {
  model       = "claude-sonnet-4-6"
  name        = "MCP Agent"
  description = "An agent connected to an MCP server"

  mcp_servers = [
    {
      name = "my_server"
      url  = "https://mcp.example.com/sse"
    }
  ]

  mcp_toolsets = [
    {
      mcp_server_name           = "my_server"
      default_enabled           = true
      default_permission_policy = "auto"
      configs = [
        {
          name              = "delete_record"
          permission_policy = "always_ask"
        }
      ]
    }
  ]

  skills = [
    {
      type     = "anthropic"
      skill_id = "xlsx"
    }
  ]
}

# Agent with custom tools
resource "anthropic_agent" "custom_tools" {
  model       = "claude-sonnet-4-6"
  name        = "Custom Tool Agent"
  description = "An agent with a custom lookup tool"

  custom_tools = [
    {
      name        = "lookup_user"
      description = "Look up a user by their email address"
      input_schema = jsonencode({
        type = "object"
        properties = {
          email = {
            type        = "string"
            description = "The user's email address"
          }
        }
        required = ["email"]
      })
    }
  ]
}

# Coordinator agent that can delegate to another agent and to itself.
# The members must not have their own multiagent roster (depth limit 1).
resource "anthropic_agent" "coordinator" {
  model       = "claude-sonnet-4-6"
  name        = "Support Coordinator"
  description = "Delegates support inquiries to the support agent"

  multiagent = {
    type = "coordinator"
    agents = [
      {
        type = "agent"
        id   = anthropic_agent.assistant.id
      },
      {
        type = "self"
      },
    ]
  }
}

# Agent of the multiagent_20261001 type: it may delegate only to the support
# agent and to copies of itself (inline agents off), and dynamic workflows are
# turned off. Both settings are on by default with this type; an advisor
# model can be added with advisor = { model = "..." }.
resource "anthropic_agent" "orchestrator" {
  model       = "claude-sonnet-4-6"
  name        = "Support Orchestrator"
  description = "Delegates support inquiries to the support agent only"

  multiagent = {
    type = "multiagent_20261001"
    subagents = {
      inline_agents_enabled = false
      predefined_agents = [
        {
          type = "agent"
          id   = anthropic_agent.assistant.id
        },
        {
          type = "self"
        },
      ]
    }
    workflows = {
      enabled = false
    }
  }
}

output "simple_agent_id" {
  description = "ID of the minimal agent."
  value       = anthropic_agent.simple.id
}

output "simple_agent_model_effort" {
  description = "Effort level of the minimal agent."
  value       = anthropic_agent.simple.model_effort
}

output "developer_agent_version" {
  description = "Version number of the developer agent."
  value       = anthropic_agent.developer.version
}

output "coordinator_roster_size" {
  description = "Number of entries in the coordinator agent's roster."
  value       = length(anthropic_agent.coordinator.multiagent.agents)
}
```

## Permission policies

`default_permission_policy` (on `agent_toolset` and on each `mcp_toolsets` entry) and the per-tool `permission_policy` overrides take one of three values ([Permission policies](https://platform.claude.com/docs/en/managed-agents/permission-policies)):

- `always_allow`: the tool runs with no confirmation. The agent toolset uses it when no default is set.
- `always_ask`: the session pauses until your client approves or denies the call. MCP toolsets use it when no default is set, so tools added to an MCP server later do not run without approval.
- `auto`: the server evaluates each call from the tool, its input and the session so far, then runs it, denies it (the agent gets an error tool result) or pauses for approval when it reaches no determination. No toolset uses it by default.

Those defaults are what the API applies to a toolset sent without `default_permission_policy`. Removing the attribute from an existing toolset does not plan a change on its own, since the attribute is computed: to change the policy, set the value you want, `always_ask` for instance.

`auto` is not a human checkpoint: a call the server judges safe runs before anyone sees it, and its effects might not be reversible. Keep `always_ask` on the tools whose calls a person must review. The server reads what your client posts in `user.message` events as your intent, so relaying untrusted end-user input there can get a call allowed. Permission policies do not apply to `custom_tools`, which your application runs.

## Multiagent orchestration

`multiagent` takes one of two types ([Multiagent orchestration](https://platform.claude.com/docs/en/managed-agents/multiagent-orchestration)):

- `coordinator`: the agent delegates to the agents listed in `agents`, which can also hold `self` (copies of the agent) and one `advisor` model.
- `multiagent_20261001`: three settings, each turned on or off on its own. `subagents` lets the agent delegate to subagents in their own session threads, `workflows` lets it write dynamic workflows that the server runs in the background with many agents, and `advisor` gives the session's primary thread a model to consult mid-turn. `subagents` and `workflows` are on by default, each with inline agents (agents defined on the fly, never saved) besides the agents listed in its own `predefined_agents`; the advisor is off by default. To allow only the agents you list, set `inline_agents_enabled = false` under both settings and list at least one agent in each. Every agent in a run uses tokens, so set a session budget when you create sessions for an agent with workflows on.

The provider sends the whole `multiagent_20261001` object on every create and update, defaults included, so the API's level-by-level merge of an update into the stored configuration always leaves exactly what the configuration says. A setting left out of the configuration stays null in state while the API holds its default, and shows as a change when it was modified outside Terraform. To move an agent from `coordinator` to `multiagent_20261001`, list its agents in `subagents.predefined_agents` and its advisor in `advisor`: the API replaces the whole object when the type changes.

## Execution identity

`execution_identity` names the principal the agent's runs act as. By default they use the Anthropic-managed service account (`type = "service_account"`), which carries no identifier. The API returns that default for every agent, and the provider keeps the attribute null while it is unset; removing the attribute from the configuration restores the default on the next update.

On [Claude Platform on AWS](https://platform.claude.com/docs/en/build-with-claude/claude-platform-on-aws), each run of a scheduled deployment creates its session as an IAM role in your AWS account, so the agent needs an `aws_role` identity before an `anthropic_deployment` can use it: creating a deployment for an agent without one returns a 400 error ([Scheduled deployments](https://platform.claude.com/docs/en/managed-agents/scheduled-deployments)).

```terraform
resource "anthropic_agent" "scheduled" {
  model = "claude-sonnet-4-6"
  name  = "Weekly compliance scan"

  execution_identity = {
    type     = "aws_role"
    role_arn = "arn:aws:iam::123456789012:role/agent-runner"
  }
}
```

The role must be in the same AWS account and needs:

- **Trust policy:** allow the `aws-external-anthropic.amazonaws.com` service principal to call `sts:AssumeRole` and `sts:SetSourceIdentity`.
- **Role permissions:** allow `aws-external-anthropic:CreateSession`, `sts:GetWebIdentityToken` and `sts:TagGetWebIdentityToken`.
- **Caller permissions:** the IAM principal that sets the role on the agent, the one Terraform applies with, needs `iam:PassRole` on that role. No managed policy grants `iam:PassRole`.

## Web tool restrictions

The `web_search` and `web_fetch` entries of `agent_toolset.configs` accept `allowed_domains` or `blocked_domains` (not both), `web_fetch` also accepts `max_content_tokens` and `url_sources`, and `web_search` a `user_location`. Setting one of them on another tool fails at plan time. See [Restrict web search and web fetch domains](https://platform.claude.com/docs/en/managed-agents/tools-web-restrictions) for the domain format the API accepts.

`web_fetch` only fetches a URL that appeared earlier in the session: in a user message, in a custom tool's result, or in a `web_search` or `web_fetch` result. `url_sources` narrows each of these sources, so a URL that a tool result plants (prompt injection) is not fetched: `user_input` takes `all` or `none`, and `client_tool_results` and `server_tool_results` take a `type` of `all`, `none`, `only` or `except`, the last two with a `tools` list (names of `custom_tools` entries for `client_tool_results`, `web_search` or `web_fetch` for `server_tool_results`). Under `except` a custom tool added later counts as a source, under `only` it does not. A fetch of a URL that only an excluded source showed fails with `url_not_in_prior_context`. See [URL sources](https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-fetch-tool#url-sources).

These lists are not the only restrictions applied to the two tools:

- In a cloud environment with `limited` networking, the environment's `allowed_hosts` also applies to `web_search` and `web_fetch`. Creating a session fails with a 400 error when an enabled web tool's `allowed_domains` has an entry that is not within `allowed_hosts`. An `allowed_hosts` entry matches one exact host unless it starts with `*.`, so `docs.example.com` is not within `["example.com"]`, while an `allowed_domains` entry also covers its subdomains. The environment is chosen per session, so Terraform cannot check this when it applies the agent.
- The organization's web search and web fetch settings on the **Capabilities** page of the Claude Console apply on top of the agent's lists: a URL or search result must pass both the organization's domain restrictions and the agent's.

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `model` (String) The model that will power the agent (e.g. `claude-sonnet-4-6`).
- `name` (String) Human-readable name for the agent. 1-256 characters.

### Optional

- `agent_toolset` (Attributes) Configuration for built-in agent tools (bash, edit, read, write, glob, grep, web_fetch, web_search). (see [below for nested schema](#nestedatt--agent_toolset))
- `custom_tools` (Attributes List) Custom tools executed by the API client. When the agent calls a custom tool, the session goes idle until the client provides the result. (see [below for nested schema](#nestedatt--custom_tools))
- `description` (String) Description of what the agent does. Up to 2048 characters.
- `execution_identity` (Attributes) Principal the agent's runs act as. When omitted, runs use the Anthropic-managed service account (the API default), and removing the attribute restores that default on the next update. On Claude Platform on AWS, an `aws_role` is required before an `anthropic_deployment` can use the agent: see the Execution identity section for the role's trust policy and permissions. (see [below for nested schema](#nestedatt--execution_identity))
- `mcp_servers` (Attributes List) MCP servers this agent connects to. Maximum 20. Names must be unique. (see [below for nested schema](#nestedatt--mcp_servers))
- `mcp_toolsets` (Attributes List) Tool configurations for MCP servers. Each entry corresponds to a server defined in `mcp_servers`, at most one entry per server. (see [below for nested schema](#nestedatt--mcp_toolsets))
- `metadata` (Map of String) Arbitrary key-value metadata. Maximum 16 pairs, keys up to 64 chars, values up to 512 chars.
- `model_effort` (String) How hard Claude works on each turn: `low`, `medium`, `high`, `xhigh` or `max`. `xhigh` and `max` spend noticeably more tokens, so they are cost-relevant. When omitted on create, the API resolves a per-model default and it is stored here. Effort follows the model: changing `model` without setting `model_effort` resolves the new model's default (known after apply). With an unchanged `model`, removing the attribute from the configuration keeps the last value rather than resetting it to the default.
- `model_inference_geo` (String) Geographic region that serves the agent's model requests (for example `us` or `global`). The value is not validated locally because the data residency documentation does not state that these are the only values. When unset, requests follow the workspace's default inference geo. Removing the attribute clears the pin on the next update.
- `model_speed` (String) Inference speed mode. `fast` provides faster output at premium pricing. Not all models support `fast`.
- `multiagent` (Attributes) Multiagent orchestration (beta), of type `coordinator` or `multiagent_20261001` (see the Multiagent orchestration section). Referenced agents must not have their own `multiagent` (depth limit 1), and `inference_geo` must match across the agent and every agent it references. Agent references are snapshotted on create/update: to pick up a newer version of a referenced agent, change its `version`. The API resolves a `self` entry into an `agent` reference carrying the agent's own ID (and echoes a coordinator `advisor` entry last); state keeps the configured order and `self` entries as configured. When imported (no configuration to correlate with), a `self` entry is therefore read back as an `agent` entry whose `id` is the agent's own ID. (see [below for nested schema](#nestedatt--multiagent))
- `skills` (Attributes List) Skills available to the agent. Maximum 20. (see [below for nested schema](#nestedatt--skills))
- `system` (String) System prompt for the agent. Up to 100,000 characters.

### Read-Only

- `archived_at` (String) Archive timestamp (RFC 3339). Null if the agent has not been archived.
- `created_at` (String) Creation timestamp (RFC 3339).
- `id` (String) Unique agent identifier assigned by the API.
- `updated_at` (String) Last update timestamp (RFC 3339).
- `version` (Number) The agent's current version. Starts at 1 and increments on each modification.

<a id="nestedatt--agent_toolset"></a>
### Nested Schema for `agent_toolset`

Optional:

- `configs` (Attributes List) Per-tool configuration overrides. The `web_fetch` and `web_search` entries also accept `allowed_domains` or `blocked_domains`, plus `max_content_tokens` and `url_sources` (`web_fetch`) or `user_location` (`web_search`); setting one of these on another tool fails at plan time. (see [below for nested schema](#nestedatt--agent_toolset--configs))
- `default_enabled` (Boolean) Whether tools are enabled by default. Defaults to `true`.
- `default_permission_policy` (String) Default permission policy: `always_allow` (runs without confirmation), `always_ask` (waits for approval) or `auto` (the server evaluates each call and runs it, denies it or waits for approval; not a human checkpoint, since a call it judges safe runs before anyone sees it). The API applies `always_allow` when omitted. Removing the attribute does not plan a change on its own: set the policy you want instead.

<a id="nestedatt--agent_toolset--configs"></a>
### Nested Schema for `agent_toolset.configs`

Required:

- `name` (String) Built-in tool name.

Optional:

- `allowed_domains` (Set of String) `web_fetch` and `web_search` only. The only hosts the tool can reach. Each entry is a plain hostname such as `docs.example.com` (no scheme, port or wildcard) and also covers its subdomains; a `web_search` entry may carry a path suffix. 1-64 entries of 1-255 characters. Conflicts with `blocked_domains`. With `limited` environment networking, every entry must also be within the environment's `allowed_hosts`, or creating a session fails.
- `blocked_domains` (Set of String) `web_fetch` and `web_search` only. Hosts the tool can never reach, written like `allowed_domains`. 1-64 entries of 1-255 characters. Conflicts with `allowed_domains`.
- `enabled` (Boolean) Whether this tool is enabled. Overrides `default_enabled`.
- `max_content_tokens` (Number) `web_fetch` only. Maximum number of tokens of fetched text content included in the context per call. Does not apply to binary content such as PDFs. Positive integer.
- `permission_policy` (String) Permission policy override: `always_allow`, `always_ask` or `auto`.
- `url_sources` (Attributes) `web_fetch` only. Which sources may contribute the URLs the tool fetches: the tool only fetches a URL that appeared earlier in the session, in a user message, a custom tool result or a `web_search` or `web_fetch` result, and each key below narrows one of those sources (a URL planted in a tool result is a prompt-injection path). Omitted keys allow their source. At least one key must be set, and setting all three to `none` is rejected. (see [below for nested schema](#nestedatt--agent_toolset--configs--url_sources))
- `user_location` (Attributes) `web_search` only. Approximate user location used to localize search results (sent with `type = "approximate"`, the only precision the API supports). At least one field must be set. (see [below for nested schema](#nestedatt--agent_toolset--configs--user_location))

<a id="nestedatt--agent_toolset--configs--url_sources"></a>
### Nested Schema for `agent_toolset.configs.url_sources`

Optional:

- `client_tool_results` (Attributes) Which custom tools' results contribute URLs that may be fetched. Omit it to allow every one (the API default). (see [below for nested schema](#nestedatt--agent_toolset--configs--url_sources--client_tool_results))
- `server_tool_results` (Attributes) Which `web_search` and `web_fetch` results contribute URLs that may be fetched. Omit it to allow every one (the API default). (see [below for nested schema](#nestedatt--agent_toolset--configs--url_sources--server_tool_results))
- `user_input` (String) Whether URLs in the text of user messages may be fetched: `all` or `none`.

<a id="nestedatt--agent_toolset--configs--url_sources--client_tool_results"></a>
### Nested Schema for `agent_toolset.configs.url_sources.client_tool_results`

Required:

- `type` (String) `all` (every result), `none` (no result), `only` (the results of the tools in `tools`) or `except` (every result but those of the tools in `tools`).

Optional:

- `tools` (Set of String) Tool names, compared case-sensitively. Required when `type` is `only` or `except`, forbidden otherwise. 1-128 names. Each name must be a tool in `custom_tools`; with `except`, a custom tool added later counts as a source, with `only` it does not.


<a id="nestedatt--agent_toolset--configs--url_sources--server_tool_results"></a>
### Nested Schema for `agent_toolset.configs.url_sources.server_tool_results`

Required:

- `type` (String) `all` (every result), `none` (no result), `only` (the results of the tools in `tools`) or `except` (every result but those of the tools in `tools`).

Optional:

- `tools` (Set of String) Tool names, compared case-sensitively. Required when `type` is `only` or `except`, forbidden otherwise. 1-128 names. Each name must be `web_search` or `web_fetch`.



<a id="nestedatt--agent_toolset--configs--user_location"></a>
### Nested Schema for `agent_toolset.configs.user_location`

Optional:

- `city` (String) City name. 1-255 characters.
- `country` (String) Two-letter ISO 3166-1 country code, uppercase (e.g. `US`). The API rejects a country its search provider does not support.
- `region` (String) Region or state name. 1-255 characters.
- `timezone` (String) IANA timezone identifier (e.g. `America/Los_Angeles`). The API rejects a name that is not a valid IANA timezone.




<a id="nestedatt--custom_tools"></a>
### Nested Schema for `custom_tools`

Required:

- `description` (String) Description shown to the agent. 1-1024 characters.
- `name` (String) Unique tool name. 1-128 characters; letters, digits, underscores, and hyphens.

Optional:

- `input_schema` (String) JSON Schema for the tool's input parameters. Use `jsonencode()` to build the value.


<a id="nestedatt--execution_identity"></a>
### Nested Schema for `execution_identity`

Required:

- `type` (String) `service_account` (the Anthropic-managed service account, which carries no identifier) or `aws_role` (an IAM role in your AWS account).

Optional:

- `role_arn` (String) ARN of the IAM role, e.g. `arn:aws:iam::123456789012:role/agent-runner`. Required when `type` is `aws_role`, forbidden otherwise. Up to 2048 characters.


<a id="nestedatt--mcp_servers"></a>
### Nested Schema for `mcp_servers`

Required:

- `name` (String) Unique name for this server, referenced by `mcp_toolsets`. 1-255 characters.
- `url` (String) Endpoint URL for the MCP server.


<a id="nestedatt--mcp_toolsets"></a>
### Nested Schema for `mcp_toolsets`

Required:

- `mcp_server_name` (String) Name of the MCP server (must match a name in `mcp_servers`).

Optional:

- `configs` (Attributes List) Per-MCP-tool configuration overrides. (see [below for nested schema](#nestedatt--mcp_toolsets--configs))
- `default_enabled` (Boolean) Whether MCP tools are enabled by default.
- `default_permission_policy` (String) Default permission policy: `always_allow` (runs without confirmation), `always_ask` (waits for approval) or `auto` (the server evaluates each call and runs it, denies it or waits for approval; not a human checkpoint, since a call it judges safe runs before anyone sees it). The API applies `always_ask` when omitted, so tools added to the server later do not run without approval. Removing the attribute does not plan a change on its own: set `always_ask` to go back to it.

<a id="nestedatt--mcp_toolsets--configs"></a>
### Nested Schema for `mcp_toolsets.configs`

Required:

- `name` (String) Name of the MCP tool to configure.

Optional:

- `enabled` (Boolean) Whether this tool is enabled.
- `permission_policy` (String) Permission policy override: `always_allow`, `always_ask` or `auto`.



<a id="nestedatt--multiagent"></a>
### Nested Schema for `multiagent`

Required:

- `type` (String) `coordinator` (a roster in `agents`) or `multiagent_20261001` (`subagents`, `workflows` and `advisor`, each turned on or off on its own).

Optional:

- `advisor` (Attributes) `multiagent_20261001` only. An advisor model the session's primary thread can consult mid-turn. Omitted, the agent has no advisor (the API default). For `coordinator`, use an `advisor` entry in `agents` instead. (see [below for nested schema](#nestedatt--multiagent--advisor))
- `agents` (Attributes List) `coordinator` only, and required there. Roster of agents the coordinator may spawn. 1-20 entries referencing distinct agents, at most one `self` and one `advisor`. (see [below for nested schema](#nestedatt--multiagent--agents))
- `subagents` (Attributes) `multiagent_20261001` only. Delegation to subagents, each running in its own session thread (one level deep, at most 25 child threads at a time). Omitted, it takes the API default: enabled, with inline agents. (see [below for nested schema](#nestedatt--multiagent--subagents))
- `workflows` (Attributes) `multiagent_20261001` only. Dynamic workflows: programs the agent writes, which the server runs in the background with many agents. Omitted, it takes the API default: enabled, with inline agents. (see [below for nested schema](#nestedatt--multiagent--workflows))

<a id="nestedatt--multiagent--advisor"></a>
### Nested Schema for `multiagent.advisor`

Required:

- `model` (String) Advisor model ID. It must be at least as capable as the agent's model, or the API rejects the pairing.


<a id="nestedatt--multiagent--agents"></a>
### Nested Schema for `multiagent.agents`

Required:

- `type` (String) Entry type: `agent` (another agent), `self` (recursive self-invocation) or `advisor` (a model the coordinator may consult mid-turn).

Optional:

- `id` (String) ID of the referenced agent. Required when `type` is `agent`, forbidden otherwise.
- `model` (String) Advisor model ID. Required when `type` is `advisor`, forbidden otherwise.
- `version` (Number) Pinned version of the referenced agent (`agent` entries only). The API pins the latest version when omitted and echoes it back.


<a id="nestedatt--multiagent--subagents"></a>
### Nested Schema for `multiagent.subagents`

Optional:

- `enabled` (Boolean) Whether the setting is on. Defaults to `true`. When `false`, the other two attributes must be unset.
- `inline_agents_enabled` (Boolean) Whether inline agents, which are defined on the fly and never saved, are allowed besides `predefined_agents`. Defaults to `true`. When `false`, `predefined_agents` must list at least one agent: set it to `false` under both `subagents` and `workflows` to allow only the agents you list.
- `predefined_agents` (Attributes List) Saved agents the setting can use, 1-20 entries referencing distinct agents and at most one `self`. Separate from the other setting's list: an agent in one list is not added to the other. Omitted, the list is empty. (see [below for nested schema](#nestedatt--multiagent--subagents--predefined_agents))

<a id="nestedatt--multiagent--subagents--predefined_agents"></a>
### Nested Schema for `multiagent.subagents.predefined_agents`

Required:

- `type` (String) Entry type: `agent` (another agent) or `self` (copies of this agent).

Optional:

- `id` (String) ID of the referenced agent. Required when `type` is `agent`, forbidden otherwise.
- `version` (Number) Pinned version of the referenced agent (`agent` entries only). The API pins the latest version when omitted and echoes it back.



<a id="nestedatt--multiagent--workflows"></a>
### Nested Schema for `multiagent.workflows`

Optional:

- `enabled` (Boolean) Whether the setting is on. Defaults to `true`. When `false`, the other two attributes must be unset.
- `inline_agents_enabled` (Boolean) Whether inline agents, which are defined on the fly and never saved, are allowed besides `predefined_agents`. Defaults to `true`. When `false`, `predefined_agents` must list at least one agent: set it to `false` under both `subagents` and `workflows` to allow only the agents you list.
- `predefined_agents` (Attributes List) Saved agents the setting can use, 1-20 entries referencing distinct agents and at most one `self`. Separate from the other setting's list: an agent in one list is not added to the other. Omitted, the list is empty. (see [below for nested schema](#nestedatt--multiagent--workflows--predefined_agents))

<a id="nestedatt--multiagent--workflows--predefined_agents"></a>
### Nested Schema for `multiagent.workflows.predefined_agents`

Required:

- `type` (String) Entry type: `agent` (another agent) or `self` (copies of this agent).

Optional:

- `id` (String) ID of the referenced agent. Required when `type` is `agent`, forbidden otherwise.
- `version` (Number) Pinned version of the referenced agent (`agent` entries only). The API pins the latest version when omitted and echoes it back.




<a id="nestedatt--skills"></a>
### Nested Schema for `skills`

Required:

- `skill_id` (String) Identifier of the skill (e.g. `xlsx` for Anthropic skills, or a tagged ID for custom skills).
- `type` (String) Skill type: `anthropic` (managed by Anthropic) or `custom` (user-created).

Optional:

- `version` (String) Version to pin. Defaults to latest if omitted.

## Import

Import an existing agent using its ID:

```shell
terraform import anthropic_agent.example <agent_id>
```
