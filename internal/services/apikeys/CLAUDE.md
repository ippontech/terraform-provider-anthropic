# API keys service notes

Load-bearing details for `internal/services/apikeys/`: the import-only `anthropic_api_key` resource and the `anthropic_api_key` / `anthropic_api_keys` data sources (Admin API, `internal/admin` client). The root `CLAUDE.md` still applies; where a note here says "see above" or "see below" and the target is not in this file, it is a section of the root `CLAUDE.md`. Record new details about this service here, not in the root file.

**Resources:**
- `anthropic_api_key` (`internal/services/apikeys/api_key_resource.go`) — import-only resource; manages lifecycle of existing API keys (rename, deactivate) via Admin API; Create always errors with a message to use `terraform import`; Delete sets `status: inactive`

**Data sources:**
- `anthropic_api_key` (`internal/services/apikeys/api_key_data_source.go`) — fetches a single API key by ID (admin API); reuses `APIKeyResourceModel` and `mapAPIKeyToState` from the resource file
- `anthropic_api_keys` (`internal/services/apikeys/api_keys_data_source.go`) — lists API keys (admin API) with optional `status` and `workspace_id` filters; transparent pagination
