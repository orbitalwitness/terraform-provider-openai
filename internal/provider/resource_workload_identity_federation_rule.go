package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var _ resource.ResourceWithImportState = &WorkloadIdentityFederationRuleResource{}

func NewWorkloadIdentityFederationRuleResource() resource.Resource {
	return &WorkloadIdentityFederationRuleResource{}
}

type WorkloadIdentityFederationRuleResource struct{ baseResource }

type workloadIdentityFederationRuleModel struct {
	ID                         types.String         `tfsdk:"id"`
	WorkloadIdentityProviderID types.String         `tfsdk:"workload_identity_provider_id"`
	ProjectID                  types.String         `tfsdk:"project_id"`
	ServiceAccountID           types.String         `tfsdk:"service_account_id"`
	Name                       types.String         `tfsdk:"name"`
	Description                types.String         `tfsdk:"description"`
	Enabled                    types.Bool           `tfsdk:"enabled"`
	AttributeMatchers          jsontypes.Normalized `tfsdk:"attribute_matchers"`
	Permissions                types.Object         `tfsdk:"permissions"`
}

type workloadIdentityFederationRuleResponse struct {
	ID                         string                      `json:"id"`
	WorkloadIdentityProviderID string                      `json:"workload_identity_provider_id"`
	ProjectID                  string                      `json:"project_id"`
	ServiceAccountID           string                      `json:"service_account_id"`
	Name                       *string                     `json:"name"`
	Description                *string                     `json:"description"`
	Enabled                    bool                        `json:"enabled"`
	AttributeMatchers          json.RawMessage             `json:"attribute_matchers"`
	Permissions                workloadIdentityPermissions `json:"permissions"`
}

func (r *WorkloadIdentityFederationRuleResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workload_identity_federation_rule"
}

func (r *WorkloadIdentityFederationRuleResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	immutableID := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Required: true, MarkdownDescription: description, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Bind verified workload token attributes to an existing project service account. Uses the configured OpenAI admin key.",
		Attributes: map[string]schema.Attribute{
			"id":                            workloadIdentityIDAttribute(),
			"workload_identity_provider_id": immutableID("ID of the workload identity provider."),
			"project_id":                    immutableID("ID of the project containing the service account."),
			"service_account_id":            immutableID("ID of the service account to authenticate as."),
			"name":                          schema.StringAttribute{Optional: true, MarkdownDescription: "Name of the rule."},
			"description":                   schema.StringAttribute{Optional: true, MarkdownDescription: "Description of the rule."},
			"enabled":                       schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), MarkdownDescription: "Whether the rule is enabled. Defaults to true."},
			"attribute_matchers": schema.StringAttribute{
				Required: true, CustomType: jsontypes.NormalizedType{},
				Validators:          []validator.String{workloadIdentityMatchersValidator{}},
				MarkdownDescription: "JSON object matching verified attributes to string, boolean or number values. Use jsonencode to preserve value types.",
			},
			"permissions": schema.SingleNestedAttribute{
				Optional: true, Computed: true,
				PlanModifiers:       []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "Token permissions. Defaults to the service account's permissions on creation. Omitting this object later preserves the existing configuration; set type = service_account_default explicitly to restore default permissions. Use restricted with non-empty scopes to narrow access.",
				Attributes: map[string]schema.Attribute{
					"type":   schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("service_account_default", "restricted")}, MarkdownDescription: "Permission mode: service_account_default or restricted."},
					"scopes": schema.SetAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Non-empty set of scopes. Required only for restricted permissions."},
				},
			},
		},
	}
}

func (m workloadIdentityFederationRuleModel) collectionPath() string {
	return workloadIdentityPath + "/" + url.PathEscape(m.WorkloadIdentityProviderID.ValueString()) + "/federation_rules"
}

func (m workloadIdentityFederationRuleModel) resourcePath() string {
	return m.collectionPath() + "/" + url.PathEscape(m.ID.ValueString())
}

func (m workloadIdentityFederationRuleModel) body(ctx context.Context, create bool) (map[string]any, diag.Diagnostics) {
	body := map[string]any{"attribute_matchers": json.RawMessage(m.AttributeMatchers.ValueString()), "enabled": m.Enabled.ValueBool()}
	if create {
		body["project_id"], body["service_account_id"] = m.ProjectID.ValueString(), m.ServiceAccountID.ValueString()
	}
	if !m.Name.IsNull() || !create {
		body["name"] = m.Name.ValueStringPointer()
	}
	if !m.Description.IsNull() || !create {
		body["description"] = m.Description.ValueStringPointer()
	}
	var diags diag.Diagnostics
	if !m.Permissions.IsNull() && !m.Permissions.IsUnknown() {
		var permissions workloadIdentityPermissionsModel
		diags.Append(m.Permissions.As(ctx, &permissions, basetypes.ObjectAsOptions{})...)
		value := workloadIdentityPermissions{Type: permissions.Type.ValueString()}
		if !permissions.Scopes.IsNull() {
			diags.Append(permissions.Scopes.ElementsAs(ctx, &value.Scopes, false)...)
		}
		if value.Type == "restricted" && len(value.Scopes) == 0 || value.Type == "service_account_default" && !permissions.Scopes.IsNull() {
			diags.AddAttributeError(path.Root("permissions"), "Invalid permissions", "restricted permissions require non-empty scopes; service_account_default must not set scopes.")
		}
		body["permissions"] = value
	}
	return body, diags
}

func (m *workloadIdentityFederationRuleModel) fill(ctx context.Context, value workloadIdentityFederationRuleResponse) diag.Diagnostics {
	m.ID, m.WorkloadIdentityProviderID = types.StringValue(value.ID), types.StringValue(value.WorkloadIdentityProviderID)
	m.ProjectID, m.ServiceAccountID = types.StringValue(value.ProjectID), types.StringValue(value.ServiceAccountID)
	m.Name, m.Description = types.StringPointerValue(value.Name), types.StringPointerValue(value.Description)
	m.Enabled = types.BoolValue(value.Enabled)
	m.AttributeMatchers = jsontypes.NewNormalizedValue(string(value.AttributeMatchers))
	permissions, diags := workloadIdentityPermissionsValue(ctx, value.Permissions)
	m.Permissions = permissions
	return diags
}

func (r *WorkloadIdentityFederationRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data workloadIdentityFederationRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := data.body(ctx, true)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	var value workloadIdentityFederationRuleResponse
	if err := r.workloadIdentityRequest(ctx, http.MethodPost, data.collectionPath(), body, &value); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create workload identity federation rule: %s", err))
		return
	}
	resp.Diagnostics.Append(data.fill(ctx, value)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *WorkloadIdentityFederationRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data workloadIdentityFederationRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var value workloadIdentityFederationRuleResponse
	if err := r.workloadIdentityRequest(ctx, http.MethodGet, data.resourcePath(), nil, &value); err != nil {
		if workloadIdentityNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read workload identity federation rule: %s", err))
		return
	}
	resp.Diagnostics.Append(data.fill(ctx, value)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *WorkloadIdentityFederationRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data workloadIdentityFederationRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := data.body(ctx, false)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	var value workloadIdentityFederationRuleResponse
	if err := r.workloadIdentityRequest(ctx, http.MethodPost, data.resourcePath(), body, &value); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update workload identity federation rule: %s", err))
		return
	}
	resp.Diagnostics.Append(data.fill(ctx, value)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *WorkloadIdentityFederationRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data workloadIdentityFederationRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.workloadIdentityRequest(ctx, http.MethodDelete, data.resourcePath(), nil, nil); err != nil && !workloadIdentityNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete workload identity federation rule: %s", err))
	}
}

func (r *WorkloadIdentityFederationRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	providerID, ruleID, ok := strings.Cut(req.ID, "/")
	if !ok || providerID == "" || ruleID == "" || strings.Contains(ruleID, "/") {
		resp.Diagnostics.AddError("Invalid import ID", "Expected workload_identity_provider_id/federation_rule_id.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workload_identity_provider_id"), providerID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), ruleID)...)
}
