package groupuser_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	provider "github.com/openai/terraform-provider-openai/internal/provider"
	"github.com/openai/terraform-provider-openai/internal/provider/listreadtest"
)

func TestGroupUserReadsFromOneList(t *testing.T) {
	server := listreadtest.New(t, "/organization/groups/group_test/users", func(_ string, body map[string]any) map[string]any {
		return map[string]any{"object": "group.user", "id": body["user_id"]}
	})
	config := fmt.Sprintf(`
provider "openai" {
  admin_api_key = "test"
  base_url      = %q
}

resource "openai_group_user" "test" {
  count    = 5
  group_id = "group_test"
  user_id  = "user_${count.index}"
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
