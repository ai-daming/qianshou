package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAPIDeclaresEveryV1ReadRoute(t *testing.T) {
	contract := filepath.Join("..", "..", "..", "..", "protocol", "openapi.yaml")
	body, err := os.ReadFile(contract)
	if err != nil {
		t.Fatalf("read OpenAPI contract: %v", err)
	}
	text := string(body)
	for _, declaration := range []string{
		"  /healthz:",
		"  /api/v1/projects:",
		"  /api/v1/projects/{projectId}/milestones:",
		"  /api/v1/projects/{projectId}/milestones/{milestoneNumber}/issues:",
		"  /api/v1/projects/{projectId}/issues/{issueNumber}:",
		"operationId: listProjects",
		"operationId: listProjectMilestones",
		"operationId: listMilestoneIssues",
		"operationId: getProjectIssue",
	} {
		if !strings.Contains(text, declaration) {
			t.Fatalf("OpenAPI contract missing %q", declaration)
		}
	}
}
