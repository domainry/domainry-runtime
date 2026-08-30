package boundary_test

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var workspaceSQLTablePattern = regexp.MustCompile(`TableIdentifier\("([^"]+)"\)`)

var reviewedWorkspaceSQLTables = map[string]bool{
	"_audit_events": true, "_workflow_executions": true,
	"automation_instruction_executions": true, "automation_rule_executions": true, "business_action_executions": true,
	"business_audit_export_artifacts": true,
	"business_change_plan_operations": true, "business_localized_text": true, "business_record_localized_value": true, "frontend_capability_manifests": true,
	"integration_api_keys": true, "integration_connections": true, "integration_credential_refresh_leases": true,
	"integration_event_mapping_intents": true, "integration_events": true, "integration_external_identities": true,
	"integration_invocations": true, "integration_outbox_messages": true, "integration_secret_materials": true, "integration_secrets": true,
	"connector_provider_states":  true,
	"integration_webhook_nonces": true, "integration_webhook_subscriptions": true, "notification_delivery_reservations": true,
	"lifecycle_archive_entries": true, "lifecycle_audit_evidence": true, "lifecycle_cleanup_jobs": true,
	"lifecycle_deletion_registry": true, "lifecycle_external_erasures": true, "lifecycle_legal_holds": true,
	"lifecycle_policy_versions": true, "lifecycle_subject_requests": true, "lifecycle_file_artifacts": true,
	"notification_recipient_preferences": true, "record_mutation_executions": true,
	"transaction_boundary_intents": true, "workflow_execution_receipts": true, "workflow_node_instances": true,
	"workflow_process_events": true, "workflow_process_instances": true, "workflow_tasks": true,
}

// TestWorkspaceSQLReviewBaseline blocks every new literal SQL statement that
// touches a workspace-owned table without a workspace_id predicate. Existing
// system-worker or legacy statements remain exact reviewed hashes until they
// are migrated; changing one requires an explicit baseline adjudication.
func TestWorkspaceSQLReviewBaseline(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	baselinePath := filepath.Join(repositoryRoot, "docs", "architecture", "runtime-workspace-sql-review-baseline.txt")
	expectedRaw, err := os.ReadFile(baselinePath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	expected := nonBlankSortedLines(string(expectedRaw))
	actual := workspaceSQLReviewFindings(t, repositoryRoot)
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("workspace SQL review baseline changed; migrate the SQL or explicitly review this exact list:\n%s", strings.Join(actual, "\n"))
	}
}

func workspaceSQLReviewFindings(t *testing.T, repositoryRoot string) []string {
	t.Helper()
	root := filepath.Join(repositoryRoot, "runtime", "infrastructure", "persistence", "database")
	findingSet := map[string]struct{}{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") || strings.Contains(filepath.ToSlash(path), "/schema/") {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		relative, _ := filepath.Rel(repositoryRoot, path)
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.Contains(line, " WHERE ") || strings.Contains(line, `Identifier("workspace_id")`) {
				continue
			}
			for _, match := range workspaceSQLTablePattern.FindAllStringSubmatch(line, -1) {
				table := match[1]
				if !reviewedWorkspaceSQLTables[table] {
					continue
				}
				digest := sha256.Sum256([]byte(line))
				findingSet[fmt.Sprintf("%s|%s|%s", filepath.ToSlash(relative), table, hex.EncodeToString(digest[:8]))] = struct{}{}
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	findings := make([]string, 0, len(findingSet))
	for finding := range findingSet {
		findings = append(findings, finding)
	}
	sort.Strings(findings)
	return findings
}

func nonBlankSortedLines(raw string) []string {
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	sort.Strings(lines)
	return lines
}
