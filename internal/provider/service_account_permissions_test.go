package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/openai/openai-go/v3"
	supertypes "github.com/orange-cloudavenue/terraform-plugin-framework-supertypes"
	"regexp"
)

func TestServiceAccountCreationMode(t *testing.T) {
	for _, keyless := range []bool{false, true} {
		t.Run(fmt.Sprint(keyless), func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", "sk-runtime-not-admin")
			var mu sync.Mutex
			var account map[string]any
			creates, deletes := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("Authorization") != "Bearer sk-admin-test" {
					t.Error("runtime credential used for provisioning")
					w.WriteHeader(401)
					return
				}
				if !strings.HasPrefix(r.URL.Path, "/organization/projects/proj_test/service_accounts") {
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				switch r.Method {
				case http.MethodPost:
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						return
					}
					if strings.HasSuffix(r.URL.Path, "/service_accounts") {
						creates++
						if body["create_service_account_only"] != keyless {
							t.Errorf("wrong creation mode: %v", body)
						}
						role := "member"
						if keyless {
							role = "none"
						}
						account = map[string]any{"id": "svc_test", "name": body["name"], "role": role, "created_at": 1, "object": "organization.project.service_account"}
						response := map[string]any{}
						for k, v := range account {
							response[k] = v
						}
						if !keyless {
							response["api_key"] = map[string]any{"id": "key_test", "value": "sk-runtime-test", "name": "Secret Key", "created_at": 1, "object": "organization.project.service_account.api_key"}
						} else {
							response["api_key"] = nil
						}
						_ = json.NewEncoder(w).Encode(response)
						return
					}
					if body["role"] != nil || body["create_service_account_only"] != nil {
						t.Error("rename must not change role or creation mode")
					}
					account["name"] = body["name"]
				case http.MethodGet:
					if account == nil {
						w.WriteHeader(404)
						_, _ = w.Write([]byte(`{"error":{"message":"missing"}}`))
						return
					}
				case http.MethodDelete:
					deletes++
					account = nil
					_, _ = w.Write([]byte(`{"deleted":true,"id":"svc_test","object":"organization.project.service_account.deleted"}`))
					return
				default:
					t.Errorf("unexpected method %s", r.Method)
				}
				_ = json.NewEncoder(w).Encode(account)
			}))
			defer server.Close()
			config := func(name string) string {
				mode := ""
				if keyless {
					mode = "create_service_account_only = true"
				}
				return fmt.Sprintf(`provider "openai" {
 base_url = %q
 admin_key = "sk-admin-test"
}
resource "openai_project_service_account" "runtime" {
 project_id = "proj_test"
 name = %q
 %s
}`, server.URL+"/", name, mode)
			}
			rn := "openai_project_service_account.runtime"
			check := resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(rn, "id", "svc_test"), resource.TestCheckResourceAttr(rn, "create_service_account_only", fmt.Sprint(keyless)))
			checks := []resource.TestStep{}
			if !keyless {
				checks = append(checks, resource.TestStep{Config: strings.Replace(config("renamed"), "name = \"renamed\"", "name = \"renamed\"\n create_service_account_only = true", 1), PlanOnly: true, ExpectError: regexp.MustCompile("Remove built-in role assignments first")})
			}

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){"openai": providerserver.NewProtocol6WithError(New("test")())},
				Steps: append([]resource.TestStep{
					{Config: config("runtime"), Check: resource.ComposeTestCheckFunc(check, func(s *terraform.State) error {
						attrs := s.RootModule().Resources[rn].Primary.Attributes
						if keyless && (attrs["api_key"] != "" || attrs["api_key_id"] != "" || attrs["role"] != "none") {
							return fmt.Errorf("keyless account has credential/default role")
						}
						if !keyless && attrs["api_key"] != "sk-runtime-test" {
							return fmt.Errorf("legacy key was not preserved")
						}
						return nil
					})},
					{Config: config("renamed"), Check: check},
					{ResourceName: rn, ImportState: true, ImportStateId: "proj_test/svc_test", ImportStateVerify: true, ImportStateVerifyIgnore: []string{"api_key", "api_key_id"}},
					{PreConfig: func() { mu.Lock(); account["name"] = "external-change"; mu.Unlock() }, Config: config("renamed"), PlanOnly: true, ExpectNonEmptyPlan: true},
					{Config: config("renamed"), Check: resource.TestCheckResourceAttr(rn, "name", "renamed")},
				}, append(checks, []resource.TestStep{
					{PreConfig: func() { mu.Lock(); account["role"] = "none"; mu.Unlock() }, Config: strings.Replace(strings.ReplaceAll(config("renamed"), "create_service_account_only = true", ""), "name = \"renamed\"", "name = \"renamed\"\n create_service_account_only = true", 1), Check: resource.TestCheckResourceAttr(rn, "create_service_account_only", "true")},
					{PreConfig: func() { mu.Lock(); account = nil; mu.Unlock() }, Config: config("renamed"), PlanOnly: true, ExpectNonEmptyPlan: true},
				}...)...),
			})
			if creates != 1 || deletes != 1 {
				t.Errorf("rename/import/drift unexpectedly replaced account: creates=%d deletes=%d", creates, deletes)
			}
		})
	}
}

func TestServiceAccountDefaultRoleDrift(t *testing.T) {
	for _, role := range []string{"member", "owner"} {
		m := ProjectServiceAccountResourceModel{CreateServiceAccountOnly: supertypes.NewBoolValue(true)}
		diags := m.Fill(context.Background(), openai.ProjectServiceAccount{Role: openai.ProjectServiceAccountRole(role)})
		if !diags.HasError() {
			t.Errorf("accepted broadened built-in role %s", role)
		}
	}
}

func TestRuntimeProjectRolePermissions(t *testing.T) {
	var mu sync.Mutex
	var role map[string]any
	creates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer sk-admin-test" {
			t.Error("wrong credential")
			w.WriteHeader(401)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/projects/proj_test/roles") {
			t.Errorf("wrong role path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		switch r.Method {
		case http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if role == nil {
				creates++
				role = map[string]any{"id": "role_inference", "object": "role", "created_at": 1, "resource_type": "project", "predefined_role": false}
			}
			role["name"] = body["role_name"]
			role["description"] = body["description"]
			role["permissions"] = body["permissions"]
		case http.MethodGet:
			if role == nil {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"error":{"message":"missing"}}`))
				return
			}
		case http.MethodDelete:
			role = nil
			_, _ = w.Write([]byte(`{"deleted":true,"id":"role_inference","object":"role.deleted"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(role)
	}))
	defer server.Close()
	config := func(permissions string) string {
		return fmt.Sprintf(`provider "openai" {
 base_url = %q
 admin_key = "sk-admin-test"
}
resource "openai_project_role" "runtime" {
 project_id = "proj_test"
 name = "inference"
 description = "Runtime inference only"
 permissions = %s
}`, server.URL+"/", permissions)
	}
	rn := "openai_project_role.runtime"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){"openai": providerserver.NewProtocol6WithError(New("test")())},
		Steps: []resource.TestStep{
			{Config: config(`["api.responses.write","api.model.request"]`)},
			{ResourceName: rn, ImportState: true, ImportStateId: "proj_test/role_inference", ImportStateVerify: true},
			{Config: config(`["api.responses.write"]`), Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(rn, "id", "role_inference"), resource.TestCheckResourceAttr(rn, "permissions.#", "1"))},
			{PreConfig: func() {
				mu.Lock()
				role["permissions"] = []string{"api.responses.write", "api.organization.projects.api_keys.write"}
				mu.Unlock()
			}, Config: config(`["api.responses.write"]`), PlanOnly: true, ExpectNonEmptyPlan: true},
			{Config: config(`["api.responses.write"]`), Check: resource.TestCheckResourceAttr(rn, "permissions.#", "1")},
		},
	})
	if creates != 1 {
		t.Errorf("permission update replaced role: %d creates", creates)
	}
}
