# Environments service notes

Load-bearing details for `internal/services/environments/`: the `anthropic_environment` resource and the `anthropic_environment` / `anthropic_environments` data sources (`client.Beta.Environments`, standard API key). The root `CLAUDE.md` still applies; where a note here says "see above" or "see below" and the target is not in this file, it is a section of the root `CLAUDE.md`. Record new details about this service here, not in the root file.

**Resources:**
- `anthropic_environment` (`internal/services/environments/environment_resource.go`) — manages environments; supports `archive_on_destroy` (archives instead of deleting on destroy when true). `description` and `metadata` are `Optional`, not `Computed` (they were `Optional` + `Computed` + `UseStateForUnknown` until the metadata-patch fix, so removing them did nothing): the update endpoint keeps an omitted `description` and clears a `null` one (https://platform.claude.com/docs/en/api/beta/environments/update), so a null description is always sent as `param.Null[string]()`, not only when the prior state has one (a saved plan or `-refresh=false` applies over a state that may miss a description set outside Terraform, which the response would then carry back where the plan has null), and `metadata` goes through `tfvalue.MetadataPatch`. The API answers `""` for an empty description; the mapper turns it into null except where the plan (create, update) or the prior state (read) holds `""`, so `description = ""` is not an inconsistent result after apply (`TestEnvironmentResource_emptyDescription`). Fake-API tests: `environment_resource_*_fake_test.go`, with the routes and handlers in `fake_test.go`.

**Data sources:**
- `anthropic_environment` (`internal/services/environments/environment_data_source.go`) — fetches a single environment
- `anthropic_environments` (`internal/services/environments/environments_data_source.go`) — lists all environments
