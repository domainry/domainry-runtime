package operations

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type OperationsLeaseReleaseCommand struct {
	Owner                string `json:"owner"`
	ResourceID           string `json:"resource_id"`
	ExpectedLeaseOwner   string `json:"expected_lease_owner"`
	ExpectedFencingToken int64  `json:"expected_fencing_token"`
	VerifiedStuck        bool   `json:"verified_stuck"`
	VerificationEvidence string `json:"verification_evidence,omitempty"`
	Reason               string `json:"reason"`
	Reference            string `json:"reference,omitempty"`
}

type OperationsLeaseReleaseReceipt struct {
	Release operationsmodel.OperationsLeaseReleaseResult `json:"release"`
	Receipt operationsmodel.OperationsReceipt            `json:"receipt"`
}

type OperationsLeaseApplicationService struct {
	repository operationsrepository.OperationsLeaseRepository
	operations *OperationsApplicationService
	now        func() time.Time
}

func NewOperationsLeaseApplicationService(repository operationsrepository.OperationsLeaseRepository, operations *OperationsApplicationService, now func() time.Time) *OperationsLeaseApplicationService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &OperationsLeaseApplicationService{repository: repository, operations: operations, now: now}
}

func (s *OperationsLeaseApplicationService) ForceRelease(ctx context.Context, command OperationsLeaseReleaseCommand, key string, principal principalmodel.Principal) (OperationsLeaseReleaseReceipt, error) {
	if s == nil || s.repository == nil || s.operations == nil {
		return OperationsLeaseReleaseReceipt{}, apperror.New(apperror.KindInternal, "backend.operations.lease_control_unavailable", nil, nil)
	}
	receipt, decision, err := s.operations.SubmitSystem(ctx, OperationsSubmitRequest{
		Kind: "worker.lease.force_release", Permission: "workspace.admin", ResourceType: "worker_lease", ResourceID: strings.TrimSpace(command.Owner) + ":" + strings.TrimSpace(command.ResourceID), Reason: command.Reason, Reference: command.Reference,
		Payload: map[string]any{"expected_lease_owner": command.ExpectedLeaseOwner, "expected_fencing_token": command.ExpectedFencingToken, "verified_stuck": command.VerifiedStuck, "verification_evidence": command.VerificationEvidence},
	}, key, operationsmodel.OperationsSystemPurposeRuntimeControl, principal)
	if err != nil {
		return OperationsLeaseReleaseReceipt{}, err
	}
	if decision == operationsmodel.OperationsSubmissionReplay && receipt.Command.Status == operationsmodel.OperationsStatusSucceeded {
		var release operationsmodel.OperationsLeaseReleaseResult
		if err := json.Unmarshal(receipt.Result, &release); err != nil {
			return OperationsLeaseReleaseReceipt{}, apperror.New(apperror.KindInternal, "backend.operations.lease_receipt_invalid", err, nil)
		}
		return OperationsLeaseReleaseReceipt{Release: release, Receipt: receipt}, nil
	}
	systemScope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "force release verified Runtime lease")
	receipt, err = s.operations.Start(ctx, receipt.Command.ID, receipt.Command.Scope, systemScope)
	if err != nil {
		return OperationsLeaseReleaseReceipt{}, err
	}
	release, changed, releaseErr := s.repository.ForceReleaseOperationsLease(ctx, operationsmodel.OperationsLeaseReleaseRequest{
		Owner: strings.TrimSpace(command.Owner), WorkspaceID: principal.WorkspaceID, ResourceID: strings.TrimSpace(command.ResourceID), ExpectedLeaseOwner: strings.TrimSpace(command.ExpectedLeaseOwner), ExpectedFencingToken: command.ExpectedFencingToken,
		VerifiedStuck: command.VerifiedStuck, VerificationEvidence: strings.TrimSpace(command.VerificationEvidence), Now: s.now().UTC(),
	})
	if releaseErr != nil || !changed {
		receipt.Command.Status, receipt.FailureClass, receipt.ErrorCode = operationsmodel.OperationsStatusFailed, operationsmodel.OperationsFailureTerminal, "backend.operations.lease_release_precondition_failed"
		receipt.NextAction = "inspect current owner, fencing token, expiry, and stuck evidence before retrying with a new key"
		_, _ = s.operations.Finish(ctx, receipt, systemScope)
		return OperationsLeaseReleaseReceipt{}, apperror.New(apperror.KindConflict, "backend.operations.lease_release_precondition_failed", releaseErr, nil)
	}
	resultJSON, _ := json.Marshal(release)
	receipt.Command.Status, receipt.Result = operationsmodel.OperationsStatusSucceeded, resultJSON
	receipt.RelatedIDs = []string{release.Owner, release.ResourceID, release.PreviousLeaseOwner}
	receipt.NextAction = "allow the owner reclaim policy to allocate a new fencing token; never resume the released owner"
	receipt, err = s.operations.Finish(ctx, receipt, systemScope)
	if err != nil {
		return OperationsLeaseReleaseReceipt{}, err
	}
	return OperationsLeaseReleaseReceipt{Release: release, Receipt: receipt}, nil
}
