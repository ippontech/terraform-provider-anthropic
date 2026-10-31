// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills

import (
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
)

// Skills beta shape, kept until the GA migration (#192,
// https://github.com/ippontech/terraform-provider-anthropic/issues/192).
//
// The provider manages skills through client.Beta.Skills under the
// skills-2025-10-02 beta header, and under that header the API keeps answering
// in the beta shape: display_title, latest_version and a string source on a
// skill, directory and a Unix-timestamp version on a skill version, and string
// timestamps. SDK v1.68.0 retyped the beta services to the GA shape instead
// (display_name, latest_version_id, a source object, time.Time timestamps, no
// directory or version) and stopped sending the header by default.
//
// To keep the wire behaviour and every Terraform value identical to SDK
// v1.67.0, the provider:
//   - sends the header itself, through skillsBetas() on every Beta.Skills call;
//   - sends display_title as a multipart extra field on create (the typed
//     BetaSkillNewParams only knows display_name);
//   - decodes the attributes it exposes from the response's RawJSON() into the
//     provider-local betaSkill and betaSkillVersion below, never from the typed
//     SDK fields, which no longer match the payload.
//
// All of this goes away with the GA migration in #192, which drops the header
// and maps the GA fields (a breaking change, so it lands in major/2.0.0).

// skillsBetas returns the beta header value every Beta.Skills call sends. A
// fresh slice each time, so no caller can alter the value for another.
func skillsBetas() []anthropic.AnthropicBeta {
	return []anthropic.AnthropicBeta{anthropic.AnthropicBetaSkills2025_10_02}
}

// skillDisplayTitleField is the beta multipart field for the skill title.
const skillDisplayTitleField = "display_title"

// betaSkill is a skill object in the beta shape.
type betaSkill struct {
	ID            string
	CreatedAt     string
	UpdatedAt     string
	DisplayTitle  string
	LatestVersion string
	Source        string
	Type          string
}

// betaSkillVersion is a skill version object in the beta shape.
type betaSkillVersion struct {
	ID          string
	SkillID     string
	Version     string
	Name        string
	Description string
	Directory   string
	CreatedAt   string
	Type        string
}

// decodeBetaSkill decodes the raw JSON of a skill response or list item.
func decodeBetaSkill(raw string) betaSkill {
	f := rawFields(raw)
	return betaSkill{
		ID:            rawString(f, "id"),
		CreatedAt:     rawString(f, "created_at"),
		UpdatedAt:     rawString(f, "updated_at"),
		DisplayTitle:  rawString(f, "display_title"),
		LatestVersion: rawString(f, "latest_version"),
		Source:        rawString(f, "source"),
		Type:          rawString(f, "type"),
	}
}

// decodeBetaSkillVersion decodes the raw JSON of a skill version response or
// list item.
func decodeBetaSkillVersion(raw string) betaSkillVersion {
	f := rawFields(raw)
	return betaSkillVersion{
		ID:          rawString(f, "id"),
		SkillID:     rawString(f, "skill_id"),
		Version:     rawString(f, "version"),
		Name:        rawString(f, "name"),
		Description: rawString(f, "description"),
		Directory:   rawString(f, "directory"),
		CreatedAt:   rawString(f, "created_at"),
		Type:        rawString(f, "type"),
	}
}

// rawFields splits a JSON object into its fields. Anything else yields no
// field, so every attribute reads as "", as the SDK v1.67.0 decoder left them
// (TestSkillDataSourceRead_notAnObject).
func rawFields(raw string) map[string]json.RawMessage {
	var f map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &f) != nil {
		return nil
	}
	return f
}

// rawString reads a field the way the SDK v1.67.0 decoder filled a string
// field: a JSON string reads as its value, an absent or null field as "", and
// any other value as its JSON text.
func rawString(f map[string]json.RawMessage, key string) string {
	raw, ok := f[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}
