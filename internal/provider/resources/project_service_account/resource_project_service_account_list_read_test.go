package projectserviceaccount_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	provider "github.com/openai/terraform-provider-openai/internal/provider"
	"github.com/openai/terraform-provider-openai/internal/provider/listreadtest"
)

func TestProjectServiceAccountReadsFromOneList(t *testing.T) {
	server := listreadtest.New(t, "/organization/projects/proj_test/service_accounts", func(id string, body map[string]any) map[string]any {
		return map[string]any{"object": "organization.project.service_account", "id": id, "name": body["name"], "role": "none", "created_at": 1}
	})
	config := fmt.Sprintf(`
provider "openai" {
  admin_api_key = "test"
  base_url      = %q
}

resource "openai_project_service_account" "test" {
  count      = 5
  project_id = "proj_test"
  name       = "runtime-${count.index}"
}
`, server.URL)
	checkReadsThenDelete := func() {
		if err := server.CheckReads(2); err != nil {
			t.Fatal(err)
		}
		server.DeleteFirst()
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"openai": providerserver.NewProtocol6WithError(provider.New("test")()),
		},
		Steps: []resource.TestStep{
			{Config: config},
			{PreConfig: server.ResetCounts, Config: config, PlanOnly: true},
			{PreConfig: checkReadsThenDelete, Config: config, PlanOnly: true, ExpectNonEmptyPlan: true},
		},
	})
}
