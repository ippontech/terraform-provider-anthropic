# Environments service notes

Load-bearing details for `internal/services/environments/`: the `anthropic_environment` resource and the `anthropic_environment` / `anthropic_environments` data sources (`client.Beta.Environments`, standard API key). The root `CLAUDE.md` still applies; where a note here says "see above" or "see below" and the target is not in this file, it is a section of the root `CLAUDE.md`. Record new details about this service here, not in the root file.

**Resources:**
- `anthropic_environment` (`internal/services/environments/environment_resource.go`) — manages environments; supports `archive_on_destroy` (archives instead of deleting on destroy when true)

**Data sources:**
- `anthropic_environment` (`internal/services/environments/environment_data_source.go`) — fetches a single environment
- `anthropic_environments` (`internal/services/environments/environments_data_source.go`) — lists all environments
