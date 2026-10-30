// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
)

// documentedPlatforms are the columns of the availability table in the
// provider documentation, in order.
var documentedPlatforms = []platform.Platform{platform.AWS, platform.GoogleCloud}

const (
	availabilityStart = "<!-- platform-availability:start -->"
	availabilityEnd   = "<!-- platform-availability:end -->"
)

func stateWord(s platform.State) string {
	switch s {
	case platform.StateSupported:
		return "Supported"
	case platform.StateUnverified:
		return "Unverified"
	case platform.StateUnavailable:
		return "Unavailable"
	default:
		return "Undeclared"
	}
}

// availabilityTable renders, from the registry, the table of every type that
// is not supported on every documented platform.
func availabilityTable(t *testing.T) string {
	t.Helper()
	header := make([]string, 0, len(documentedPlatforms))
	for _, p := range documentedPlatforms {
		header = append(header, p.DisplayName())
	}
	return typeTable(t, header, func(typ registeredType) []string {
		var cells []string
		supported := true
		for _, p := range documentedPlatforms {
			s := typ.on(p)
			cells = append(cells, stateWord(s.State))
			supported = supported && s.State == platform.StateSupported
		}
		if supported {
			return nil
		}
		return cells
	})
}

// TestIndexDocumentsPlatformAvailability keeps the availability table of the
// provider documentation equal to the registry. On a mismatch, paste the
// printed table between the markers of templates/index.md.tmpl and run
// `make generate`.
func TestIndexDocumentsPlatformAvailability(t *testing.T) {
	assertIndexTable(t, availabilityStart, availabilityEnd, availabilityTable(t))
}
