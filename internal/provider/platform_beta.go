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
// provider block that activates it, and the guide that lists what remains
// unverified there.
var platformBeta = map[platform.Platform]struct{ block, guide string }{
	platform.AWS: {
		block: "aws",
		guide: "https://registry.terraform.io/providers/ippontech/anthropic/latest/docs/guides/claude_platform_on_aws",
	},
	platform.GoogleCloud: {
		block: "google_cloud",
		guide: "https://registry.terraform.io/providers/ippontech/anthropic/latest/docs/guides/claude_models_on_google_cloud",
	},
}

// addPlatformBetaWarning adds, on the platform block, the warning that the
// platform's support is in beta: it is covered by tests against local fakes
// only, never against the real gateway. Configure calls it once, after the
// block configured the alias, so every alias on a cloud platform prints it once
// per run, and a first-party alias never does.
func addPlatformBetaWarning(diags *diag.Diagnostics, p platform.Platform) {
	beta, ok := platformBeta[p]
	if !ok {
		return
	}
	// Each link ends its line, so that a terminal or a copy does not take
	// trailing punctuation for part of it.
	diags.AddAttributeWarning(path.Root(beta.block), p.DisplayName()+" support is in beta",
		p.DisplayName()+" support in this provider is verified with offline tests only, against local fakes of "+
			"the gateway: nothing has been tested against the real gateway yet, so requests, errors or routes may "+
			"behave differently there.\n\n"+
			"The guide lists what remains unverified: "+beta.guide+"\n\n"+
			"Please report what works and what does not: "+providerIssuesURL)
}
