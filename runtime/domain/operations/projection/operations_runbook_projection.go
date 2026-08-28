package projection

import (
	"strings"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func OperationsRunbookForError(code string) (operationsmodel.OperationsRunbookLink, bool) {
	code = strings.TrimSpace(code)
	if !strings.HasPrefix(code, "backend.operations.") {
		return operationsmodel.OperationsRunbookLink{}, false
	}
	category := "control-plane"
	actions := []string{"inspect the operation receipt and related IDs", "correct the reported precondition", "retry with a new idempotency key only when instructed"}
	switch {
	case strings.Contains(code, "database_retirement"):
		category, actions = "database-retirement", []string{"keep maintenance and drain active", "verify backup, restore drill, approval, and quarantine evidence", "resume only after a new preview passes"}
	case strings.Contains(code, "bulk_"):
		category, actions = "bounded-bulk", []string{"discard the stale or mismatched confirmation", "capture a new bounded dry-run", "review every per-item eligibility result before apply"}
	case strings.Contains(code, "dead_letter"):
		category, actions = "dead-letter", []string{"inspect the owner item and evidence reference", "verify current dependency readiness", "use only an action allowed by the owner"}
	case strings.Contains(code, "lease") || strings.Contains(code, "drain"):
		category, actions = "lease-and-drain", []string{"inspect live and expired lease owners", "wait for normal lease expiry", "force release only with observed owner, fencing token, and independent stuck evidence"}
	case strings.Contains(code, "diagnostics"):
		category, actions = "diagnostics", []string{"request only registered bounded sections", "follow the degraded section runbook", "capture a new snapshot after recovery"}
	case strings.Contains(code, "break_glass"):
		category, actions = "break-glass", []string{"verify the incident reference and two independent approvers", "confirm the alert event reached the configured target", "revoke the grant immediately when incident work ends and verify expiry"}
	case strings.Contains(code, "control"):
		category, actions = "maintenance-and-worker-control", []string{"read the current durable control revision", "verify readiness dependencies", "retry with the observed revision and a new key"}
	}
	return operationsmodel.OperationsRunbookLink{ErrorCode: code, Category: category, URL: "/operations/runbooks/" + category + "?error_code=" + code, NextActions: actions}, true
}
