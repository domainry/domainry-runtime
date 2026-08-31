package http

// These tests enforce cross-package HTTP architecture boundaries.

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
)

func TestHTTPRouterDoesNotRetainAgentStateRepository(t *testing.T) {
	repositoryType := reflect.TypeOf((*agentpersistence.AgentStateRepository)(nil)).Elem()
	routerType := reflect.TypeOf(HTTPRouter{})
	for index := 0; index < routerType.NumField(); index++ {
		field := routerType.Field(index)
		if field.Type == repositoryType {
			t.Fatalf("HTTP Router field %s retains AgentStateRepository", field.Name)
		}
	}
}

func TestRuntimeHTTPDoesNotDeclareAgentProductRoutes(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, route := range []string{"/agent-dialog", "/operations/agent"} {
			if strings.Contains(string(body), route) {
				t.Errorf("Runtime HTTP source %s redeclares Agent-owned route %q", path, route)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
