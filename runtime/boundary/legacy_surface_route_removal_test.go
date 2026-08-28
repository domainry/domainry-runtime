package boundary_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLegacyManagementHTTPRoutesCannotBeReintroduced(t *testing.T) {
	httpRoot := filepath.Join(runtimeRoot(t), "transport", "http")
	handlePattern := regexp.MustCompile(`HandleFunc\("[A-Z]+ ([^"]+)"`)
	allowedProtocolPrefixes := []string{
		"/integrations/entrypoints/",
		"/integrations/agents/",
		"/integrations/webhooks/",
		"/integrations/google/oauth/callback",
	}
	allowedOperationsRoutes := map[string]bool{
		"/integrations/web-push/subscriptions/cleanup-expired": true,
	}
	forbiddenPrefixes := []string{
		"/audit-events",
		"/scheduler/",
		"/metadata/",
		"/platform-capabilities",
		"/domain-change-plans/",
		"/integrations/",
		"/workflow-executions",
		"/workflow-processes",
		"/workflow-tasks",
		"/workflows/",
	}

	err := filepath.WalkDir(httpRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, match := range handlePattern.FindAllStringSubmatch(string(raw), -1) {
			routePath := match[1]
			if allowedOperationsRoutes[routePath] {
				continue
			}
			if routePath == "/schema" {
				t.Errorf("%s reintroduced removed route %s", path, routePath)
				continue
			}
			allowedProtocol := false
			for _, prefix := range allowedProtocolPrefixes {
				allowedProtocol = allowedProtocol || strings.HasPrefix(routePath, prefix)
			}
			if allowedProtocol {
				continue
			}
			for _, prefix := range forbiddenPrefixes {
				if strings.HasPrefix(routePath, prefix) {
					t.Errorf("%s reintroduced removed management route %s", path, routePath)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeOpsRoutesCannotUseWorkspaceAdminWrapperOrCatalogShortcut(t *testing.T) {
	root := runtimeRoot(t)
	for _, relative := range []string{
		"transport/http/operations/operations_routes.go",
		"transport/http/scheduler/scheduler_routes.go",
		"transport/http/integrations/integrations_routes.go",
	} {
		raw, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		for lineNumber, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, `"/operations`) && strings.Contains(line, ".admin(") {
				t.Errorf("%s:%d wraps an Ops route with workspace admin", relative, lineNumber+1)
			}
		}
	}
	catalog, err := os.ReadFile(filepath.Join(root, "domain", "operations", "projection", "operations_definition_catalog_projection.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(catalog), `"workspace.admin"`) {
		t.Fatal("Runtime Operations catalog reintroduced workspace.admin as an operation permission")
	}
}
