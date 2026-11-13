package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/openai/openai-go/v3"
	supertypes "github.com/orange-cloudavenue/terraform-plugin-framework-supertypes"
)

func (m *ProjectServiceAccountResourceModel) Fill(ctx context.Context, data any) diag.Diagnostics {
	switch data := data.(type) {
	case openai.AdminOrganizationProjectServiceAccountNewResponse:
		m.Id = supertypes.NewStringValue(data.ID)
		m.Name = supertypes.NewStringValue(data.Name)
		m.Role = supertypes.NewStringValue(string(data.Role))
		m.CreatedAt = supertypes.NewInt64Value(data.CreatedAt)
		if data.APIKey.ID != "" {
			m.ApiKeyId = supertypes.NewStringValue(data.APIKey.ID)
			m.ApiKey = supertypes.NewStringValue(data.APIKey.Value)
		} else {
			m.ApiKeyId = supertypes.NewStringNull()
			m.ApiKey = supertypes.NewStringNull()
		}
		if m.CreateServiceAccountOnly.IsNull() || m.CreateServiceAccountOnly.IsUnknown() {
			m.CreateServiceAccountOnly = supertypes.NewBoolValue(data.Role == "none")
		}
		return m.checkDefaultRole()
	case openai.ProjectServiceAccount:
		m.Id = supertypes.NewStringValue(data.ID)
		m.Name = supertypes.NewStringValue(data.Name)
		m.Role = supertypes.NewStringValue(string(data.Role))
		m.CreatedAt = supertypes.NewInt64Value(data.CreatedAt)
		if m.CreateServiceAccountOnly.IsNull() || m.CreateServiceAccountOnly.IsUnknown() {
			m.CreateServiceAccountOnly = supertypes.NewBoolValue(data.Role == "none")
		}
		return m.checkDefaultRole()
	default:
		var diags diag.Diagnostics
		diags.AddError("Unknown type", fmt.Sprintf("Unknown type: %T", data))
		return diags
	}
}

func (r *ProjectServiceAccountResource) getNewParams(ctx context.Context, data ProjectServiceAccountResourceModel) (*openai.AdminOrganizationProjectServiceAccountNewParams, diag.Diagnostics) {
	return &openai.AdminOrganizationProjectServiceAccountNewParams{
		Name:                     data.Name.ValueString(),
		CreateServiceAccountOnly: openai.Bool(data.CreateServiceAccountOnly.ValueBool()),
	}, nil
}

func (r *ProjectServiceAccountResource) getUpdateParams(ctx context.Context, data ProjectServiceAccountResourceModel) (*openai.AdminOrganizationProjectServiceAccountUpdateParams, diag.Diagnostics) {
	return &openai.AdminOrganizationProjectServiceAccountUpdateParams{
		Name: openai.String(data.Name.ValueString()),
	}, nil
}

func (m *ProjectServiceAccountResourceModel) checkDefaultRole() diag.Diagnostics {
	var diags diag.Diagnostics
	if m.CreateServiceAccountOnly.ValueBool() && m.Role.ValueString() != "none" {
		diags.AddError("Unexpected built-in service-account role", "This account is managed without default roles but the API reports a built-in role. Stop rollout and reconcile its role assignments. The service-account update API cannot set role=none; the provider will not silently retain broader access or delete the account.")
	}
	return diags
}
