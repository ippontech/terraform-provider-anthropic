// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
)

// providerIssuesURL is where users report what works and what does not.
const providerIssuesURL = "https://github.com/ippontech/terraform-provider-anthropic/issues"

// platformBeta describes the beta warning of each cloud platform: the
// provider block that activates it, the guide that lists what remains
// unverified there, and how far the support was checked against the real
// gateway (the end of the warning's first sentence).
var platformBeta = map[platform.Platform]struct{ block, guide, gateway string }{
	platform.AWS: {
		block:   "aws",
		guide:   "https://registry.terraform.io/providers/ippontech/anthropic/latest/docs/guides/claude_platform_on_aws",
		gateway: "and in part against the real gateway, so some requests, errors or routes may still behave differently there.",
	},
	platform.GoogleCloud: {
		block:   "google_cloud",
		guide:   "https://registry.terraform.io/providers/ippontech/anthropic/latest/docs/guides/claude_models_on_google_cloud",
		gateway: "and has had little or no testing against the real gateway, so requests, errors or routes may behave differently there.",
	},
}

// addPlatformBetaWarning adds, on the platform block, the warning that the
// platform's support is in beta: it is covered by tests against local fakes,
// and in part or not at all against the real gateway, depending on the
// platform. Configure calls it once,
// after the block configured the alias, so every alias on a cloud platform
// prints it once per run, and a first-party alias never does.
func addPlatformBetaWarning(diags *diag.Diagnostics, p platform.Platform) {
	beta, ok := platformBeta[p]
	if !ok {
		return
	}
	// Each link ends its line, so that a terminal or a copy does not take
	// trailing punctuation for part of it.
	diags.AddAttributeWarning(path.Root(beta.block), p.DisplayName()+" support is in beta",
		p.DisplayName()+" support in this provider is verified with offline tests, against local fakes of the "+
			"gateway, "+beta.gateway+"\n\n"+
			"The guide lists what remains unverified: "+beta.guide+"\n\n"+
			"Please report what works and what does not: "+providerIssuesURL)
}
