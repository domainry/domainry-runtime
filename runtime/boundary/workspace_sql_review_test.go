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
	"_automation_instruction_executions": true, "_automation_rule_executions": true, "_action_executions": true,
	"_audit_export_artifacts": true, "_record_localized_values": true,
	"_publication_outbox":                 true,
	"_notification_delivery_reservations": true,
	"_lifecycle_archive_entries":          true, "_lifecycle_audit_evidence": true, "_lifecycle_cleanup_jobs": true,
	"_lifecycle_deletion_registry": true, "_lifecycle_external_erasure_requests": true, "_lifecycle_legal_holds": true,
	"_lifecycle_policy_versions": true, "_lifecycle_subject_requests": true, "_lifecycle_file_artifacts": true,
	"_notification_recipient_preferences": true, "_record_mutation_executions": true,
	"_transaction_boundary_intents": true, "_workflow_execution_receipts": true, "_workflow_node_instances": true,
	"_workflow_process_events": true, "_workflow_process_instances": true, "_workflow_tasks": true,
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
