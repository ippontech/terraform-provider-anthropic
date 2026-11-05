// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
)

// providerTypeName is the provider's type name, the prefix of every resource
// and data source type name.
const providerTypeName = "anthropic"

// configuredPlatform is the platform the last successful Configure resolved,
// first party until then.
func (p *AnthropicProvider) configuredPlatform() platform.Platform {
	if pl, ok := p.platform.Load().(platform.Platform); ok {
		return pl
	}
	return platform.FirstParty
}

// The framework builds a resource or data source from its factory on every
// RPC and calls its Configure first, after the provider's own Configure has
// run (MoveResourceState is the one RPC without a Configure call, and no type
// here implements it). So the factory is where a type the configured platform
// does not serve can be swapped for a stand-in whose Configure fails, without
// a line in any resource. Before the provider is configured (schema and
// validation RPCs) the platform is first party and every factory returns the
// real type; the stand-in only ever exists after Configure, and it carries the
// real type's Metadata and Schema, so even then nothing but Configure changes.
//
// Unverified and undeclared types get the real type: they are allowed, and a
// gateway 404 that is not the API's own is turned into an error by
// platform.NotFoundGuard rather than read as a deletion.

func (p *AnthropicProvider) gateResource(reg registration[resource.Resource]) func() resource.Resource {
	return func() resource.Resource {
		r := reg.New()
		pl := p.configuredPlatform()
		if support := reg.on(pl); !support.Allows() {
			return &unavailableResource{Resource: r, platform: pl, support: support}
		}
		return r
	}
}

func (p *AnthropicProvider) gateDataSource(reg registration[datasource.DataSource]) func() datasource.DataSource {
	return func() datasource.DataSource {
		d := reg.New()
		pl := p.configuredPlatform()
		if support := reg.on(pl); !support.Allows() {
			return &unavailableDataSource{DataSource: d, platform: pl, support: support}
		}
		return d
	}
}

// unavailableResource stands in for a resource the configured platform does
// not serve. Its Configure fails, so no CRUD method runs.
type unavailableResource struct {
	resource.Resource
	platform platform.Platform
	support  platform.Support
}

var _ resource.ResourceWithConfigure = &unavailableResource{}

func (u *unavailableResource) Configure(ctx context.Context, _ resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	var meta resource.MetadataResponse
	u.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: providerTypeName}, &meta)
	resp.Diagnostics.AddError(
		platform.UnavailableSummary(meta.TypeName, u.platform),
		platform.UnavailableDetail(meta.TypeName, u.platform, u.support),
	)
}

// unavailableDataSource is unavailableResource for data sources.
type unavailableDataSource struct {
	datasource.DataSource
	platform platform.Platform
	support  platform.Support
}

var _ datasource.DataSourceWithConfigure = &unavailableDataSource{}

func (u *unavailableDataSource) Configure(ctx context.Context, _ datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	var meta datasource.MetadataResponse
	u.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: providerTypeName}, &meta)
	resp.Diagnostics.AddError(
		platform.UnavailableSummary(meta.TypeName, u.platform),
		platform.UnavailableDetail(meta.TypeName, u.platform, u.support),
	)
}
