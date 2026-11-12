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

// platformExperimental describes the experimental warning of each cloud
// platform: the provider block that activates it, the guide that lists what
// remains unverified there, and how far the support was checked against the
// real gateway (the end of the warning's first sentence). "Experimental" is
// the provider's own label, defined in templates/index.md.tmpl ("Stability"):
// the block may change in a minor release. "Beta" is Anthropic's label for
// an API's status and is never used for the provider's own maturity.
var platformExperimental = map[platform.Platform]struct{ block, guide, gateway string }{
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

// addPlatformExperimentalWarning adds, on the platform block, the warning
// that the platform's support is experimental: it rests on the SDK's own beta
// clients for the platform and is covered by tests against local fakes,
// and in part or not at all against the real gateway, depending on the
// platform. Configure calls it once,
// after the block configured the alias, so every alias on a cloud platform
// prints it once per run, and a first-party alias never does.
func addPlatformExperimentalWarning(diags *diag.Diagnostics, p platform.Platform) {
	exp, ok := platformExperimental[p]
	if !ok {
		return
	}
	// Each link ends its line, so that a terminal or a copy does not take
	// trailing punctuation for part of it.
	diags.AddAttributeWarning(path.Root(exp.block), p.DisplayName()+" support is experimental",
		p.DisplayName()+" support in this provider rests on the Anthropic SDK's beta client for the platform and may "+
			"change in a minor release. It is verified with offline tests, against local fakes of the "+
			"gateway, "+exp.gateway+"\n\n"+
			"The guide lists what remains unverified: "+exp.guide+"\n\n"+
			"Please report what works and what does not: "+providerIssuesURL)
}
