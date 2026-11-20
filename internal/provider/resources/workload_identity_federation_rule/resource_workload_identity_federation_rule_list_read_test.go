package workloadidentityfederationrule_test

import (
	"fmt"
	"maps"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	provider "github.com/openai/terraform-provider-openai/internal/provider"
	"github.com/openai/terraform-provider-openai/internal/provider/listreadtest"
)

func TestWorkloadIdentityFederationRuleReadsFromOneList(t *testing.T) {
	server := listreadtest.New(t, "/organization/workload_identity_providers/wip_test/federation_rules", func(id string, body map[string]any) map[string]any {
		item := map[string]any{"object": "workload_identity_federation_rule", "id": id, "workload_identity_provider_id": "wip_test", "enabled": true, "created_at": 1, "updated_at": 1}
		maps.Copy(item, body)
		return item
	})
	config := fmt.Sprintf(`
provider "openai" {
  admin_api_key = "test"
  base_url      = %q
}

resource "openai_workload_identity_federation_rule" "test" {
  count                         = 5
  workload_identity_provider_id = "wip_test"
  project_id                    = "proj_test"
  service_account_id            = "user_${count.index}"
  name                          = "rule-${count.index}"
  attribute_matchers            = { feature = "feature-${count.index}" }
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
