package boundary_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentPublicContractsAreOwnedBySDK(t *testing.T) {
	root := runtimeRoot(t)
	for _, obsolete := range []string{
		filepath.Join(root, "domain", "agent", "model", "agent_definition.go"),
		filepath.Join(root, "application", "agent", "runtime", "agent_runner_contract.go"),
	} {
		if _, err := os.Stat(obsolete); err == nil || !os.IsNotExist(err) {
			t.Fatalf("Runtime compatibility contract must be absent: %s", obsolete)
		}
	}
	forbidden := map[string]bool{
		"SkillSchema": true, "AgentSchema": true, "AgentExecutionLimits": true,
		"AgentTaskDefinition": true, "GlobalAgentContextContract": true,
		"AgentRoutingContract": true, "AgentEntrypointAssignment": true,
		"AgentTaskIdentity": true, "AgentServicePrincipalBinding": true,
		"InteractiveAgentHandoff": true, "AgentExecutionIdentity": true,
		"GlobalAgentContext": true, "AgentTaskRunnerRequest": true,
		"AgentTaskRunnerResult": true, "InteractiveAgentRunRequest": true,
		"AgentRouteCandidate": true, "AgentRouteResult": true,
		"InteractiveAgentResult": true,
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				typeSpec := specification.(*ast.TypeSpec)
				if forbidden[typeSpec.Name.Name] {
					t.Errorf("Runtime redeclares Agent SDK contract %s in %s", typeSpec.Name.Name, path)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
