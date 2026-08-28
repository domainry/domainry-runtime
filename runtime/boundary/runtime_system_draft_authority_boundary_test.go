package boundary

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeDefinitionMutationsHaveNoDirectHTTPAuthoringBypass(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	roots := []string{
		filepath.Join(repositoryRoot, "runtime", "transport", "http", "metadata", "metadata_routes.go"),
		filepath.Join(repositoryRoot, "runtime", "transport", "http", "automation", "automation_routes.go"),
		filepath.Join(repositoryRoot, "runtime", "transport", "http", "workflows"),
		filepath.Join(repositoryRoot, "runtime", "transport", "http", "openapi"),
		filepath.Join(repositoryRoot, "runtime", "application", "metadata"),
		filepath.Join(repositoryRoot, "runtime", "application", "automation"),
		filepath.Join(repositoryRoot, "runtime", "application", "workflow"),
	}
	forbidden := []string{
		"PUT /metadata/definitions/",
		"DELETE /metadata/definitions/",
		"/metadata/definitions/{resourceType}/{resourceKey}/rollback",
		"PUT /automation-rules/{ruleKey}",
		"DELETE /automation-rules/{ruleKey}",
		"/automation-rules/{ruleKey}/rollback",
		"/automation-rules/{ruleKey}/enable",
		"/automation-rules/{ruleKey}/disable",
		" UpsertMetadataDefinition(",
		" DisableMetadataDefinition(",
		" RollbackMetadataDefinition(",
		" RollbackMetadataDefinitionIdempotent(",
		" UpsertAutomationRule(",
		" RollbackAutomationRule(",
		" SetAutomationRuleEnabled(",
		" DisableAutomationRule(",
		"func (h *MetadataHandler) upsertMetadataDefinition(",
		"func (h *MetadataHandler) rollbackMetadataDefinition(",
		"func (h *MetadataHandler) disableMetadataDefinition(",
		"/workflows/{workflowKey}/draft",
		"/workflows/{workflowKey}/versions",
		"/workflows/{workflowKey}/rollback",
		"/workflows/{workflowKey}/enable",
		"/workflows/{workflowKey}/disable",
		" CreateWorkflowDefinitionDraft(",
		" UpdateWorkflowDefinitionDraft(",
		" DeleteWorkflowDefinitionDraft(",
		" PublishWorkflowDefinitionDraft(",
		" CreateWorkflowDraftFromVersion(",
		" ArchiveWorkflowDefinitionVersion(",
		" SetWorkflowDefinitionEnabled(",
		" RollbackWorkflowDefinition(",
	}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for _, token := range forbidden {
				if strings.Contains(string(content), token) {
					t.Errorf("direct Runtime definition authoring bypass %q found in %s", token, filepath.ToSlash(path))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
