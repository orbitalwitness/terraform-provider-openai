package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func TestWorkloadIdentityLifecycle(t *testing.T) {
	// A normal inference key must never override the configured admin key.
	t.Setenv("OPENAI_API_KEY", "sk-inference-must-not-be-used")
	var mu sync.Mutex
	objects := map[string]map[string]any{}
	creates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if req.Header.Get("Authorization") != "Bearer sk-admin-test" {
			t.Errorf("wrong authentication header: %q", req.Header.Get("Authorization"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		path := strings.TrimPrefix(req.URL.Path, "/")
		value, exists := objects[path]
		switch req.Method {
		case http.MethodPost:
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			if !exists {
				creates++
				if path == workloadIdentityPath {
					if body["type"] != "oidc" {
						t.Error("provider create must set type=oidc")
					}
					value = map[string]any{"id": "wip_test", "verification": map[string]any{"type": "discovery", "discovery_url": nil}, "description": nil}
				} else if strings.HasSuffix(path, "/federation_rules") {
					if body["project_id"] == nil || body["service_account_id"] == nil {
						t.Error("missing rule target")
					}
					value = map[string]any{"id": fmt.Sprintf("wifr_%d", creates), "workload_identity_provider_id": "wip_test", "name": nil, "description": nil, "permissions": map[string]any{"type": "service_account_default"}}
				} else {
					t.Errorf("unexpected POST path %s", path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				id, ok := value["id"].(string)
				if !ok {
					t.Fatal("mock resource ID must be a string")
				}
				path += "/" + id
			} else {
				for _, immutable := range []string{"type", "project_id", "service_account_id", "workload_identity_provider_id"} {
					if _, ok := body[immutable]; ok {
						t.Errorf("update sent immutable field %s", immutable)
					}
				}
			}
			for key, item := range body {
				value[key] = item
			}
			if verification, ok := value["verification"].(map[string]any); ok && verification["type"] == "discovery" {
				if _, exists := verification["discovery_url"]; !exists {
					verification["discovery_url"] = nil
				}
			}
			objects[path] = value
		case http.MethodDelete:
			delete(objects, path)
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"deleted":true}`))
			return
		case http.MethodGet:
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
		default:
			t.Errorf("unexpected method %s", req.Method)
		}
		if err := json.NewEncoder(w).Encode(value); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()

	config := func(extraProvider, extraRule, serviceAccount string) string {
		return fmt.Sprintf(`
provider "openai" {
  base_url = %q
  admin_key = "sk-admin-test"
}
resource "openai_workload_identity_provider" "test" {
  name = "GitHub Actions"
  issuer = "https://token.actions.githubusercontent.com"
  audience = "openai"
  %s
}
resource "openai_workload_identity_federation_rule" "test" {
  workload_identity_provider_id = openai_workload_identity_provider.test.id
  project_id = "proj_test"
  service_account_id = %q
  attribute_matchers = jsonencode({ sub = "repo:org/repo:ref:refs/heads/main", protected = true, version = 1 })
  %s
}
`, server.URL+"/", extraProvider, serviceAccount, extraRule)
	}
	rn := "openai_workload_identity_federation_rule.test"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){"openai": providerserver.NewProtocol6WithError(New("test")())},
		Steps: []resource.TestStep{
			{Config: config("", "", "svc_test"), Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("openai_workload_identity_provider.test", "verification.type", "discovery"),
				resource.TestCheckResourceAttr(rn, "permissions.type", "service_account_default"),
			)},
			{ResourceName: "openai_workload_identity_provider.test", ImportState: true, ImportStateVerify: true},
			{ResourceName: rn, ImportState: true, ImportStateId: "wip_test/wifr_2", ImportStateVerify: true},
			{Config: config(`description = "temporary"
enabled = false
attribute_transformations = { subject = "assertion.sub" }
verification = { type = "uploaded_jwks", jwks = jsonencode({keys = []}) }`, `name = "release"
description = "temporary"
enabled = false
permissions = { type = "restricted", scopes = ["api.model.read"] }`, "svc_test"), Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr("openai_workload_identity_provider.test", "enabled", "false"),
				resource.TestCheckResourceAttr(rn, "enabled", "false"),
				resource.TestCheckResourceAttr(rn, "permissions.type", "restricted"),
			)},
			// Clear optional fields and explicitly restore server-default verification and permissions.
			{Config: config(`verification = { type = "discovery" }`, `permissions = { type = "service_account_default" }`, "svc_test"), Check: resource.ComposeTestCheckFunc(
				resource.TestCheckNoResourceAttr("openai_workload_identity_provider.test", "description"),
				resource.TestCheckNoResourceAttr(rn, "name"),
				resource.TestCheckNoResourceAttr(rn, "description"),
			)},
			{Config: config(`verification = { type = "discovery" }`, `permissions = { type = "service_account_default" }`, "svc_replacement"), Check: resource.TestCheckResourceAttr(rn, "service_account_id", "svc_replacement")},
			// An out-of-band deletion must remove both resources from state on refresh.
			{PreConfig: func() { mu.Lock(); clear(objects); mu.Unlock() }, RefreshState: true, ExpectNonEmptyPlan: true},
		},
	})
	if creates != 3 {
		t.Errorf("expected provider + rule + replacement creation, got %d", creates)
	}
}

func TestWorkloadIdentityInvalidMatchers(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){"openai": providerserver.NewProtocol6WithError(New("test")())},
		Steps: []resource.TestStep{{
			Config: `provider "openai" { admin_key = "sk-admin-test" }
resource "openai_workload_identity_federation_rule" "test" {
  workload_identity_provider_id = "wip_test"
  project_id = "proj_test"
  service_account_id = "svc_test"
  attribute_matchers = jsonencode({sub = ["not-a-scalar"]})
}`,
			PlanOnly: true, ExpectError: regexp.MustCompile("Invalid attribute matchers"),
		}},
	})
}

func TestWorkloadIdentityAPIErrors(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":{"message":"unavailable","type":"test_error"}}`))
			}))
			defer server.Close()
			client := openai.NewClient(option.WithBaseURL(server.URL+"/"), option.WithAdminAPIKey("sk-admin-test"), option.WithMaxRetries(0))
			ctx := t.Context()
			for _, factory := range []func() frameworkresource.Resource{NewWorkloadIdentityProviderResource, NewWorkloadIdentityFederationRuleResource} {
				r := factory()
				var schemaResponse frameworkresource.SchemaResponse
				r.Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResponse)
				state := tfsdk.State{Schema: schemaResponse.Schema}
				switch r := r.(type) {
				case *WorkloadIdentityProviderResource:
					r.client = &client
					var data workloadIdentityProviderModel
					if diags := data.fill(ctx, workloadIdentityProviderResponse{ID: "wip_test", Verification: workloadIdentityVerification{Type: "discovery"}}); diags.HasError() {
						t.Fatal(diags)
					}
					if diags := state.Set(ctx, &data); diags.HasError() {
						t.Fatal(diags)
					}
				case *WorkloadIdentityFederationRuleResource:
					r.client = &client
					var data workloadIdentityFederationRuleModel
					if diags := data.fill(ctx, workloadIdentityFederationRuleResponse{ID: "wifr_test", WorkloadIdentityProviderID: "wip_test", AttributeMatchers: json.RawMessage(`{"sub":"test"}`), Permissions: workloadIdentityPermissions{Type: "service_account_default"}}); diags.HasError() {
						t.Fatal(diags)
					}
					if diags := state.Set(ctx, &data); diags.HasError() {
						t.Fatal(diags)
					}
				}
				read := frameworkresource.ReadResponse{State: state}
				r.Read(ctx, frameworkresource.ReadRequest{State: state}, &read)
				if status == http.StatusNotFound {
					if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
						t.Errorf("404 read must remove state: %v", read.Diagnostics)
					}
				} else if !read.Diagnostics.HasError() || read.State.Raw.IsNull() {
					t.Error("403 read must report an error and retain state")
				}
				var deleted frameworkresource.DeleteResponse
				r.Delete(ctx, frameworkresource.DeleteRequest{State: state}, &deleted)
				if deleted.Diagnostics.HasError() != (status == http.StatusForbidden) {
					t.Errorf("unexpected delete diagnostics: %v", deleted.Diagnostics)
				}
			}
		})
	}
}

func TestWorkloadIdentityInvalidConfiguration(t *testing.T) {
	ctx := t.Context()
	verification, diags := workloadIdentityVerificationValue(ctx, workloadIdentityVerification{Type: "uploaded_jwks"})
	if diags.HasError() {
		t.Fatal(diags)
	}
	provider := workloadIdentityProviderModel{Verification: verification, AttributeTransformations: types.MapValueMust(types.StringType, map[string]attr.Value{})}
	_, diags = provider.body(ctx, true)
	if !diags.HasError() {
		t.Error("uploaded_jwks must require jwks")
	}
	permissions, diags := workloadIdentityPermissionsValue(ctx, workloadIdentityPermissions{Type: "restricted"})
	if diags.HasError() {
		t.Fatal(diags)
	}
	rule := workloadIdentityFederationRuleModel{Permissions: permissions}
	_, diags = rule.body(ctx, true)
	if !diags.HasError() {
		t.Error("restricted permissions must require scopes")
	}
}
