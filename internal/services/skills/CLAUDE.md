# Skills service notes

Load-bearing details for `internal/services/skills/`: the `anthropic_skill` and `anthropic_skill_version` resources and the `anthropic_skill(s)` / `anthropic_skill_version(s)` data sources (`client.Beta.Skills`, standard API key). The root `CLAUDE.md` still applies; where a note here says "see above" or "see below" and the target is not in this file, it is a section of the root `CLAUDE.md`. Record new details about this service here, not in the root file. The SDK pin described in the root `CLAUDE.md` exists because of the skill field names.

**Resources:**
- `anthropic_skill_version` (`internal/services/skills/skill_version_resource.go`) — manages skill versions, addressed by their beta `version` (a Unix-timestamp string, not the `id`)
- `anthropic_skill` (`internal/services/skills/skill_resource.go`) — manages skills; like every skills resource and data source it calls `client.Beta.Skills` with the `skills-2025-10-02` header set by the provider and maps the beta shape decoded from `RawJSON()` (`skill_beta_shape.go`, see the SDK paragraph at the top)

**Data sources:**
- `anthropic_skill` (`internal/services/skills/skill_data_source.go`) — fetches a single skill
- `anthropic_skills` (`internal/services/skills/skills_data_source.go`) — lists all skills
- `anthropic_skill_version` (`internal/services/skills/skill_version_data_source.go`) — fetches a single skill version
- `anthropic_skill_versions` (`internal/services/skills/skill_versions_data_source.go`) — lists all skill versions
