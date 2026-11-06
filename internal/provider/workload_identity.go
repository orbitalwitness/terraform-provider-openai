package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const workloadIdentityPath = "organization/workload_identity_providers"

func (r *baseResource) workloadIdentityRequest(ctx context.Context, method, path string, body, result any) error {
	// Execute prefers OPENAI_API_KEY; these endpoints must use the configured admin key.
	return r.client.Execute(ctx, method, path, body, result, option.WithAPIKey(""))
}

func workloadIdentityNotFound(err error) bool {
	var apiErr *openai.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

type workloadIdentityVerification struct {
	Type         string          `json:"type"`
	DiscoveryURL *string         `json:"discovery_url,omitempty"`
	JWKS         json.RawMessage `json:"jwks,omitempty"`
}

type workloadIdentityVerificationModel struct {
	Type         types.String         `tfsdk:"type"`
	DiscoveryURL types.String         `tfsdk:"discovery_url"`
	JWKS         jsontypes.Normalized `tfsdk:"jwks"`
}

var workloadIdentityVerificationTypes = map[string]attr.Type{
	"type": types.StringType, "discovery_url": types.StringType, "jwks": jsontypes.NormalizedType{},
}

type workloadIdentityPermissions struct {
	Type   string   `json:"type"`
	Scopes []string `json:"scopes,omitempty"`
}

type workloadIdentityPermissionsModel struct {
	Type   types.String `tfsdk:"type"`
	Scopes types.Set    `tfsdk:"scopes"`
}

var workloadIdentityPermissionsTypes = map[string]attr.Type{
	"type": types.StringType, "scopes": types.SetType{ElemType: types.StringType},
}

// Matchers are JSON because the API accepts a mixture of strings, booleans and numbers.
type workloadIdentityMatchersValidator struct{}

func (workloadIdentityMatchersValidator) Description(context.Context) string {
	return "must be a JSON object whose values are strings, booleans or numbers"
}

func (v workloadIdentityMatchersValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v workloadIdentityMatchersValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var matchers map[string]any
	if err := json.Unmarshal([]byte(req.ConfigValue.ValueString()), &matchers); err != nil || matchers == nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid attribute matchers", v.Description(ctx))
		return
	}
	for _, value := range matchers {
		switch value.(type) {
		case string, bool, float64:
		default:
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid attribute matchers", v.Description(ctx))
			return
		}
	}
}

func workloadIdentityVerificationValue(ctx context.Context, value workloadIdentityVerification) (types.Object, diag.Diagnostics) {
	jwks := jsontypes.NewNormalizedNull()
	if len(value.JWKS) > 0 && string(value.JWKS) != "null" {
		jwks = jsontypes.NewNormalizedValue(string(value.JWKS))
	}
	return types.ObjectValueFrom(ctx, workloadIdentityVerificationTypes, workloadIdentityVerificationModel{
		Type: types.StringValue(value.Type), DiscoveryURL: types.StringPointerValue(value.DiscoveryURL), JWKS: jwks,
	})
}

func workloadIdentityPermissionsValue(ctx context.Context, value workloadIdentityPermissions) (types.Object, diag.Diagnostics) {
	scopes := types.SetNull(types.StringType)
	var diags diag.Diagnostics
	if value.Type == "restricted" {
		scopes, diags = types.SetValueFrom(ctx, types.StringType, value.Scopes)
	}
	result, objectDiags := types.ObjectValueFrom(ctx, workloadIdentityPermissionsTypes, workloadIdentityPermissionsModel{
		Type: types.StringValue(value.Type), Scopes: scopes,
	})
	diags.Append(objectDiags...)
	return result, diags
}

func workloadIdentityIDAttribute() schema.StringAttribute {
	return schema.StringAttribute{Computed: true, MarkdownDescription: "The resource ID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}}
}
