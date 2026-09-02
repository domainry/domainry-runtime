package operations

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type OperationsDirectAuthoringProjection func(context.Context, string, principalmodel.Principal) (capabilitycontract.CapabilityAuthoringSuccessProjection, error)

type OperationsDirectAuthoringSuccessResult struct {
	Resource            any                                                      `json:"resource"`
	ResourceHash        string                                                   `json:"resource_hash"`
	SnapshotHash        string                                                   `json:"snapshot_hash"`
	AvailableSuccessors []capabilitycontract.CapabilityAuthoringSuccessorSummary `json:"available_successors"`
}

func (s *OperationsApplicationService) UseDirectAuthoringProjection(projection OperationsDirectAuthoringProjection) {
	if s != nil {
		s.directAuthoringProjection = projection
	}
}

const operationsDiagnosticSectionCount = 6

func operationsDiagnosticSectionAllowed(section string) bool {
	switch section {
	case "schema_migration", "db_pool", "worker_lease", "queue_lag", "dlq", "backup_age":
		return true
	default:
		return false
	}
}

type OperationsDiagnosticsCommand struct {
	Sections  []string `json:"sections"`
	Page      int      `json:"page,omitempty"`
	PageSize  int      `json:"page_size,omitempty"`
	Reason    string   `json:"reason"`
	Reference string   `json:"reference,omitempty"`
}

type OperationsDiagnosticsResult struct {
	Snapshot operationsmodel.OperationsDiagnosticsSnapshot `json:"snapshot"`
	Receipt  operationsmodel.OperationsReceipt             `json:"receipt"`
}

func (s *OperationsApplicationService) RegisterDiagnostics(repository operationsrepository.OperationsDiagnosticsRepository, instanceID string) error {
	if s == nil || repository == nil || strings.TrimSpace(instanceID) == "" {
		return apperror.New(apperror.KindBadRequest, "backend.operations.diagnostics_registration_invalid", nil, nil)
	}
	s.diagnostics, s.instanceID = repository, strings.TrimSpace(instanceID)
	return nil
}

func (s *OperationsApplicationService) CaptureDiagnostics(ctx context.Context, command OperationsDiagnosticsCommand, key string, principal principalmodel.Principal) (OperationsDiagnosticsResult, error) {
	if s == nil || s.diagnostics == nil {
		return OperationsDiagnosticsResult{}, apperror.New(apperror.KindInternal, "backend.operations.diagnostics_unavailable", nil, nil)
	}
	sections := []string{}
	seen := map[string]struct{}{}
	for _, section := range command.Sections {
		section = strings.TrimSpace(section)
		if !operationsDiagnosticSectionAllowed(section) {
			return OperationsDiagnosticsResult{}, apperror.New(apperror.KindBadRequest, "backend.operations.diagnostics_section_invalid", nil, nil)
		}
		if _, duplicate := seen[section]; !duplicate {
			seen[section] = struct{}{}
			sections = append(sections, section)
		}
	}
	if len(sections) == 0 {
		return OperationsDiagnosticsResult{}, apperror.New(apperror.KindBadRequest, "backend.operations.diagnostics_section_invalid", nil, nil)
	}
	if command.Page <= 0 {
		command.Page = 1
	}
	if command.PageSize <= 0 {
		command.PageSize = 20
	}
	if command.PageSize > 50 {
		return OperationsDiagnosticsResult{}, apperror.New(apperror.KindBadRequest, "backend.operations.diagnostics_cost_exceeded", nil, nil)
	}
	command.Sections = sections
	receipt, decision, err := s.SubmitSystem(ctx, OperationsSubmitRequest{Kind: "diagnostics.snapshot", ResourceType: "runtime", ResourceID: s.instanceID, Reason: command.Reason, Reference: command.Reference, Payload: command}, key, operationsmodel.OperationsSystemPurposeRuntimeControl, principal)
	if err != nil {
		return OperationsDiagnosticsResult{}, err
	}
	if decision == operationsmodel.OperationsSubmissionReplay && receipt.Command.Status == operationsmodel.OperationsStatusSucceeded {
		var snapshot operationsmodel.OperationsDiagnosticsSnapshot
		if json.Unmarshal(receipt.Result, &snapshot) != nil {
			return OperationsDiagnosticsResult{}, apperror.New(apperror.KindInternal, "backend.operations.diagnostics_receipt_invalid", nil, nil)
		}
		return OperationsDiagnosticsResult{Snapshot: snapshot, Receipt: receipt}, nil
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "capture bounded Runtime diagnostics")
	receipt, err = s.Start(ctx, receipt.Command.ID, receipt.Command.Scope, scope)
	if err != nil {
		return OperationsDiagnosticsResult{}, err
	}
	snapshot, snapshotErr := s.diagnostics.OperationsDiagnosticsSnapshot(ctx, operationsmodel.OperationsDiagnosticsRequest{WorkspaceID: principal.WorkspaceID, InstanceID: s.instanceID, Sections: sections, Page: command.Page, PageSize: command.PageSize, Now: s.now().UTC()})
	if snapshotErr != nil {
		receipt.Command.Status, receipt.FailureClass, receipt.ErrorCode, receipt.NextAction = operationsmodel.OperationsStatusFailed, operationsmodel.OperationsFailureRetryable, "backend.operations.diagnostics_capture_failed", "check database readiness and retry the bounded snapshot"
		_, _ = s.Finish(ctx, receipt, scope)
		return OperationsDiagnosticsResult{}, apperror.New(apperror.KindInternal, "backend.operations.diagnostics_capture_failed", snapshotErr, nil)
	}
	encoded, _ := json.Marshal(snapshot)
	receipt.Command.Status, receipt.Result = operationsmodel.OperationsStatusSucceeded, encoded
	receipt.Correlation, receipt.RelatedIDs = receipt.Command.ID, []string{s.instanceID, principal.WorkspaceID}
	receipt.Evidence, receipt.NextAction = []string{"/operations/runbooks/diagnostics"}, "follow each degraded section runbook, then capture a new snapshot to verify recovery"
	receipt, err = s.Finish(ctx, receipt, scope)
	if err != nil {
		return OperationsDiagnosticsResult{}, err
	}
	return OperationsDiagnosticsResult{Snapshot: snapshot, Receipt: receipt}, nil
}
