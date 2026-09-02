package operations

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type OperationsControlRequest struct {
	Kind             operationsmodel.OperationsControlKind `json:"kind"`
	Owner            string                                `json:"owner"`
	Active           bool                                  `json:"active"`
	Reason           string                                `json:"reason"`
	Reference        string                                `json:"reference,omitempty"`
	ExpectedRevision int64                                 `json:"expected_revision"`
}

type OperationsControlResult struct {
	Control       operationsmodel.OperationsControl        `json:"control"`
	DrainSnapshot *operationsmodel.OperationsLeaseSnapshot `json:"drain_snapshot,omitempty"`
	Receipt       operationsmodel.OperationsReceipt        `json:"receipt"`
}

type OperationsControlApplicationService struct {
	repository       operationsrepository.OperationsControlRepository
	leases           operationsrepository.OperationsLeaseRepository
	operations       *OperationsApplicationService
	now              func() time.Time
	drainSettleDelay time.Duration
	drainWaitTimeout time.Duration
}

func NewOperationsControlApplicationService(repository operationsrepository.OperationsControlRepository, operations *OperationsApplicationService, leases operationsrepository.OperationsLeaseRepository, now func() time.Time) *OperationsControlApplicationService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &OperationsControlApplicationService{repository: repository, operations: operations, leases: leases, now: now}
}

func (s *OperationsControlApplicationService) Set(ctx context.Context, request OperationsControlRequest, key string, principal principalmodel.Principal) (OperationsControlResult, error) {
	if s == nil || s.repository == nil || s.operations == nil {
		return OperationsControlResult{}, apperror.New(apperror.KindInternal, "backend.operations.control_unavailable", nil, nil)
	}
	request.Owner, request.Reason, request.Reference = strings.TrimSpace(request.Owner), strings.TrimSpace(request.Reason), strings.TrimSpace(request.Reference)
	if request.Owner == "" || request.Reason == "" || request.ExpectedRevision < 0 || !operationsControlKindValid(request.Kind) {
		return OperationsControlResult{}, apperror.New(apperror.KindBadRequest, "backend.operations.control_invalid", nil, nil)
	}
	operationKind, resourceType := operationsControlOperation(request.Kind, request.Active)
	receipt, decision, err := s.operations.SubmitSystem(ctx, OperationsSubmitRequest{
		Kind: operationKind, ResourceType: resourceType, ResourceID: request.Owner,
		Reason: request.Reason, Reference: request.Reference,
		Payload: map[string]any{"active": request.Active, "expected_revision": request.ExpectedRevision},
	}, key, operationsmodel.OperationsSystemPurposeRuntimeControl, principal)
	if err != nil {
		return OperationsControlResult{}, err
	}
	if decision == operationsmodel.OperationsSubmissionReplay && receipt.Command.Status == operationsmodel.OperationsStatusSucceeded {
		control, found, readErr := s.repository.GetOperationsControl(ctx, operationsmodel.OperationsSystemPurposeRuntimeControl, request.Kind, request.Owner)
		if readErr != nil || !found {
			return OperationsControlResult{}, apperror.New(apperror.KindInternal, "backend.operations.control_read_failed", readErr, nil)
		}
		return OperationsControlResult{Control: control, Receipt: receipt}, nil
	}
	systemScope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "apply durable Runtime operation control")
	receipt, err = s.operations.Start(ctx, receipt.Command.ID, receipt.Command.Scope, systemScope)
	if err != nil {
		return OperationsControlResult{}, err
	}
	state := operationsmodel.OperationsControlInactive
	if request.Active {
		state = operationsmodel.OperationsControlActive
	}
	control := operationsmodel.OperationsControl{
		SystemPurpose: operationsmodel.OperationsSystemPurposeRuntimeControl, Kind: request.Kind, Owner: request.Owner,
		State: state, Reason: request.Reason, Reference: request.Reference, UpdatedBy: principal.UserID,
		Revision: request.ExpectedRevision + 1, UpdatedAt: s.now().UTC(),
	}
	changed, err := s.repository.PutOperationsControl(ctx, control, request.ExpectedRevision)
	if err != nil || !changed {
		receipt.Command.Status, receipt.FailureClass, receipt.ErrorCode = operationsmodel.OperationsStatusFailed, operationsmodel.OperationsFailureManualIntervention, "backend.operations.control_revision_conflict"
		receipt.NextAction = "read the current control revision and retry with a new idempotency key"
		_, _ = s.operations.Finish(ctx, receipt, systemScope)
		return OperationsControlResult{}, apperror.New(apperror.KindConflict, "backend.operations.control_revision_conflict", err, nil)
	}
	result := OperationsControlResult{Control: control}
	if control.Kind == operationsmodel.OperationsControlInstanceDrain && control.Active() && s.leases != nil {
		snapshot, snapshotErr := s.waitForInstanceDrain(ctx, control.Owner)
		if snapshotErr != nil {
			receipt.Command.Status, receipt.FailureClass, receipt.ErrorCode = operationsmodel.OperationsStatusFailed, operationsmodel.OperationsFailureRetryable, "backend.operations.drain_snapshot_failed"
			receipt.NextAction = "keep the durable drain active and retry diagnostics before force release"
			_, _ = s.operations.Finish(ctx, receipt, systemScope)
			return OperationsControlResult{}, apperror.New(apperror.KindInternal, "backend.operations.drain_snapshot_failed", snapshotErr, nil)
		}
		result.DrainSnapshot = &snapshot
	}
	resultJSON, _ := json.Marshal(struct {
		Control       operationsmodel.OperationsControl        `json:"control"`
		DrainSnapshot *operationsmodel.OperationsLeaseSnapshot `json:"drain_snapshot,omitempty"`
	}{Control: control, DrainSnapshot: result.DrainSnapshot})
	receipt.Command.Status, receipt.Result = operationsmodel.OperationsStatusSucceeded, resultJSON
	receipt.RelatedIDs = []string{control.Owner}
	receipt.NextAction = operationsControlNextAction(control, result.DrainSnapshot)
	receipt, err = s.operations.Finish(ctx, receipt, systemScope)
	if err != nil {
		return OperationsControlResult{}, err
	}
	result.Receipt = receipt
	return result, nil
}

func (s *OperationsControlApplicationService) List(ctx context.Context, kind operationsmodel.OperationsControlKind, limit int, principal principalmodel.Principal) ([]operationsmodel.OperationsControl, error) {
	if err := operationsAuthorize(principal, operationscontract.ActionListControls); err != nil {
		return nil, err
	}
	return s.repository.ListOperationsControls(ctx, operationsmodel.OperationsSystemPurposeRuntimeControl, kind, limit)
}

func operationsControlKindValid(kind operationsmodel.OperationsControlKind) bool {
	return kind == operationsmodel.OperationsControlMaintenance || kind == operationsmodel.OperationsControlWorkerPause || kind == operationsmodel.OperationsControlInstanceDrain
}

func operationsControlOperation(kind operationsmodel.OperationsControlKind, active bool) (string, string) {
	switch kind {
	case operationsmodel.OperationsControlMaintenance:
		if active {
			return "runtime.maintenance.enable", "runtime"
		}
		return "runtime.maintenance.disable", "runtime"
	case operationsmodel.OperationsControlWorkerPause:
		if active {
			return "worker.owner.pause", "worker_owner"
		}
		return "worker.owner.resume", "worker_owner"
	default:
		if active {
			return "runtime.instance.drain", "runtime_instance"
		}
		return "runtime.instance.undrain", "runtime_instance"
	}
}

func (s *OperationsControlApplicationService) waitForInstanceDrain(ctx context.Context, instanceID string) (operationsmodel.OperationsLeaseSnapshot, error) {
	settleDelay := s.drainSettleDelay
	if settleDelay <= 0 {
		settleDelay = 1100 * time.Millisecond
	}
	settle := time.NewTimer(settleDelay)
	defer settle.Stop()
	select {
	case <-ctx.Done():
		return operationsmodel.OperationsLeaseSnapshot{}, ctx.Err()
	case <-settle.C:
	}
	waitTimeout := s.drainWaitTimeout
	if waitTimeout <= 0 {
		waitTimeout = 5 * time.Second
	}
	deadline := time.Now().Add(waitTimeout)
	for {
		snapshot, err := s.leases.OperationsLeaseSnapshot(ctx, instanceID, s.now().UTC())
		if err != nil || snapshot.Live == 0 || time.Now().After(deadline) {
			return snapshot, err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return snapshot, ctx.Err()
		case <-timer.C:
		}
	}
}

func operationsControlNextAction(control operationsmodel.OperationsControl, drain *operationsmodel.OperationsLeaseSnapshot) string {
	if drain != nil && drain.Live > 0 {
		return "inspect the reported live leases; force release only after expiry or independent stuck verification"
	}
	if control.Active() {
		return "observe all Runtime instances until the requested control is applied"
	}
	return "verify readiness and owner processing have recovered"
}
