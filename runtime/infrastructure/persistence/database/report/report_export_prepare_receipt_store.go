package report

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-orm/query"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

const (
	reportExportPrepareReceiptLeaseTTL = 5 * time.Minute
	reportExportPrepareTable           = "_operations"
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
	columns, values := reportExportPrepareReceiptColumns(), reportExportPrepareReceiptValues(receipt)
	if err := s.store.GuardSubjectEvidenceWrite(ctx, s.store.DB(), receipt.WorkspaceID, reportExportPrepareTable, columns, values); err != nil {
		return reportmodel.ReportExportPrepareClaimResult{}, err
	}
	statement, arguments, buildErr := query.NewWorkspaceInsertBuilder(s.store.SQLRenderer, reportExportPrepareTable, receipt.WorkspaceID).
		Columns(append(columns[:1], columns[2:]...)...).
		Values(append(values[:1], values[2:]...)...).
		Build()
	if buildErr != nil {
		return reportmodel.ReportExportPrepareClaimResult{}, fmt.Errorf("build report export prepare receipt insert: %w", buildErr)
	}
	if _, insertErr := s.store.DB().ExecContext(ctx, statement, arguments...); insertErr == nil {
		s.store.ObserveIdempotency(ctx, receipt.WorkspaceID, receipt.UseCase, idempotency.OutcomeAcquired)
		return reportmodel.ReportExportPrepareClaimResult{Decision: idempotency.DecisionAcquired, Receipt: receipt}, nil
	} else {
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
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, reportExportPrepareTable, requested.WorkspaceID).
		Set("id", requested.ID).
		Set("requested_by", requested.RequesterUserID).
		Set("metadata_json", reportExportPrepareMetadataJSON(requested)).
		Set("request_fingerprint", requested.RequestFingerprint).
		Set("status", string(idempotency.StatusProcessing)).
		Set("error_code", "").
		Set("failure_class", "").
		Set("lease_owner", requested.LeaseOwner).
		Set("lease_expires_at", leaseExpiresAt).
		SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).
		Set("updated_at", nowValue).
		Set("expires_at", "").
		Where(query.And(
			query.Equal("id", current.ID),
			reportExportPrepareOperationPredicate(requested),
			query.Equal("status", string(idempotency.StatusProcessing)),
			query.LessThanOrEqual("lease_expires_at", nowValue),
			query.Equal("result_json", reportExportPrepareResultJSON(current)),
		)).Build()
	if err != nil {
		return reportmodel.ReportExportPrepareReceipt{}, false, fmt.Errorf("build report export prepare orphan adoption: %w", err)
	}
	result, err := s.store.DB().ExecContext(ctx, statement, arguments...)
	if err != nil {
		return reportmodel.ReportExportPrepareReceipt{}, false, err
	}
	rows, err := result.RowsAffected()
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
	return adopted, rows == 1, nil
}

func (s *ReportExportPrepareReceiptStore) reclaimReportExportPrepare(ctx context.Context, requested reportmodel.ReportExportPrepareReceipt, now time.Time, leaseTTL time.Duration) (reportmodel.ReportExportPrepareClaimResult, error) {
	updatedAt, leaseExpiresAt := now.Format(time.RFC3339Nano), now.Add(leaseTTL).Format(time.RFC3339Nano)
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, reportExportPrepareTable, requested.WorkspaceID).
		Set("status", string(idempotency.StatusProcessing)).
		Set("error_code", "").
		Set("failure_class", "").
		Set("lease_owner", requested.LeaseOwner).
		Set("lease_expires_at", leaseExpiresAt).
		SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).
		Set("updated_at", updatedAt).
		Set("expires_at", "").
		Where(query.And(
			reportExportPrepareOperationPredicate(requested),
			query.Equal("requested_by", requested.RequesterUserID),
			query.Equal("metadata_json", reportExportPrepareMetadataJSON(requested)),
			query.Equal("request_fingerprint", requested.RequestFingerprint),
			query.Or(
				query.Equal("status", string(idempotency.StatusFailedRetryable)),
				query.And(query.Equal("status", string(idempotency.StatusProcessing)), query.LessThanOrEqual("lease_expires_at", updatedAt)),
			),
		)).Build()
	if err != nil {
		return reportmodel.ReportExportPrepareClaimResult{}, fmt.Errorf("build report export prepare reclaim: %w", err)
	}
	result, err := s.store.DB().ExecContext(ctx, statement, arguments...)
	if err != nil {
		return reportmodel.ReportExportPrepareClaimResult{}, err
	}
	rows, err := result.RowsAffected()
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
	if rows == 1 {
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
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, reportExportPrepareTable, workspaceID).
		Set("result_json", reportExportPrepareResultJSON(next)).
		Set("related_ids_json", reportExportPrepareRelatedIDsJSON(next)).
		Set("updated_at", next.UpdatedAt).
		Where(query.And(
			reportExportPrepareFencePredicate(receiptID, value.LeaseOwner, value.FencingToken),
			query.Equal("result_json", reportExportPrepareResultJSON(current)),
			s.store.SubjectEvidenceWriteAllowed(workspaceID, reportExportPrepareTable, receiptID),
			s.store.SubjectActorWriteAllowed(workspaceID, current.RequesterUserID),
		)).Build()
	if err != nil {
		return reportmodel.ReportExportPrepareReceipt{}, fmt.Errorf("build report export prepare payload binding: %w", err)
	}
	if err := expectOneReportExportPrepareMutation(ctx, s.store, statement, arguments, receiptID); err != nil {
		return reportmodel.ReportExportPrepareReceipt{}, err
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
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, reportExportPrepareTable, workspaceID).
		Set("status", next.Status).
		Set("result_json", reportExportPrepareResultJSON(next)).
		Set("related_ids_json", reportExportPrepareRelatedIDsJSON(next)).
		Set("lease_owner", "").
		Set("lease_expires_at", "").
		Set("expires_at", next.ExpiresAt).
		Set("finished_at", next.UpdatedAt).
		Set("updated_at", next.UpdatedAt).
		Where(query.And(
			reportExportPrepareFencePredicate(receiptID, value.LeaseOwner, value.FencingToken),
			query.Equal("result_json", reportExportPrepareResultJSON(current)),
			s.store.SubjectEvidenceWriteAllowed(workspaceID, reportExportPrepareTable, receiptID),
			s.store.SubjectActorWriteAllowed(workspaceID, current.RequesterUserID),
		)).Build()
	if err != nil {
		return fmt.Errorf("build report export prepare completion: %w", err)
	}
	result, err := s.store.DB().ExecContext(ctx, statement, arguments...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 1 {
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
	statement, arguments, err := query.NewWorkspaceDeleteBuilder(s.store.SQLRenderer, reportExportPrepareTable, workspaceID).
		Where(query.And(
			reportExportPrepareFencePredicate(receiptID, leaseOwner, value.FencingToken),
			query.Equal("result_json", reportExportPrepareResultJSON(current)),
		)).Build()
	if err != nil {
		return fmt.Errorf("build report export prepare release: %w", err)
	}
	return expectOneReportExportPrepareMutation(ctx, s.store, statement, arguments, receiptID)
}

func (s *ReportExportPrepareReceiptStore) finishReportExportPrepareFailure(ctx context.Context, value reportmodel.ReportExportPrepareFailure, status idempotency.Status) error {
	now := normalizedReportExportPrepareTime(value.Now)
	workspaceID, receiptID, leaseOwner := strings.TrimSpace(value.WorkspaceID), strings.TrimSpace(value.ReceiptID), strings.TrimSpace(value.LeaseOwner)
	errorCode := strings.TrimSpace(value.ErrorCode)
	if workspaceID == "" || receiptID == "" || leaseOwner == "" || value.FencingToken < 1 || value.ExpiresAt.IsZero() || (status == idempotency.StatusFailedTerminal && errorCode == "") {
		return fmt.Errorf("report export prepare failure binding is incomplete")
	}
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, reportExportPrepareTable, workspaceID).
		Set("status", string(status)).
		Set("error_code", errorCode).
		Set("failure_class", reportExportPrepareFailureClass(status)).
		Set("lease_owner", "").
		Set("lease_expires_at", "").
		Set("updated_at", now.Format(time.RFC3339Nano)).
		Set("finished_at", now.Format(time.RFC3339Nano)).
		Set("expires_at", value.ExpiresAt.UTC().Format(time.RFC3339Nano)).
		Where(reportExportPrepareFencePredicate(receiptID, leaseOwner, value.FencingToken)).Build()
	if err != nil {
		return fmt.Errorf("build report export prepare failure transition: %w", err)
	}
	return expectOneReportExportPrepareMutation(ctx, s.store, statement, arguments, receiptID)
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
		statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, reportExportPrepareTable, workspaceID).
			Set("status", next.Status).
			Set("result_json", reportExportPrepareResultJSON(next)).
			Set("related_ids_json", reportExportPrepareRelatedIDsJSON(next)).
			Set("lease_owner", "").
			Set("lease_expires_at", "").
			Set("expires_at", next.ExpiresAt).
			Set("finished_at", next.UpdatedAt).
			Set("updated_at", next.UpdatedAt).
			Where(query.And(
				query.Equal("id", receiptID),
				query.Equal("owner", reportExportPrepareOwner),
				query.Equal("kind", reportExportPrepareKind),
				query.Equal("status", current.Status),
				query.Equal("result_json", reportExportPrepareResultJSON(current)),
				s.store.SubjectEvidenceWriteAllowed(workspaceID, reportExportPrepareTable, receiptID),
				s.store.SubjectActorWriteAllowed(workspaceID, current.RequesterUserID),
			)).Build()
		if err != nil {
			return reportmodel.ReportExportCompletionBindingResult{}, fmt.Errorf("build report export completion binding: %w", err)
		}
		result, err := s.store.DB().ExecContext(ctx, statement, arguments...)
		if err != nil {
			return reportmodel.ReportExportCompletionBindingResult{}, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return reportmodel.ReportExportCompletionBindingResult{}, err
		}
		if rows == 1 {
			return reportmodel.ReportExportCompletionBindingResult{Decision: reportmodel.ReportExportCompletionBound, Receipt: next}, nil
		}
	}
	current, _, err := s.findReportExportPrepareByID(ctx, workspaceID, receiptID)
	return reportmodel.ReportExportCompletionBindingResult{Decision: reportmodel.ReportExportCompletionConflict, Receipt: current}, err
}

func (s *ReportExportPrepareReceiptStore) findReportExportPrepareByOperation(ctx context.Context, workspaceID, operationID string) (reportmodel.ReportExportPrepareReceipt, bool, error) {
	return s.findReportExportPrepare(ctx, workspaceID, query.Equal("idempotency_key", strings.TrimSpace(operationID)))
}

func (s *ReportExportPrepareReceiptStore) findReportExportPrepareByID(ctx context.Context, workspaceID, receiptID string) (reportmodel.ReportExportPrepareReceipt, bool, error) {
	return s.findReportExportPrepare(ctx, workspaceID, query.Equal("id", strings.TrimSpace(receiptID)))
}

func (s *ReportExportPrepareReceiptStore) findReportExportPrepare(ctx context.Context, workspaceID string, predicate query.Predicate) (reportmodel.ReportExportPrepareReceipt, bool, error) {
	statement, arguments, err := query.NewWorkspaceSelectBuilder(s.store.SQLRenderer, reportExportPrepareTable, strings.TrimSpace(workspaceID)).
		Columns(reportExportPrepareReceiptColumns()...).Where(query.And(
		query.Equal("owner", reportExportPrepareOwner),
		query.Equal("kind", reportExportPrepareKind),
		predicate,
	)).Limit(1).Build()
	if err != nil {
		return reportmodel.ReportExportPrepareReceipt{}, false, fmt.Errorf("build report export prepare receipt lookup: %w", err)
	}
	value, err := scanReportExportPrepareReceipt(s.store.DB().QueryRowContext(ctx, statement, arguments...))
	if errors.Is(err, sql.ErrNoRows) {
		return reportmodel.ReportExportPrepareReceipt{}, false, nil
	}
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

func reportExportPrepareOperationPredicate(value reportmodel.ReportExportPrepareReceipt) query.Predicate {
	return query.And(
		query.Equal("owner", reportExportPrepareOwner),
		query.Equal("kind", reportExportPrepareKind),
		query.Equal("action_key", value.UseCase),
		query.Equal("resource_type", "report"),
		query.Equal("resource_id", value.ReportKey),
		query.Equal("idempotency_key", value.OperationID),
		query.Equal("reason", value.ObjectKey),
		query.Equal("reference", value.AuditID),
	)
}

func reportExportPrepareFencePredicate(receiptID, leaseOwner string, fencingToken int64) query.Predicate {
	return query.And(
		query.Equal("id", strings.TrimSpace(receiptID)),
		query.Equal("owner", reportExportPrepareOwner),
		query.Equal("kind", reportExportPrepareKind),
		query.Equal("status", string(idempotency.StatusProcessing)),
		query.Equal("lease_owner", strings.TrimSpace(leaseOwner)),
		query.Equal("fencing_token", fencingToken),
	)
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

func expectOneReportExportPrepareMutation(ctx context.Context, store *database.RuntimeStore, statement string, arguments []any, receiptID string) error {
	result, err := store.DB().ExecContext(ctx, statement, arguments...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		store.ObserveIdempotency(ctx, "", reportmodel.ReportExportPrepareUseCase, idempotency.OutcomeLeaseLost)
		return mutation.MutationConflict("report_export_prepare_receipt", receiptID, mutation.MutationConflictLeaseLost, nil)
	}
	return nil
}

func reportExportPrepareReceiptColumns() []string {
	return []string{"id", "workspace_id", "system_purpose", "owner", "kind", "action_key", "parent_id", "resource_type", "resource_id", "idempotency_key", "request_fingerprint", "requested_by", "reason", "reference", "status", "status_url", "result_json", "metadata_json", "error_code", "failure_class", "next_action", "related_ids_json", "correlation", "evidence_json", "lease_owner", "lease_expires_at", "fencing_token", "expires_at", "created_at", "started_at", "finished_at", "updated_at"}
}

func reportExportPrepareReceiptValues(value reportmodel.ReportExportPrepareReceipt) []any {
	resultJSON := reportExportPrepareResultJSON(value)
	metadataJSON := reportExportPrepareMetadataJSON(value)
	relatedIDs, _ := json.Marshal(reportExportPrepareRelatedIDs(value))
	evidence, _ := json.Marshal([]string{value.AuditID})
	failureClass := ""
	if value.Status == string(idempotency.StatusFailedRetryable) {
		failureClass = "retryable"
	} else if value.Status == string(idempotency.StatusFailedTerminal) {
		failureClass = "terminal"
	}
	return []any{value.ID, value.WorkspaceID, "", reportExportPrepareOwner, reportExportPrepareKind, value.UseCase, "", "report", value.ReportKey, value.OperationID, value.RequestFingerprint, value.RequesterUserID, value.ObjectKey, value.AuditID, value.Status, "/operations/" + value.ID, resultJSON, metadataJSON, value.TerminalErrorCode, failureClass, "", string(relatedIDs), value.OperationID, string(evidence), value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken, value.ExpiresAt, value.CreatedAt, value.CreatedAt, "", value.UpdatedAt}
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

type reportExportPrepareScanner interface{ Scan(...any) error }

func scanReportExportPrepareReceipt(scanner reportExportPrepareScanner) (reportmodel.ReportExportPrepareReceipt, error) {
	var value reportmodel.ReportExportPrepareReceipt
	var systemPurpose, owner, kind, parentID, resourceType, statusURL string
	var resultJSON, metadataJSON, failureClass, nextAction, relatedIDs, correlation, evidence string
	var startedAt, finishedAt string
	err := scanner.Scan(
		&value.ID, &value.WorkspaceID, &systemPurpose, &owner, &kind, &value.UseCase, &parentID,
		&resourceType, &value.ReportKey, &value.OperationID, &value.RequestFingerprint,
		&value.RequesterUserID, &value.ObjectKey, &value.AuditID, &value.Status, &statusURL,
		&resultJSON, &metadataJSON, &value.TerminalErrorCode, &failureClass, &nextAction,
		&relatedIDs, &correlation, &evidence, &value.LeaseOwner, &value.LeaseExpiresAt,
		&value.FencingToken, &value.ExpiresAt, &value.CreatedAt, &startedAt, &finishedAt, &value.UpdatedAt,
	)
	if err != nil {
		return value, err
	}
	if systemPurpose != "" || owner != reportExportPrepareOwner || kind != reportExportPrepareKind || resourceType != "report" || correlation != value.OperationID {
		return value, fmt.Errorf("report export prepare operation identity is invalid")
	}
	var metadata reportExportPrepareMetadata
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return value, fmt.Errorf("decode report export prepare operation metadata: %w", err)
	}
	var result reportExportPrepareResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
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
