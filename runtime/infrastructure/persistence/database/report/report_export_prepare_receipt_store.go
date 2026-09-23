package report

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

const (
	reportExportPrepareReceiptLeaseTTL = 5 * time.Minute
	reportExportPrepareOwner           = "report"
	reportExportPrepareKind            = "report.export.prepare"
)

type ReportExportPrepareReceiptStore struct {
	store *database.RuntimeStore
}

func NewReportExportPrepareReceiptStore(store *database.RuntimeStore) *ReportExportPrepareReceiptStore {
	return &ReportExportPrepareReceiptStore{store: store}
}

func (s *ReportExportPrepareReceiptStore) GetReportExportPrepareReceipt(ctx context.Context, workspaceID, receiptID string) (reportmodel.ReportExportPrepareReceipt, bool, error) {
	if s == nil || s.store == nil || s.store.DB() == nil {
		return reportmodel.ReportExportPrepareReceipt{}, false, fmt.Errorf("report export prepare receipt store is unavailable")
	}
	workspaceID, receiptID = strings.TrimSpace(workspaceID), strings.TrimSpace(receiptID)
	if workspaceID == "" || receiptID == "" {
		return reportmodel.ReportExportPrepareReceipt{}, false, fmt.Errorf("report export prepare receipt lookup is incomplete")
	}
	return s.findReportExportPrepareByID(ctx, workspaceID, receiptID)
}

func (s *ReportExportPrepareReceiptStore) TryBeginReportExportPrepare(ctx context.Context, request reportmodel.ReportExportPrepareClaimRequest) (reportmodel.ReportExportPrepareClaimResult, error) {
	if s == nil || s.store == nil || s.store.DB() == nil {
		return reportmodel.ReportExportPrepareClaimResult{}, fmt.Errorf("report export prepare receipt store is unavailable")
	}
	for attempt := 0; attempt < 50; attempt++ {
		claim, err := s.tryBeginReportExportPrepareOnce(ctx, request)
		if err == nil || !s.store.IsTransientError(err) {
			return claim, err
		}
		if err := reportExportPrepareClaimBackoff(ctx, attempt); err != nil {
			return reportmodel.ReportExportPrepareClaimResult{}, err
		}
	}
	return reportmodel.ReportExportPrepareClaimResult{}, fmt.Errorf("claim report export prepare: database remained busy after retry")
}

func (s *ReportExportPrepareReceiptStore) tryBeginReportExportPrepareOnce(ctx context.Context, request reportmodel.ReportExportPrepareClaimRequest) (reportmodel.ReportExportPrepareClaimResult, error) {
	receipt, now, leaseTTL, err := normalizeReportExportPrepareClaim(request)
	if err != nil {
		return reportmodel.ReportExportPrepareClaimResult{}, err
	}
	receipt.OperationID = reportExportPrepareOperationID(receipt)
	receipt.ID = reportExportPrepareReceiptID(receipt)
	receipt.RequestFingerprint = strings.TrimSpace(request.RequestFingerprint)
	receipt.Status = string(idempotency.StatusProcessing)
	receipt.LeaseOwner = strings.TrimSpace(request.LeaseOwner)
	receipt.LeaseExpiresAt = now.Add(leaseTTL).Format(time.RFC3339Nano)
	receipt.FencingToken = 1
	receipt.CreatedAt, receipt.UpdatedAt = now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)
	tx, beginErr := s.store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if beginErr != nil {
		return reportmodel.ReportExportPrepareClaimResult{}, beginErr
	}
	if guardErr := s.store.GuardSubjectEvidenceWrite(ctx, tx, receipt.WorkspaceID, sharedoperation.TableName,
		[]string{"id", "owner", "requested_by"}, []any{receipt.ID, reportExportPrepareOwner, receipt.RequesterUserID}); guardErr != nil {
		_ = tx.Rollback()
		return reportmodel.ReportExportPrepareClaimResult{}, guardErr
	}
	inserted, insertErr := s.operationStore().InsertRecord(sharedoperation.WithExecutor(ctx, tx), reportExportPrepareRecord(receipt))
	if insertErr == nil && inserted {
		if commitErr := tx.Commit(); commitErr != nil {
			return reportmodel.ReportExportPrepareClaimResult{}, commitErr
		}
		s.store.ObserveIdempotency(ctx, receipt.WorkspaceID, receipt.UseCase, idempotency.OutcomeAcquired)
		return reportmodel.ReportExportPrepareClaimResult{Decision: idempotency.DecisionAcquired, Receipt: receipt}, nil
	} else {
		_ = tx.Rollback()
		current, found, findErr := s.findReportExportPrepareByOperation(ctx, receipt.WorkspaceID, receipt.OperationID)
		if findErr != nil {
			return reportmodel.ReportExportPrepareClaimResult{}, findErr
		}
		if !found {
			return reportmodel.ReportExportPrepareClaimResult{}, database.MutationConstraintError(insertErr, "report_export_prepare_receipt", receipt.ID, mutation.MutationConflictIdempotency)
		}
		if !sameReportExportPrepareOperation(current, receipt) {
			return reportmodel.ReportExportPrepareClaimResult{}, fmt.Errorf("report export prepare operation identity collision")
		}
		if reportExportPrepareOrphanCanBeAdopted(current, now) {
			adopted, won, adoptErr := s.adoptReportExportPrepareOrphan(ctx, receipt, current, now, leaseTTL)
			if adoptErr != nil {
				return reportmodel.ReportExportPrepareClaimResult{}, adoptErr
			}
			if won {
				s.store.ObserveIdempotency(ctx, receipt.WorkspaceID, receipt.UseCase, idempotency.OutcomeReclaimed)
				return reportmodel.ReportExportPrepareClaimResult{Decision: idempotency.DecisionAcquired, Receipt: adopted}, nil
			}
			current = adopted
		}
		if current.RequesterUserID != receipt.RequesterUserID || current.CallerKey != receipt.CallerKey {
			s.store.ObserveIdempotency(ctx, receipt.WorkspaceID, receipt.UseCase, idempotency.OutcomeConflict)
			return reportmodel.ReportExportPrepareClaimResult{Decision: idempotency.DecisionFingerprintConflict, Receipt: current, AuditOperationConflict: true}, nil
		}
		decision := idempotency.Classify(idempotency.ReceiptState{
			Status: idempotency.Status(current.Status), Fingerprint: current.RequestFingerprint, Lease: reportExportPrepareLease(current),
		}, receipt.RequestFingerprint, now)
		if decision != idempotency.DecisionAcquired {
			s.store.ObserveIdempotency(ctx, receipt.WorkspaceID, receipt.UseCase, idempotency.OutcomeForDecision(decision, false))
			return reportmodel.ReportExportPrepareClaimResult{Decision: decision, Receipt: current}, nil
		}
		return s.reclaimReportExportPrepare(ctx, receipt, now, leaseTTL)
	}
}

func (s *ReportExportPrepareReceiptStore) adoptReportExportPrepareOrphan(ctx context.Context, requested, current reportmodel.ReportExportPrepareReceipt, now time.Time, leaseTTL time.Duration) (reportmodel.ReportExportPrepareReceipt, bool, error) {
	nowValue, leaseExpiresAt := now.Format(time.RFC3339Nano), now.Add(leaseTTL).Format(time.RFC3339Nano)
	status, empty := string(idempotency.StatusProcessing), ""
	metadataJSON := json.RawMessage(reportExportPrepareMetadataJSON(requested))
	changed, err := s.operationStore().PatchRecord(ctx, sharedoperation.RecordFilter{
		WorkspaceID: requested.WorkspaceID, ID: current.ID, Owner: reportExportPrepareOwner, Kind: reportExportPrepareKind,
		ActionKey: requested.UseCase, IdempotencyKey: requested.OperationID, Reference: requested.AuditID,
		ResourceType: "report", ResourceID: requested.ReportKey, Status: status, LeaseExpiresAtOrBefore: nowValue,
		ResultJSON: json.RawMessage(reportExportPrepareResultJSON(current)),
	}, sharedoperation.RecordChanges{
		ID: &requested.ID, RequestedBy: &requested.RequesterUserID, RequestFingerprint: &requested.RequestFingerprint,
		MetadataJSON: &metadataJSON, Status: &status, ErrorCode: &empty, FailureClass: &empty,
		LeaseOwner: &requested.LeaseOwner, LeaseExpiresAt: &leaseExpiresAt, IncrementFencingToken: true,
		UpdatedAt: &nowValue, ExpiresAt: &empty,
	})
	if err != nil {
		return reportmodel.ReportExportPrepareReceipt{}, false, err
	}
	adopted, found, err := s.findReportExportPrepareByOperation(ctx, requested.WorkspaceID, requested.OperationID)
	if err != nil {
		return reportmodel.ReportExportPrepareReceipt{}, false, err
	}
	if !found {
		return reportmodel.ReportExportPrepareReceipt{}, false, fmt.Errorf("report export prepare receipt disappeared during orphan adoption")
	}
	return adopted, changed, nil
}

func (s *ReportExportPrepareReceiptStore) reclaimReportExportPrepare(ctx context.Context, requested reportmodel.ReportExportPrepareReceipt, now time.Time, leaseTTL time.Duration) (reportmodel.ReportExportPrepareClaimResult, error) {
	updatedAt, leaseExpiresAt := now.Format(time.RFC3339Nano), now.Add(leaseTTL).Format(time.RFC3339Nano)
	status, empty := string(idempotency.StatusProcessing), ""
	metadataJSON := json.RawMessage(reportExportPrepareMetadataJSON(requested))
	changed, err := s.operationStore().PatchRecord(ctx, sharedoperation.RecordFilter{
		WorkspaceID: requested.WorkspaceID, ID: requested.ID, Owner: reportExportPrepareOwner, Kind: reportExportPrepareKind,
		IdempotencyKey: requested.OperationID, RequestFingerprint: requested.RequestFingerprint, RequestedBy: requested.RequesterUserID,
		MetadataJSON: metadataJSON, LeaseExpiresAtOrBefore: updatedAt,
		ReclaimableStatus: string(idempotency.StatusFailedRetryable), ExpiredLeaseStatus: string(idempotency.StatusProcessing),
	}, sharedoperation.RecordChanges{
		Status: &status, ErrorCode: &empty, FailureClass: &empty, LeaseOwner: &requested.LeaseOwner,
		LeaseExpiresAt: &leaseExpiresAt, IncrementFencingToken: true, UpdatedAt: &updatedAt, ExpiresAt: &empty,
	})
	if err != nil {
		return reportmodel.ReportExportPrepareClaimResult{}, err
	}
	current, found, err := s.findReportExportPrepareByOperation(ctx, requested.WorkspaceID, requested.OperationID)
	if err != nil {
		return reportmodel.ReportExportPrepareClaimResult{}, err
	}
	if !found {
		return reportmodel.ReportExportPrepareClaimResult{}, fmt.Errorf("report export prepare receipt disappeared during reclaim")
	}
	if changed {
		s.store.ObserveIdempotency(ctx, requested.WorkspaceID, requested.UseCase, idempotency.OutcomeReclaimed)
		return reportmodel.ReportExportPrepareClaimResult{Decision: idempotency.DecisionAcquired, Receipt: current}, nil
	}
	s.store.ObserveIdempotency(ctx, requested.WorkspaceID, requested.UseCase, idempotency.OutcomeInProgress)
	return reportmodel.ReportExportPrepareClaimResult{Decision: idempotency.DecisionInProgress, Receipt: current}, nil
}

func (s *ReportExportPrepareReceiptStore) SaveReportExportPreparePayload(ctx context.Context, value reportmodel.ReportExportPreparePayload) (reportmodel.ReportExportPrepareReceipt, error) {
	now := normalizedReportExportPrepareTime(value.Now)
	workspaceID, receiptID := strings.TrimSpace(value.WorkspaceID), strings.TrimSpace(value.ReceiptID)
	payloadJSON, businessJobKey := strings.TrimSpace(value.PayloadJSON), strings.TrimSpace(value.BusinessJobKey)
	if workspaceID == "" || receiptID == "" || payloadJSON == "" || businessJobKey == "" || strings.TrimSpace(value.LeaseOwner) == "" || value.FencingToken < 1 {
		return reportmodel.ReportExportPrepareReceipt{}, fmt.Errorf("report export prepare payload binding is incomplete")
	}
	current, found, err := s.findReportExportPrepareByID(ctx, workspaceID, receiptID)
	if err != nil {
		return reportmodel.ReportExportPrepareReceipt{}, err
	}
	if !found || current.Status != string(idempotency.StatusProcessing) || current.LeaseOwner != strings.TrimSpace(value.LeaseOwner) || current.FencingToken != value.FencingToken || current.PayloadJSON != "" || current.JobID != "" {
		return reportmodel.ReportExportPrepareReceipt{}, reportExportPrepareLeaseLost(ctx, workspaceID, receiptID, s.store)
	}
	next := current
	next.PayloadJSON, next.BusinessJobKey = payloadJSON, businessJobKey
	next.UpdatedAt = now.Format(time.RFC3339Nano)
	resultJSON, relatedIDsJSON := json.RawMessage(reportExportPrepareResultJSON(next)), json.RawMessage(reportExportPrepareRelatedIDsJSON(next))
	changed, err := s.patchRecordWithSubjectGuard(ctx, current, sharedoperation.RecordFilter{
		WorkspaceID: workspaceID, ID: receiptID, Owner: reportExportPrepareOwner, Kind: reportExportPrepareKind,
		Status: string(idempotency.StatusProcessing), LeaseOwner: strings.TrimSpace(value.LeaseOwner), FencingToken: &value.FencingToken,
		ResultJSON: json.RawMessage(reportExportPrepareResultJSON(current)),
	}, sharedoperation.RecordChanges{ResultJSON: &resultJSON, RelatedIDsJSON: &relatedIDsJSON, UpdatedAt: &next.UpdatedAt})
	if err != nil {
		return reportmodel.ReportExportPrepareReceipt{}, err
	}
	if !changed {
		return reportmodel.ReportExportPrepareReceipt{}, reportExportPrepareLeaseLost(ctx, workspaceID, receiptID, s.store)
	}
	receipt, found, err := s.findReportExportPrepareByID(ctx, workspaceID, receiptID)
	if err != nil {
		return reportmodel.ReportExportPrepareReceipt{}, err
	}
	if !found {
		return reportmodel.ReportExportPrepareReceipt{}, fmt.Errorf("report export prepare receipt is unavailable after payload binding")
	}
	return receipt, nil
}

func (s *ReportExportPrepareReceiptStore) CompleteReportExportPrepare(ctx context.Context, value reportmodel.ReportExportPrepareCompletion) error {
	now := normalizedReportExportPrepareTime(value.Now)
	workspaceID, receiptID, jobID := strings.TrimSpace(value.WorkspaceID), strings.TrimSpace(value.ReceiptID), strings.TrimSpace(value.JobID)
	if workspaceID == "" || receiptID == "" || jobID == "" || strings.TrimSpace(value.LeaseOwner) == "" || value.FencingToken < 1 {
		return fmt.Errorf("report export prepare completion is incomplete")
	}
	current, found, err := s.findReportExportPrepareByID(ctx, workspaceID, receiptID)
	if err != nil {
		return err
	}
	if found && current.Status == string(idempotency.StatusSucceeded) && current.JobID == jobID {
		return nil
	}
	if !found || current.Status != string(idempotency.StatusProcessing) || current.LeaseOwner != strings.TrimSpace(value.LeaseOwner) || current.FencingToken != value.FencingToken || current.PayloadJSON == "" || current.BusinessJobKey == "" || current.JobID != "" {
		return reportExportPrepareLeaseLost(ctx, workspaceID, receiptID, s.store)
	}
	next := current
	next.Status, next.JobID = string(idempotency.StatusSucceeded), jobID
	next.LeaseOwner, next.LeaseExpiresAt = "", ""
	next.ExpiresAt = value.ExpiresAt.UTC().Format(time.RFC3339Nano)
	next.UpdatedAt = now.Format(time.RFC3339Nano)
	status, empty := next.Status, ""
	resultJSON, relatedIDsJSON := json.RawMessage(reportExportPrepareResultJSON(next)), json.RawMessage(reportExportPrepareRelatedIDsJSON(next))
	changed, err := s.patchRecordWithSubjectGuard(ctx, current, sharedoperation.RecordFilter{
		WorkspaceID: workspaceID, ID: receiptID, Owner: reportExportPrepareOwner, Kind: reportExportPrepareKind,
		Status: string(idempotency.StatusProcessing), LeaseOwner: strings.TrimSpace(value.LeaseOwner), FencingToken: &value.FencingToken,
		ResultJSON: json.RawMessage(reportExportPrepareResultJSON(current)),
	}, sharedoperation.RecordChanges{
		Status: &status, ResultJSON: &resultJSON, RelatedIDsJSON: &relatedIDsJSON, LeaseOwner: &empty,
		LeaseExpiresAt: &empty, ExpiresAt: &next.ExpiresAt, FinishedAt: &next.UpdatedAt, UpdatedAt: &next.UpdatedAt,
	})
	if err != nil {
		return err
	}
	if changed {
		return nil
	}
	current, found, err = s.findReportExportPrepareByID(ctx, workspaceID, receiptID)
	if err != nil {
		return err
	}
	if found && current.Status == string(idempotency.StatusSucceeded) && current.JobID == jobID {
		return nil
	}
	s.store.ObserveIdempotency(ctx, workspaceID, reportmodel.ReportExportPrepareUseCase, idempotency.OutcomeLeaseLost)
	return mutation.MutationConflict("report_export_prepare_receipt", receiptID, mutation.MutationConflictLeaseLost, nil)
}

func (s *ReportExportPrepareReceiptStore) FailReportExportPrepareRetryable(ctx context.Context, value reportmodel.ReportExportPrepareFailure) error {
	return s.finishReportExportPrepareFailure(ctx, value, idempotency.StatusFailedRetryable)
}

func (s *ReportExportPrepareReceiptStore) FailReportExportPrepareTerminal(ctx context.Context, value reportmodel.ReportExportPrepareFailure) error {
	return s.finishReportExportPrepareFailure(ctx, value, idempotency.StatusFailedTerminal)
}

func (s *ReportExportPrepareReceiptStore) ReleaseReportExportPrepare(ctx context.Context, value reportmodel.ReportExportPrepareFailure) error {
	workspaceID, receiptID, leaseOwner := strings.TrimSpace(value.WorkspaceID), strings.TrimSpace(value.ReceiptID), strings.TrimSpace(value.LeaseOwner)
	if workspaceID == "" || receiptID == "" || leaseOwner == "" || value.FencingToken < 1 {
		return fmt.Errorf("report export prepare release binding is incomplete")
	}
	current, found, err := s.findReportExportPrepareByID(ctx, workspaceID, receiptID)
	if err != nil {
		return err
	}
	if !found || current.Status != string(idempotency.StatusProcessing) || current.LeaseOwner != leaseOwner || current.FencingToken != value.FencingToken || current.PayloadJSON != "" || current.BusinessJobKey != "" || current.JobID != "" {
		return reportExportPrepareLeaseLost(ctx, workspaceID, receiptID, s.store)
	}
	rows, err := s.operationStore().DeleteRecords(ctx, sharedoperation.RecordFilter{
		WorkspaceID: workspaceID, ID: receiptID, Owner: reportExportPrepareOwner, Kind: reportExportPrepareKind,
		Status: string(idempotency.StatusProcessing), LeaseOwner: leaseOwner, FencingToken: &value.FencingToken,
		ResultJSON: json.RawMessage(reportExportPrepareResultJSON(current)),
	})
	if err != nil {
		return err
	}
	if rows != 1 {
		return reportExportPrepareLeaseLost(ctx, workspaceID, receiptID, s.store)
	}
	return nil
}

func (s *ReportExportPrepareReceiptStore) finishReportExportPrepareFailure(ctx context.Context, value reportmodel.ReportExportPrepareFailure, status idempotency.Status) error {
	now := normalizedReportExportPrepareTime(value.Now)
	workspaceID, receiptID, leaseOwner := strings.TrimSpace(value.WorkspaceID), strings.TrimSpace(value.ReceiptID), strings.TrimSpace(value.LeaseOwner)
	errorCode := strings.TrimSpace(value.ErrorCode)
	if workspaceID == "" || receiptID == "" || leaseOwner == "" || value.FencingToken < 1 || value.ExpiresAt.IsZero() || (status == idempotency.StatusFailedTerminal && errorCode == "") {
		return fmt.Errorf("report export prepare failure binding is incomplete")
	}
	statusValue, failureClass, empty := string(status), reportExportPrepareFailureClass(status), ""
	nowValue, expiresAt := now.Format(time.RFC3339Nano), value.ExpiresAt.UTC().Format(time.RFC3339Nano)
	changed, err := s.operationStore().PatchRecord(ctx, sharedoperation.RecordFilter{
		WorkspaceID: workspaceID, ID: receiptID, Owner: reportExportPrepareOwner, Kind: reportExportPrepareKind,
		Status: string(idempotency.StatusProcessing), LeaseOwner: leaseOwner, FencingToken: &value.FencingToken,
	}, sharedoperation.RecordChanges{
		Status: &statusValue, ErrorCode: &errorCode, FailureClass: &failureClass, LeaseOwner: &empty,
		LeaseExpiresAt: &empty, UpdatedAt: &nowValue, FinishedAt: &nowValue, ExpiresAt: &expiresAt,
	})
	if err != nil {
		return err
	}
	if !changed {
		return reportExportPrepareLeaseLost(ctx, workspaceID, receiptID, s.store)
	}
	return nil
}

func (s *ReportExportPrepareReceiptStore) BindReportExportCompletion(ctx context.Context, binding reportmodel.ReportExportCompletionBinding) (reportmodel.ReportExportCompletionBindingResult, error) {
	workspaceID, receiptID := strings.TrimSpace(binding.WorkspaceID), strings.TrimSpace(binding.ReceiptID)
	payloadJSON := strings.TrimSpace(binding.PayloadJSON)
	if workspaceID == "" || receiptID == "" || strings.TrimSpace(binding.JobID) == "" || strings.TrimSpace(binding.ArtifactID) == "" || payloadJSON == "" || strings.TrimSpace(binding.CompletionFingerprint) == "" {
		return reportmodel.ReportExportCompletionBindingResult{}, fmt.Errorf("report export completion binding is incomplete")
	}
	for attempt := 0; attempt < 3; attempt++ {
		current, found, err := s.findReportExportPrepareByID(ctx, workspaceID, receiptID)
		if err != nil {
			return reportmodel.ReportExportCompletionBindingResult{}, err
		}
		if !found || !reportExportCompletionOwnsReceipt(current, binding, payloadJSON) {
			return reportmodel.ReportExportCompletionBindingResult{Decision: reportmodel.ReportExportCompletionConflict, Receipt: current}, nil
		}
		if current.CompletionFingerprint != "" || current.CompletionArtifactID != "" {
			decision := reportmodel.ReportExportCompletionConflict
			if current.CompletionFingerprint == strings.TrimSpace(binding.CompletionFingerprint) && current.CompletionArtifactID == strings.TrimSpace(binding.ArtifactID) {
				decision = reportmodel.ReportExportCompletionReplay
			}
			return reportmodel.ReportExportCompletionBindingResult{Decision: decision, Receipt: current}, nil
		}
		next := current
		next.Status = string(idempotency.StatusSucceeded)
		next.JobID = strings.TrimSpace(binding.JobID)
		next.CompletionArtifactID = strings.TrimSpace(binding.ArtifactID)
		next.CompletionFingerprint = strings.TrimSpace(binding.CompletionFingerprint)
		next.LeaseOwner, next.LeaseExpiresAt = "", ""
		next.ExpiresAt = binding.ExpiresAt.UTC().Format(time.RFC3339Nano)
		next.UpdatedAt = normalizedReportExportPrepareTime(binding.Now).Format(time.RFC3339Nano)
		status, empty := next.Status, ""
		resultJSON, relatedIDsJSON := json.RawMessage(reportExportPrepareResultJSON(next)), json.RawMessage(reportExportPrepareRelatedIDsJSON(next))
		changed, err := s.patchRecordWithSubjectGuard(ctx, current, sharedoperation.RecordFilter{
			WorkspaceID: workspaceID, ID: receiptID, Owner: reportExportPrepareOwner, Kind: reportExportPrepareKind,
			Status: current.Status, ResultJSON: json.RawMessage(reportExportPrepareResultJSON(current)),
		}, sharedoperation.RecordChanges{
			Status: &status, ResultJSON: &resultJSON, RelatedIDsJSON: &relatedIDsJSON, LeaseOwner: &empty,
			LeaseExpiresAt: &empty, ExpiresAt: &next.ExpiresAt, FinishedAt: &next.UpdatedAt, UpdatedAt: &next.UpdatedAt,
		})
		if err != nil {
			return reportmodel.ReportExportCompletionBindingResult{}, err
		}
		if changed {
			return reportmodel.ReportExportCompletionBindingResult{Decision: reportmodel.ReportExportCompletionBound, Receipt: next}, nil
		}
	}
	current, _, err := s.findReportExportPrepareByID(ctx, workspaceID, receiptID)
	return reportmodel.ReportExportCompletionBindingResult{Decision: reportmodel.ReportExportCompletionConflict, Receipt: current}, err
}

func (s *ReportExportPrepareReceiptStore) findReportExportPrepareByOperation(ctx context.Context, workspaceID, operationID string) (reportmodel.ReportExportPrepareReceipt, bool, error) {
	return s.findReportExportPrepare(ctx, sharedoperation.RecordFilter{
		WorkspaceID: strings.TrimSpace(workspaceID), Owner: reportExportPrepareOwner, Kind: reportExportPrepareKind,
		IdempotencyKey: strings.TrimSpace(operationID),
	})
}

func (s *ReportExportPrepareReceiptStore) findReportExportPrepareByID(ctx context.Context, workspaceID, receiptID string) (reportmodel.ReportExportPrepareReceipt, bool, error) {
	return s.findReportExportPrepare(ctx, sharedoperation.RecordFilter{
		WorkspaceID: strings.TrimSpace(workspaceID), ID: strings.TrimSpace(receiptID), Owner: reportExportPrepareOwner, Kind: reportExportPrepareKind,
	})
}

func (s *ReportExportPrepareReceiptStore) findReportExportPrepare(ctx context.Context, filter sharedoperation.RecordFilter) (reportmodel.ReportExportPrepareReceipt, bool, error) {
	record, found, err := s.operationStore().GetRecord(ctx, filter)
	if err != nil || !found {
		return reportmodel.ReportExportPrepareReceipt{}, found, err
	}
	value, err := reportExportPrepareReceipt(record)
	return value, err == nil, err
}

func normalizeReportExportPrepareClaim(request reportmodel.ReportExportPrepareClaimRequest) (reportmodel.ReportExportPrepareReceipt, time.Time, time.Duration, error) {
	receipt := request.Receipt
	receipt.WorkspaceID = strings.TrimSpace(receipt.WorkspaceID)
	receipt.RequesterUserID = strings.TrimSpace(receipt.RequesterUserID)
	receipt.UseCase = strings.TrimSpace(receipt.UseCase)
	receipt.ReportKey = strings.TrimSpace(receipt.ReportKey)
	receipt.ObjectKey = strings.TrimSpace(receipt.ObjectKey)
	receipt.AuditID = strings.TrimSpace(receipt.AuditID)
	receipt.RetryOfJobID = strings.TrimSpace(receipt.RetryOfJobID)
	callerKey := strings.TrimSpace(receipt.CallerKey)
	fingerprint, owner := strings.TrimSpace(request.RequestFingerprint), strings.TrimSpace(request.LeaseOwner)
	if receipt.WorkspaceID == "" || receipt.RequesterUserID == "" || receipt.UseCase == "" || receipt.ReportKey == "" || receipt.ObjectKey == "" || receipt.AuditID == "" || callerKey == "" || fingerprint == "" || owner == "" {
		return receipt, time.Time{}, 0, fmt.Errorf("report export prepare claim is incomplete")
	}
	receipt.CallerKey = reportExportPrepareCallerKey(callerKey)
	if request.LeaseTTL <= 0 {
		request.LeaseTTL = reportExportPrepareReceiptLeaseTTL
	}
	now := normalizedReportExportPrepareTime(request.Now)
	return receipt, now, request.LeaseTTL, nil
}

func normalizedReportExportPrepareTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func reportExportPrepareOperationID(receipt reportmodel.ReportExportPrepareReceipt) string {
	values := []string{receipt.WorkspaceID, receipt.UseCase, receipt.ReportKey, receipt.ObjectKey, receipt.AuditID}
	if receipt.RetryOfJobID != "" {
		values = append(values, "retry", receipt.RetryOfJobID)
	}
	return reportExportPrepareDigest("operation", values...)
}

func reportExportPrepareReceiptID(receipt reportmodel.ReportExportPrepareReceipt) string {
	values := []string{receipt.WorkspaceID, receipt.RequesterUserID, receipt.UseCase, receipt.ReportKey, receipt.ObjectKey, receipt.AuditID, receipt.CallerKey}
	if receipt.RetryOfJobID != "" {
		values = append(values, "retry", receipt.RetryOfJobID)
	}
	return reportExportPrepareDigest("receipt", values...)
}

func reportExportPrepareDigest(kind string, values ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(append([]string{"domainry-runtime/report-export-prepare/v1", kind}, values...), "\x00")))
	return "report_export_prepare:" + hex.EncodeToString(digest[:])
}

func reportExportPrepareCallerKey(value string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func sameReportExportPrepareOperation(left, right reportmodel.ReportExportPrepareReceipt) bool {
	return left.OperationID == right.OperationID && left.WorkspaceID == right.WorkspaceID && left.UseCase == right.UseCase && left.ReportKey == right.ReportKey && left.ObjectKey == right.ObjectKey && left.AuditID == right.AuditID && left.RetryOfJobID == right.RetryOfJobID
}

func reportExportPrepareFailureClass(status idempotency.Status) string {
	if status == idempotency.StatusFailedRetryable {
		return "retryable"
	}
	return "terminal"
}

func reportExportPrepareLeaseLost(ctx context.Context, workspaceID, receiptID string, store *database.RuntimeStore) error {
	store.ObserveIdempotency(ctx, strings.TrimSpace(workspaceID), reportmodel.ReportExportPrepareUseCase, idempotency.OutcomeLeaseLost)
	return mutation.MutationConflict("report_export_prepare_receipt", strings.TrimSpace(receiptID), mutation.MutationConflictLeaseLost, nil)
}

func reportExportCompletionOwnsReceipt(receipt reportmodel.ReportExportPrepareReceipt, binding reportmodel.ReportExportCompletionBinding, payloadJSON string) bool {
	statusCanComplete := (receipt.Status == string(idempotency.StatusSucceeded) && receipt.JobID == strings.TrimSpace(binding.JobID)) ||
		((receipt.Status == string(idempotency.StatusProcessing) || receipt.Status == string(idempotency.StatusFailedRetryable)) && receipt.JobID == "")
	return statusCanComplete && receipt.PayloadJSON == payloadJSON && receipt.BusinessJobKey != "" &&
		receipt.WorkspaceID == strings.TrimSpace(binding.WorkspaceID) &&
		receipt.RequesterUserID == strings.TrimSpace(binding.RequesterUserID) &&
		receipt.ReportKey == strings.TrimSpace(binding.ReportKey) &&
		receipt.ObjectKey == strings.TrimSpace(binding.ObjectKey) &&
		receipt.AuditID == strings.TrimSpace(binding.AuditID)
}

func reportExportPrepareLease(value reportmodel.ReportExportPrepareReceipt) idempotency.Lease {
	expiresAt, _ := time.Parse(time.RFC3339Nano, value.LeaseExpiresAt)
	return idempotency.Lease{Owner: value.LeaseOwner, Token: value.FencingToken, ExpiresAt: expiresAt}
}

func reportExportPrepareOrphanCanBeAdopted(value reportmodel.ReportExportPrepareReceipt, now time.Time) bool {
	return value.Status == string(idempotency.StatusProcessing) && !reportExportPrepareLease(value).IsLive(now) &&
		value.PayloadJSON == "" && value.BusinessJobKey == "" && value.JobID == "" &&
		value.CompletionArtifactID == "" && value.CompletionFingerprint == ""
}

func (s *ReportExportPrepareReceiptStore) operationStore() *sharedoperation.SQLStore {
	return sharedoperation.NewSQLStore(s.store.DB(), s.store.SQLRenderer)
}

func (s *ReportExportPrepareReceiptStore) patchRecordWithSubjectGuard(ctx context.Context, current reportmodel.ReportExportPrepareReceipt, filter sharedoperation.RecordFilter, changes sharedoperation.RecordChanges) (bool, error) {
	tx, err := s.store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.store.GuardSubjectEvidenceWrite(ctx, tx, current.WorkspaceID, sharedoperation.TableName,
		[]string{"id", "owner", "requested_by"}, []any{current.ID, reportExportPrepareOwner, current.RequesterUserID}); err != nil {
		return false, err
	}
	changed, err := s.operationStore().PatchRecord(sharedoperation.WithExecutor(ctx, tx), filter, changes)
	if err != nil || !changed {
		return changed, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func reportExportPrepareRecord(value reportmodel.ReportExportPrepareReceipt) sharedoperation.Record {
	relatedIDs, _ := json.Marshal(reportExportPrepareRelatedIDs(value))
	evidence, _ := json.Marshal([]string{value.AuditID})
	failureClass := ""
	if value.Status == string(idempotency.StatusFailedRetryable) {
		failureClass = "retryable"
	} else if value.Status == string(idempotency.StatusFailedTerminal) {
		failureClass = "terminal"
	}
	return sharedoperation.Record{
		ID: value.ID, WorkspaceID: value.WorkspaceID, Owner: reportExportPrepareOwner, Kind: reportExportPrepareKind,
		ActionKey: value.UseCase, ResourceType: "report", ResourceID: value.ReportKey, IdempotencyKey: value.OperationID,
		RequestFingerprint: value.RequestFingerprint, RequestedBy: value.RequesterUserID, Reason: value.ObjectKey, Reference: value.AuditID,
		Status: value.Status, StatusURL: "/operations/" + value.ID, ResultJSON: json.RawMessage(reportExportPrepareResultJSON(value)),
		MetadataJSON: json.RawMessage(reportExportPrepareMetadataJSON(value)), ErrorCode: value.TerminalErrorCode, FailureClass: failureClass,
		RelatedIDsJSON: relatedIDs, Correlation: value.OperationID, EvidenceJSON: evidence,
		LeaseOwner: value.LeaseOwner, LeaseExpiresAt: value.LeaseExpiresAt, FencingToken: value.FencingToken, ExpiresAt: value.ExpiresAt,
		CreatedAt: value.CreatedAt, StartedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

type reportExportPrepareMetadata struct {
	CallerKey    string `json:"caller_key"`
	RetryOfJobID string `json:"retry_of_job_id,omitempty"`
}

type reportExportPrepareResult struct {
	PayloadJSON           string `json:"payload_json,omitempty"`
	BusinessJobKey        string `json:"business_job_key,omitempty"`
	JobID                 string `json:"job_id,omitempty"`
	CompletionArtifactID  string `json:"completion_artifact_id,omitempty"`
	CompletionFingerprint string `json:"completion_fingerprint,omitempty"`
}

func reportExportPrepareMetadataJSON(value reportmodel.ReportExportPrepareReceipt) string {
	encoded, _ := json.Marshal(reportExportPrepareMetadata{CallerKey: value.CallerKey, RetryOfJobID: value.RetryOfJobID})
	return string(encoded)
}

func reportExportPrepareResultJSON(value reportmodel.ReportExportPrepareReceipt) string {
	encoded, _ := json.Marshal(reportExportPrepareResult{
		PayloadJSON: value.PayloadJSON, BusinessJobKey: value.BusinessJobKey, JobID: value.JobID,
		CompletionArtifactID: value.CompletionArtifactID, CompletionFingerprint: value.CompletionFingerprint,
	})
	return string(encoded)
}

func reportExportPrepareRelatedIDs(value reportmodel.ReportExportPrepareReceipt) []string {
	result := []string{}
	for _, id := range []string{value.JobID, value.CompletionArtifactID, value.RetryOfJobID} {
		if strings.TrimSpace(id) != "" {
			result = append(result, id)
		}
	}
	return result
}

func reportExportPrepareRelatedIDsJSON(value reportmodel.ReportExportPrepareReceipt) string {
	encoded, _ := json.Marshal(reportExportPrepareRelatedIDs(value))
	return string(encoded)
}

func reportExportPrepareReceipt(record sharedoperation.Record) (reportmodel.ReportExportPrepareReceipt, error) {
	value := reportmodel.ReportExportPrepareReceipt{
		ID: record.ID, WorkspaceID: record.WorkspaceID, UseCase: record.ActionKey, ReportKey: record.ResourceID,
		OperationID: record.IdempotencyKey, RequestFingerprint: record.RequestFingerprint, RequesterUserID: record.RequestedBy,
		ObjectKey: record.Reason, AuditID: record.Reference, Status: record.Status, TerminalErrorCode: record.ErrorCode,
		LeaseOwner: record.LeaseOwner, LeaseExpiresAt: record.LeaseExpiresAt, FencingToken: record.FencingToken,
		ExpiresAt: record.ExpiresAt, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
	if record.SystemPurpose != "" || record.Owner != reportExportPrepareOwner || record.Kind != reportExportPrepareKind || record.ResourceType != "report" || record.Correlation != value.OperationID {
		return value, fmt.Errorf("report export prepare operation identity is invalid")
	}
	var metadata reportExportPrepareMetadata
	if err := json.Unmarshal(record.MetadataJSON, &metadata); err != nil {
		return value, fmt.Errorf("decode report export prepare operation metadata: %w", err)
	}
	var result reportExportPrepareResult
	if err := json.Unmarshal(record.ResultJSON, &result); err != nil {
		return value, fmt.Errorf("decode report export prepare operation result: %w", err)
	}
	value.CallerKey, value.RetryOfJobID = metadata.CallerKey, metadata.RetryOfJobID
	value.PayloadJSON, value.BusinessJobKey, value.JobID = result.PayloadJSON, result.BusinessJobKey, result.JobID
	value.CompletionArtifactID, value.CompletionFingerprint = result.CompletionArtifactID, result.CompletionFingerprint
	return value, nil
}

func reportExportPrepareClaimBackoff(ctx context.Context, attempt int) error {
	timer := time.NewTimer(time.Duration(attempt+1) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var _ reportcontract.ReportExportPrepareReceiptStore = (*ReportExportPrepareReceiptStore)(nil)
