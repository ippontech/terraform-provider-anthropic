// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package platform names the platforms a provider alias can reach, what each
// of them serves, and how a gateway platform's answers are read.
//
// The Claude API is served first party at api.anthropic.com and, by Anthropic
// itself, through two cloud gateways: Claude Platform on AWS and Claude Models
// on Google Cloud (Anthropic managed). Each gateway fronts a separate Anthropic
// organization and serves only part of the API, so a resource that works first
// party can be unavailable on a gateway, and a 404 from a gateway does not
// necessarily come from the API.
package platform

// Platform identifies what a provider alias talks to.
type Platform string

const (
	// FirstParty is the Claude API at api.anthropic.com, where every resource
	// and data source is available. The zero value of Platform means the same.
	FirstParty Platform = "first_party"
	// AWS is Claude Platform on AWS: the Claude API served by Anthropic
	// through the aws-external-anthropic gateway, authenticated with SigV4 or
	// a key generated in the AWS Console.
	AWS Platform = "aws"
	// GoogleCloud is Claude Models on Google Cloud (Anthropic managed): the
	// Claude API served by Anthropic through claude.googleapis.com,
	// authenticated with a Google OAuth access token.
	GoogleCloud Platform = "google_cloud"
)

// IsGateway reports whether p reaches the API through a cloud gateway rather
// than at api.anthropic.com.
func (p Platform) IsGateway() bool {
	return p == AWS || p == GoogleCloud
}

// DisplayName is the platform's product name, as diagnostics print it.
func (p Platform) DisplayName() string {
	switch p {
	case AWS:
		return "Claude Platform on AWS"
	case GoogleCloud:
		return "Claude Models on Google Cloud (Anthropic managed)"
	default:
		return "the Claude API"
	}
}

// DocURL is the documentation page that lists what p serves.
func (p Platform) DocURL() string {
	switch p {
	case AWS:
		return "https://platform.claude.com/docs/en/build-with-claude/claude-platform-on-aws"
	case GoogleCloud:
		return "https://platform.claude.com/docs/en/build-with-claude/claude-models-on-google-cloud/anthropic-managed"
	default:
		return "https://platform.claude.com/docs/en/api/overview"
	}
}
