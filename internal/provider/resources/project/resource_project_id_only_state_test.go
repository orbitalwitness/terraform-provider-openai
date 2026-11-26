package project_test

import (
	"testing"

	"github.com/openai/terraform-provider-openai/internal/provider/listreadtest"
)

func TestProjectReadFallsBackToIDWhenProjectIDIsMissing(t *testing.T) {
	server := listreadtest.New(t, "/organization/projects", func(id string, body map[string]any) map[string]any {
		return map[string]any{"object": "organization.project", "id": id, "name": body["name"], "status": "active", "created_at": 1}
	})
	server.CheckReadFromIDOnlyState(t, "openai_project", map[string]string{"name": "runtime"}, "project_id")
}
