package listreadtest

import (
	"context"
	"maps"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	provider "github.com/openai/terraform-provider-openai/internal/provider"
)

// CheckReadFromIDOnlyState creates one resource through the provider, then
// refreshes it from a state that carries only its config and `id`, as state
// written by providers without the typed ID attribute does. It fails unless
// the refresh keeps the resource and fills idAttribute with the same ID.
func (s *Server) CheckReadFromIDOnlyState(t *testing.T, typeName string, config map[string]string, idAttribute string) {
	t.Helper()
	ctx := context.Background()
	server := providerserver.NewProtocol6(provider.New("test")())()
	schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	encode := func(typ tftypes.Object, value tftypes.Value) *tfprotov6.DynamicValue {
		dynamic, err := tfprotov6.NewDynamicValue(typ, value)
		if err != nil {
			t.Fatal(err)
		}
		return &dynamic
	}
	object := func(typ tftypes.Object, set map[string]tftypes.Value) *tfprotov6.DynamicValue {
		fields := make(map[string]tftypes.Value)
		for name, fieldType := range typ.AttributeTypes {
			fields[name] = tftypes.NewValue(fieldType, nil)
		}
		maps.Copy(fields, set)
		return encode(typ, tftypes.NewValue(typ, fields))
	}
	failOnError := func(stage string, diagnostics []*tfprotov6.Diagnostic) {
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == tfprotov6.DiagnosticSeverityError {
				t.Fatalf("%s failed: %s: %s", stage, diagnostic.Summary, diagnostic.Detail)
			}
		}
	}
	stringValue := func(value string) tftypes.Value { return tftypes.NewValue(tftypes.String, value) }
	readString := func(state *tfprotov6.DynamicValue, typ tftypes.Object, attribute string) string {
		value, err := state.Unmarshal(typ)
		if err != nil {
			t.Fatal(err)
		}
		if value.IsNull() {
			t.Fatalf("resource was removed from state")
		}
		var fields map[string]tftypes.Value
		if err := value.As(&fields); err != nil {
			t.Fatal(err)
		}
		var result string
		if err := fields[attribute].As(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}

	configured, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: object(schemas.Provider.ValueType().(tftypes.Object), map[string]tftypes.Value{
		"admin_api_key": stringValue("test"),
		"base_url":      stringValue(s.URL),
	})})
	if err != nil {
		t.Fatal(err)
	}
	failOnError("configure", configured.Diagnostics)

	typ := schemas.ResourceSchemas[typeName].ValueType().(tftypes.Object)
	configValues := make(map[string]tftypes.Value)
	for name, value := range config {
		configValues[name] = stringValue(value)
	}
	planned := make(map[string]tftypes.Value)
	for name, fieldType := range typ.AttributeTypes {
		planned[name] = tftypes.NewValue(fieldType, tftypes.UnknownValue)
	}
	maps.Copy(planned, configValues)
	created, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName:     typeName,
		PriorState:   encode(typ, tftypes.NewValue(typ, nil)),
		Config:       object(typ, configValues),
		PlannedState: object(typ, planned),
	})
	if err != nil {
		t.Fatal(err)
	}
	failOnError("create", created.Diagnostics)
	id := readString(created.NewState, typ, "id")

	idOnly := maps.Clone(configValues)
	idOnly["id"] = stringValue(id)
	read, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: object(typ, idOnly)})
	if err != nil {
		t.Fatal(err)
	}
	failOnError("read", read.Diagnostics)
	if got := readString(read.NewState, typ, idAttribute); got != id {
		t.Fatalf("%s = %q, want %q", idAttribute, got, id)
	}
}
