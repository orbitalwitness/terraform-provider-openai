package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var _ resource.ResourceWithImportState = &WorkloadIdentityProviderResource{}

func NewWorkloadIdentityProviderResource() resource.Resource {
	return &WorkloadIdentityProviderResource{}
}

type WorkloadIdentityProviderResource struct{ baseResource }

type workloadIdentityProviderModel struct {
	ID                       types.String `tfsdk:"id"`
	Name                     types.String `tfsdk:"name"`
	Issuer                   types.String `tfsdk:"issuer"`
	Audience                 types.String `tfsdk:"audience"`
	Description              types.String `tfsdk:"description"`
	Enabled                  types.Bool   `tfsdk:"enabled"`
	AttributeTransformations types.Map    `tfsdk:"attribute_transformations"`
	Verification             types.Object `tfsdk:"verification"`
}

type workloadIdentityTransformation struct {
	Attribute  string `json:"attribute"`
	Expression string `json:"expression"`
}

type workloadIdentityProviderResponse struct {
	ID                       string                           `json:"id"`
	Name                     string                           `json:"name"`
	Issuer                   string                           `json:"issuer"`
	Audience                 string                           `json:"audience"`
	Description              *string                          `json:"description"`
	Enabled                  bool                             `json:"enabled"`
	AttributeTransformations []workloadIdentityTransformation `json:"attribute_transformations"`
	Verification             workloadIdentityVerification     `json:"verification"`
}

func (r *WorkloadIdentityProviderResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workload_identity_provider"
}

func (r *WorkloadIdentityProviderResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Register an OIDC provider for keyless project service-account authentication. Uses the configured OpenAI admin key.",
		Attributes: map[string]schema.Attribute{
			"id":          workloadIdentityIDAttribute(),
			"name":        schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}, MarkdownDescription: "Name of the provider."},
			"issuer":      schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}, MarkdownDescription: "OIDC issuer URL."},
			"audience":    schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}, MarkdownDescription: "Expected audience of workload identity tokens."},
			"description": schema.StringAttribute{Optional: true, MarkdownDescription: "Description of the provider."},
			"enabled":     schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), MarkdownDescription: "Whether the provider is enabled. Defaults to true."},
			"attribute_transformations": schema.MapAttribute{
				Optional: true, Computed: true, ElementType: types.StringType,
				Default:             mapdefault.StaticValue(types.MapValueMust(types.StringType, map[string]attr.Value{})),
				Validators:          []validator.Map{mapvalidator.SizeAtMost(20), mapvalidator.KeysAre(stringvalidator.LengthBetween(1, 256)), mapvalidator.ValueStringsAre(stringvalidator.LengthBetween(1, 4096))},
				MarkdownDescription: "Map of attribute names to transformation expressions. Defaults to an empty map.",
			},
			"verification": schema.SingleNestedAttribute{
				Optional: true, Computed: true,
				PlanModifiers:       []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "OIDC verification configuration. Defaults to discovery using the issuer on creation. Omitting this object later preserves the existing configuration; set type = discovery explicitly to restore discovery. Omit discovery_url to use the issuer's discovery endpoint.",
				Attributes: map[string]schema.Attribute{
					"type":          schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("discovery", "uploaded_jwks")}, MarkdownDescription: "Verification method: discovery or uploaded_jwks."},
					"discovery_url": schema.StringAttribute{Optional: true, MarkdownDescription: "Custom discovery URL. Only valid for discovery verification."},
					"jwks":          schema.StringAttribute{Optional: true, CustomType: jsontypes.NormalizedType{}, MarkdownDescription: "JSON Web Key Set as a JSON object. Required for uploaded_jwks verification."},
				},
			},
		},
	}
}

func (m workloadIdentityProviderModel) body(ctx context.Context, create bool) (map[string]any, diag.Diagnostics) {
	body := map[string]any{"name": m.Name.ValueString(), "issuer": m.Issuer.ValueString(), "audience": m.Audience.ValueString(), "enabled": m.Enabled.ValueBool()}
	if create {
		body["type"] = "oidc"
	}
	if !m.Description.IsNull() || !create {
		body["description"] = m.Description.ValueStringPointer()
	}
	transformations := map[string]string{}
	diags := m.AttributeTransformations.ElementsAs(ctx, &transformations, false)
	keys := make([]string, 0, len(transformations))
	for key := range transformations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]workloadIdentityTransformation, 0, len(keys))
	for _, key := range keys {

		values = append(values, workloadIdentityTransformation{Attribute: key, Expression: transformations[key]})
	}
	body["attribute_transformations"] = values
	if !m.Verification.IsNull() && !m.Verification.IsUnknown() {
		var verification workloadIdentityVerificationModel
		diags.Append(m.Verification.As(ctx, &verification, basetypes.ObjectAsOptions{})...)
		value := workloadIdentityVerification{Type: verification.Type.ValueString(), DiscoveryURL: verification.DiscoveryURL.ValueStringPointer()}
		switch value.Type {
		case "discovery":
			if !verification.JWKS.IsNull() {
				diags.AddAttributeError(path.Root("verification"), "Invalid verification", "jwks is only valid for uploaded_jwks verification.")
			}
		case "uploaded_jwks":
			var jwks map[string]any
			if err := json.Unmarshal([]byte(verification.JWKS.ValueString()), &jwks); err != nil || jwks == nil || !verification.DiscoveryURL.IsNull() {
				diags.AddAttributeError(path.Root("verification"), "Invalid verification", "uploaded_jwks requires a jwks JSON object and must not set discovery_url.")
			}
			value.JWKS = json.RawMessage(verification.JWKS.ValueString())
		}
		body["verification"] = value
	}
	return body, diags
}

func (m *workloadIdentityProviderModel) fill(ctx context.Context, value workloadIdentityProviderResponse) diag.Diagnostics {
	m.ID, m.Name = types.StringValue(value.ID), types.StringValue(value.Name)
	m.Issuer, m.Audience = types.StringValue(value.Issuer), types.StringValue(value.Audience)
	m.Description, m.Enabled = types.StringPointerValue(value.Description), types.BoolValue(value.Enabled)
	transformations := map[string]string{}
	for _, transformation := range value.AttributeTransformations {
		transformations[transformation.Attribute] = transformation.Expression
	}
	var diags diag.Diagnostics
	m.AttributeTransformations, diags = types.MapValueFrom(ctx, types.StringType, transformations)
	verification, verificationDiags := workloadIdentityVerificationValue(ctx, value.Verification)
	m.Verification = verification
	diags.Append(verificationDiags...)
	return diags
}

func (r *WorkloadIdentityProviderResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data workloadIdentityProviderModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := data.body(ctx, true)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	var value workloadIdentityProviderResponse
	if err := r.workloadIdentityRequest(ctx, http.MethodPost, workloadIdentityPath, body, &value); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create workload identity provider: %s", err))
		return
	}
	resp.Diagnostics.Append(data.fill(ctx, value)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *WorkloadIdentityProviderResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data workloadIdentityProviderModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var value workloadIdentityProviderResponse
	if err := r.workloadIdentityRequest(ctx, http.MethodGet, workloadIdentityPath+"/"+url.PathEscape(data.ID.ValueString()), nil, &value); err != nil {
		if workloadIdentityNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read workload identity provider: %s", err))
		return
	}
	resp.Diagnostics.Append(data.fill(ctx, value)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *WorkloadIdentityProviderResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data workloadIdentityProviderModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := data.body(ctx, false)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	var value workloadIdentityProviderResponse
	if err := r.workloadIdentityRequest(ctx, http.MethodPost, workloadIdentityPath+"/"+url.PathEscape(data.ID.ValueString()), body, &value); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update workload identity provider: %s", err))
		return
	}
	resp.Diagnostics.Append(data.fill(ctx, value)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *WorkloadIdentityProviderResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data workloadIdentityProviderModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.workloadIdentityRequest(ctx, http.MethodDelete, workloadIdentityPath+"/"+url.PathEscape(data.ID.ValueString()), nil, nil); err != nil && !workloadIdentityNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete workload identity provider: %s", err))
	}
}

func (r *WorkloadIdentityProviderResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
