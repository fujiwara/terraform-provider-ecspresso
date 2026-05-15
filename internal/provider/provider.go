package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

var _ provider.Provider = (*ecspressoProvider)(nil)

type ecspressoProvider struct {
	version string
}

// New returns a provider factory bound to the given version string.
// The version is reported via the Metadata response and surfaces in
// `terraform providers schema -json` output.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &ecspressoProvider{version: version}
	}
}

func (p *ecspressoProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "ecspresso"
	resp.Version = p.version
}

func (p *ecspressoProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Configure the ecspresso provider. The provider currently has no top-level configuration; all configuration lives on the resource.",
		Attributes:  map[string]schema.Attribute{},
	}
}

func (p *ecspressoProvider) Configure(_ context.Context, _ provider.ConfigureRequest, _ *provider.ConfigureResponse) {
}

func (p *ecspressoProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}

func (p *ecspressoProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewServiceResource,
	}
}
