package boundary_test

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

var workspaceFallbackLine = regexp.MustCompile(`(?i)workspace[^\n]{0,80}(==|!=)[[:space:]]*""|workspace[^\n]{0,100}(default|fallback)|default[^\n]{0,100}workspace|"default"|'default'`)

var reviewedWorkspaceFallbackBaseline = map[string]int{
	"runtime/application/seed/business/records.go":                                             1,
	"runtime/application/seed/globalcapability/runtime.go":                                     1,
	"runtime/application/audit/audit_application_service.go":                                   2,
	"runtime/application/automation/automation_definition_validation_application_service.go":   1,
	"runtime/application/integration/integration_application_delivery_management.go":           2,
	"runtime/application/integration/integration_application_execution_evidence.go":            1,
	"runtime/application/integration/integration_application_failure_alerts.go":                3,
	"runtime/application/integration/integration_application_inbound_webhooks.go":              2,
	"runtime/application/metadata/metadata_localized_text_coverage_application_service.go":     1,
	"runtime/application/seed/deployment/frontend_capability.go":                               1,
	"runtime/application/workflow/workflow_execution_idempotency_application_service.go":       1,
	"runtime/infrastructure/persistence/database/action/action_business_execution_store.go":    1,
	"runtime/infrastructure/persistence/database/automation/sql_values.go":                     1,
	"runtime/infrastructure/persistence/database/deployment/runtime_status_store.go":           3,
	"runtime/infrastructure/persistence/database/integration/integration_worker_scope.go":      1,
	"runtime/infrastructure/persistence/database/metadata/manifest_store.go":                   1,
	"runtime/infrastructure/persistence/database/metadata/definition_store.go":                 1,
	"runtime/infrastructure/persistence/database/operations/operations_lease_release_store.go": 1,
	"runtime/infrastructure/persistence/database/record/record_batch_job_store.go":             3,
	"runtime/infrastructure/persistence/database/schema/idempotency_receipt_migration.go":      1,
	"runtime/infrastructure/persistence/database/schema/evidence_tables.go":                    1,
	"runtime/infrastructure/persistence/database/transaction/boundary_intent_store.go":         1,
	"runtime/infrastructure/persistence/database/workflow/workflow_execution_receipt_store.go": 1,
	"runtime/infrastructure/persistence/database/workspace_rls.go":                             1,
}

func TestWorkspaceScopeInventoryCoversRuntimeSchemaAndNonDatabaseSurfaces(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	documentPath := filepath.Join(repositoryRoot, "docs", "architecture", "runtime-workspace-scope-inventory.md")
	raw, err := os.ReadFile(documentPath)
	if err != nil {
		t.Fatal(err)
	}
	document := string(raw)

	store, err := database.OpenContext(t.Context(), config.Config{
		DatabaseDriver:       "sqlite",
		DBPath:               filepath.Join(t.TempDir(), "workspace-inventory.db"),
		IntegrationSecretKey: "workspace-inventory-test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := store.DB().QueryContext(t.Context(), "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(document, "`"+table+"`") {
			t.Errorf("Runtime schema table %s is missing from workspace scope inventory", table)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	for _, surface := range []string{
		"`workspace_scoped`",
		"`runtime_global`",
		"`installation_scoped`",
		"`public`",
		"`ObjectSchema.Key`",
		"`application/upload`",
		"`agentReportDownloadTasks`",
		"`dictionaryCache`",
		"`MemoryLimiter.buckets`",
		"workflow executions",
		"scheduler job definitions",
		"integration events",
		"provider callback/subscription mapping",
	} {
		if !strings.Contains(document, surface) {
			t.Errorf("workspace scope inventory is missing non-database surface %s", surface)
		}
	}
}

func TestWorkspaceFallbackInventoryIsAnExactNonGrowingBaseline(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	documentPath := filepath.Join(repositoryRoot, "docs", "architecture", "runtime-workspace-fallback-inventory.md")
	raw, err := os.ReadFile(documentPath)
	if err != nil {
		t.Fatal(err)
	}
	document := string(raw)
	actual := map[string]int{}
	for _, root := range []string{"runtime/application", "runtime/infrastructure"} {
		err := filepath.WalkDir(filepath.Join(repositoryRoot, root), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			relative, err := filepath.Rel(repositoryRoot, path)
			if err != nil {
				return err
			}
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				line := scanner.Text()
				// This migration inspector names and reports legacy default values;
				// it never substitutes one workspace for another.
				if filepath.ToSlash(relative) == "runtime/infrastructure/persistence/database/workspace_scope_migration.go" {
					continue
				}
				// Scheduler's installation seed name is business vocabulary, not
				// an empty-workspace fallback.
				if strings.Contains(line, "default scheduler") || strings.Contains(line, "ensureDefaultWorkflowDefinition") {
					continue
				}
				// Lifecycle's default policy catalog is explicit tenant policy
				// vocabulary; it never supplies a fallback workspace.
				if strings.Contains(line, "InstallDefaultPolicies") || strings.Contains(line, "DefaultPolicyCatalog") || strings.Contains(line, "lifecycle.policy.defaults_installed") {
					continue
				}
				// Rejecting a missing workspace before constructing a local
				// subject-artifact path is a fail-closed guard, not a fallback.
				if filepath.ToSlash(relative) == "runtime/infrastructure/lifecycleartifact/filesystem/subject_store.go" && strings.Contains(line, `strings.TrimSpace(workspaceID) == ""`) {
					continue
				}
				// Scanner error handling can place WorkspaceID and `err != nil`
				// on one line; that is not an empty-workspace comparison.
				if strings.Contains(line, "row.Scan(&value.ID, &value.WorkspaceID") {
					continue
				}
				// Data-migration plans explicitly assert that fallback is forbidden;
				// those contract field names are not fallback implementations.
				if strings.Contains(line, "ForbidsWorkspaceFallback") || strings.Contains(line, "forbids_workspace_fallback") {
					continue
				}
				// Data-migration copy/verification rejects missing workspace values;
				// the line is a guard, not a fallback implementation.
				if strings.Contains(line, "strings.TrimSpace(fmt.Sprint") && strings.Contains(line, `== ""`) {
					continue
				}
				// Direct-authoring and durable-store guards reject missing or
				// mismatched workspace identities; they never substitute a tenant.
				if strings.Contains(line, "workspaceID != identitymodel.InstallationWorkspaceID") ||
					(strings.Contains(line, "strings.TrimSpace(initiator.WorkspaceID)") && strings.Contains(line, "strings.TrimSpace(resolved.WorkspaceID)")) ||
					(strings.Contains(line, "!principal.Known") && strings.Contains(line, "workspaceID")) ||
					(strings.Contains(line, "!principal.Known") && strings.Contains(line, "principal.WorkspaceID")) ||
					(strings.Contains(line, "artifact.WorkspaceID") && strings.Contains(line, "principal.WorkspaceID")) ||
					(strings.Contains(line, "strings.TrimSpace(artifact.WorkspaceID)") && strings.Contains(line, `== ""`)) ||
					(strings.Contains(line, "strings.TrimSpace(value.WorkspaceID)") && strings.Contains(line, "workspaceID")) ||
					(strings.Contains(line, "strings.TrimSpace(request.WorkspaceID)") && strings.Contains(line, `== ""`)) ||
					(strings.Contains(line, "strings.TrimSpace(value.WorkspaceID)") && strings.Contains(line, `== ""`)) ||
					(strings.Contains(line, "strings.TrimSpace(intent.WorkspaceID)") && strings.Contains(line, `== ""`)) ||
					(strings.Contains(line, "strings.TrimSpace(scope.WorkspaceID)") && strings.Contains(line, `== ""`)) ||
					strings.Contains(line, "workspaceID = strings.TrimSpace(workspaceID)") {
					continue
				}
				if workspaceFallbackLine.MatchString(line) {
					actual[filepath.ToSlash(relative)]++
				}
			}
			return scanner.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	total := 0
	for path, count := range reviewedWorkspaceFallbackBaseline {
		total += count
		if actual[path] != count {
			t.Errorf("workspace fallback baseline for %s must be tightened after removals and must never grow: want %d current matches, got %d", path, count, actual[path])
		}
		if !strings.Contains(document, fmt.Sprintf("| %d | `%s` |", count, path)) {
			t.Errorf("workspace fallback inventory document does not match baseline for %s", path)
		}
	}
	for path, count := range actual {
		if count > 0 {
			if _, reviewed := reviewedWorkspaceFallbackBaseline[path]; !reviewed {
				t.Errorf("new workspace fallback in %s is forbidden; found %d matching lines", path, count)
			}
		}
	}
	if total > 104 {
		t.Fatalf("workspace fallback baseline may only decrease from 104, got %d", total)
	}
}
