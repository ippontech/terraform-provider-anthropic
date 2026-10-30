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

// refusal makes the diagnostic of a refused type from its type name.
type refusal func(typeName string) (summary, detail string)

func unavailableOn(p platform.Platform, s platform.Support) refusal {
	return func(typeName string) (string, string) {
		return platform.UnavailableSummary(typeName, p), platform.UnavailableDetail(typeName, p, s)
	}
}

func workspaceRequiredOn(p platform.Platform) refusal {
	return func(typeName string) (string, string) {
		return platform.WorkspaceRequiredSummary(typeName, p), platform.WorkspaceRequiredDetail(typeName, p)
	}
}

// refuse returns why a type with this support cannot be configured now, or
// nil when it can.
func (p *AnthropicProvider) refuse(support platform.Support) refusal {
	pl := p.configuredPlatform()
	switch {
	case !support.Allows():
		return unavailableOn(pl, support)
	case p.workspaceless.Load() && !support.OrganizationRoutes:
		return workspaceRequiredOn(pl)
	}
	return nil
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
//
// The stand-in also refuses, on a Google Cloud alias without a workspace, the
// types that need one (all but those marked OrganizationRoutes): the SDK
// would otherwise fail each of their requests, after its retries, with a
// message naming a Go field.

func (p *AnthropicProvider) gateResource(reg registration[resource.Resource]) func() resource.Resource {
	return func() resource.Resource {
		r := reg.New()
		if refuse := p.refuse(reg.on(p.configuredPlatform())); refuse != nil {
			return &refusedResource{Resource: r, refuse: refuse}
		}
		return r
	}
}

func (p *AnthropicProvider) gateDataSource(reg registration[datasource.DataSource]) func() datasource.DataSource {
	return func() datasource.DataSource {
		d := reg.New()
		if refuse := p.refuse(reg.on(p.configuredPlatform())); refuse != nil {
			return &refusedDataSource{DataSource: d, refuse: refuse}
		}
		return d
	}
}

// refusedResource stands in for a resource the configured provider alias
// cannot serve: a type the platform does not serve, or one that needs a
// workspace the alias does not set. Its Configure fails, so no CRUD method
// runs.
type refusedResource struct {
	resource.Resource
	refuse refusal
}

var _ resource.ResourceWithConfigure = &refusedResource{}

func (u *refusedResource) Configure(ctx context.Context, _ resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	var meta resource.MetadataResponse
	u.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: providerTypeName}, &meta)
	resp.Diagnostics.AddError(u.refuse(meta.TypeName))
}

// refusedDataSource is refusedResource for data sources.
type refusedDataSource struct {
	datasource.DataSource
	refuse refusal
}

var _ datasource.DataSourceWithConfigure = &refusedDataSource{}

func (u *refusedDataSource) Configure(ctx context.Context, _ datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	var meta datasource.MetadataResponse
	u.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: providerTypeName}, &meta)
	resp.Diagnostics.AddError(u.refuse(meta.TypeName))
}
