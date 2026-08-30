package boundary_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeStateInventoryCoversKnownMutableState(t *testing.T) {
	root := runtimeRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "..", "docs", "architecture", "runtime-state-inventory.md"))
	if err != nil {
		t.Fatal(err)
	}
	document := string(raw)
	fields := []string{
		"business-records",
		"_audit_events",
		"_workflow_process_instances",
		"_automation_rule_executions",
		"_publication_outbox",
		"_integration_webhook_nonces",
		"metadata.current_projection.v1",
		"agentDialogSessions",
		"agentDialogProposals",
		"agentReportQueryRuns",
		"agentReportExportAudits",
		"agentReportDownloadTasks",
		"dictionaryCache",
		"_operation_requests",
		"_operation_controls",
		"_operation_break_glass_grants",
	}
	for _, field := range fields {
		if !strings.Contains(document, "`"+field+"`") {
			t.Errorf("runtime mutable state %s is missing from docs/architecture/runtime-state-inventory.md", field)
		}
	}
	for _, heading := range []string{"Authoritative owner", "Workspace scope", "Sensitivity / region", "Source of truth", "Policy key", "Retention class", "Retention / minimum", "Archive + purge / legal hold", "Backup behavior", "Export / erase", "Loss allowed", "Consistency", "Capacity policy", "Multi-instance target"} {
		if !strings.Contains(document, heading) {
			t.Errorf("runtime state inventory is missing required classification %q", heading)
		}
	}
	for _, class := range []string{"product_retention", "legal_audit_retention", "technical_ttl", "user_requested_erase"} {
		if !strings.Contains(document, class) {
			t.Errorf("runtime state inventory is missing retention class %q", class)
		}
	}
	for _, policyKey := range []string{"record.object.default.v1", "audit.evidence.v1", "workflow.execution.v1", "runtime.publication_handoff.v1", "report.export.v1"} {
		if !strings.Contains(document, "`"+policyKey+"`") {
			t.Errorf("runtime state inventory is missing policy key %q", policyKey)
		}
	}
}
