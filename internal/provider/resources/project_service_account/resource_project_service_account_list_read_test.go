package projectserviceaccount_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
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

func TestProjectServiceAccountReadFallsBackToIDWhenServiceAccountIDIsMissing(t *testing.T) {
	ctx := context.Background()
	api := listreadtest.New(t, "/organization/projects/proj_test/service_accounts", func(id string, body map[string]any) map[string]any {
		return map[string]any{"object": "organization.project.service_account", "id": id, "name": body["name"], "role": "none", "created_at": 1}
	})
	server := providerserver.NewProtocol6(provider.New("test")())()
	schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	dynamic := func(typ tftypes.Object, set map[string]tftypes.Value) *tfprotov6.DynamicValue {
		fields := make(map[string]tftypes.Value)
		for name, fieldType := range typ.AttributeTypes {
			fields[name] = tftypes.NewValue(fieldType, nil)
		}
		for name, value := range set {
			fields[name] = value
		}
		value, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, fields))
		if err != nil {
			t.Fatal(err)
		}
		return &value
	}
	nullState := func(typ tftypes.Object) *tfprotov6.DynamicValue {
		value, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, nil))
		if err != nil {
			t.Fatal(err)
		}
		return &value
	}
	failOnError := func(stage string, diagnostics []*tfprotov6.Diagnostic) {
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == tfprotov6.DiagnosticSeverityError {
				t.Fatalf("%s failed: %s: %s", stage, diagnostic.Summary, diagnostic.Detail)
			}
		}
	}
	configured, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: dynamic(schemas.Provider.ValueType().(tftypes.Object), map[string]tftypes.Value{
		"admin_api_key": tftypes.NewValue(tftypes.String, "test"),
		"base_url":      tftypes.NewValue(tftypes.String, api.URL),
	})})
	if err != nil {
		t.Fatal(err)
	}
	failOnError("configure", configured.Diagnostics)

	typ := schemas.ResourceSchemas["openai_project_service_account"].ValueType().(tftypes.Object)
	str := func(value string) tftypes.Value { return tftypes.NewValue(tftypes.String, value) }
	created, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName:     "openai_project_service_account",
		PriorState:   nullState(typ),
		Config:       dynamic(typ, map[string]tftypes.Value{"project_id": str("proj_test"), "name": str("runtime")}),
		PlannedState: dynamic(typ, map[string]tftypes.Value{"project_id": str("proj_test"), "name": str("runtime"), "id": tftypes.NewValue(tftypes.String, tftypes.UnknownValue), "service_account_id": tftypes.NewValue(tftypes.String, tftypes.UnknownValue), "created_at": tftypes.NewValue(tftypes.Number, tftypes.UnknownValue)}),
	})
	if err != nil {
		t.Fatal(err)
	}
	failOnError("create", created.Diagnostics)
	state, err := created.NewState.Unmarshal(typ)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]tftypes.Value
	if err := state.As(&fields); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := fields["id"].As(&id); err != nil {
		t.Fatal(err)
	}

	read, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName:     "openai_project_service_account",
		CurrentState: dynamic(typ, map[string]tftypes.Value{"project_id": str("proj_test"), "name": str("runtime"), "id": str(id)}),
	})
	if err != nil {
		t.Fatal(err)
	}
	failOnError("read", read.Diagnostics)
	refreshed, err := read.NewState.Unmarshal(typ)
	if err != nil {
		t.Fatal(err)
	}
	if err := refreshed.As(&fields); err != nil {
		t.Fatal(err)
	}
	var serviceAccountID string
	if err := fields["service_account_id"].As(&serviceAccountID); err != nil {
		t.Fatal(err)
	}
	if serviceAccountID != id {
		t.Fatalf("service_account_id = %q, want %q", serviceAccountID, id)
	}
}
