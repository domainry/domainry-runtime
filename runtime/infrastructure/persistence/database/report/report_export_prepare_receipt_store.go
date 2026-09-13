package report

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
)

const reportExportPrepareReceiptLeaseTTL = 5 * time.Minute

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
	if err := s.store.GuardSubjectEvidenceWrite(ctx, s.store.DB(), receipt.WorkspaceID, runtimeschema.ReportExportPrepareReceiptTable, columns, values); err != nil {
		return reportmodel.ReportExportPrepareClaimResult{}, err
	}
	statement, arguments, buildErr := query.NewWorkspaceInsertBuilder(s.store.SQLRenderer, runtimeschema.ReportExportPrepareReceiptTable, receipt.WorkspaceID).
		Columns(append(columns[:2], columns[3:]...)...).
		Values(append(values[:2], values[3:]...)...).
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
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, runtimeschema.ReportExportPrepareReceiptTable, requested.WorkspaceID).
		Set("id", requested.ID).
		Set("requester_user_id", requested.RequesterUserID).
		Set("idempotency_key", requested.CallerKey).
		Set("request_fingerprint", requested.RequestFingerprint).
		Set("status", string(idempotency.StatusProcessing)).
		Set("terminal_error_code", "").
		Set("lease_owner", requested.LeaseOwner).
		Set("lease_expires_at", leaseExpiresAt).
		SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).
		Set("updated_at", nowValue).
		Set("expires_at", "").
		Where(query.And(
			query.Equal("id", current.ID),
			query.Equal("operation_id", requested.OperationID),
			query.Equal("status", string(idempotency.StatusProcessing)),
			query.LessThanOrEqual("lease_expires_at", nowValue),
			query.Equal("payload_json", ""),
			query.Equal("business_job_key", ""),
			query.Equal("job_id", ""),
			query.Equal("completion_artifact_id", ""),
			query.Equal("completion_fingerprint", ""),
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
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, runtimeschema.ReportExportPrepareReceiptTable, requested.WorkspaceID).
		Set("status", string(idempotency.StatusProcessing)).
		Set("terminal_error_code", "").
		Set("lease_owner", requested.LeaseOwner).
		Set("lease_expires_at", leaseExpiresAt).
		SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).
		Set("updated_at", updatedAt).
		Set("expires_at", "").
		Where(query.And(
			query.Equal("operation_id", requested.OperationID),
			query.Equal("requester_user_id", requested.RequesterUserID),
			query.Equal("idempotency_key", requested.CallerKey),
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
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, runtimeschema.ReportExportPrepareReceiptTable, workspaceID).
		Set("payload_json", payloadJSON).
		Set("business_job_key", businessJobKey).
		Set("updated_at", now.Format(time.RFC3339Nano)).
		Where(query.And(
			query.Equal("id", receiptID),
			query.Equal("status", string(idempotency.StatusProcessing)),
			query.Equal("lease_owner", strings.TrimSpace(value.LeaseOwner)),
			query.Equal("fencing_token", value.FencingToken),
			query.Equal("payload_json", ""),
			query.Equal("job_id", ""),
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
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, runtimeschema.ReportExportPrepareReceiptTable, workspaceID).
		Set("status", string(idempotency.StatusSucceeded)).
		Set("job_id", jobID).
		Set("expires_at", value.ExpiresAt.UTC().Format(time.RFC3339Nano)).
		Set("updated_at", now.Format(time.RFC3339Nano)).
		Where(query.And(
			query.Equal("id", receiptID),
			query.Equal("status", string(idempotency.StatusProcessing)),
			query.Equal("lease_owner", strings.TrimSpace(value.LeaseOwner)),
			query.Equal("fencing_token", value.FencingToken),
			query.NotEqual("payload_json", ""),
			query.NotEqual("business_job_key", ""),
			query.Equal("job_id", ""),
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
	current, found, err := s.findReportExportPrepareByID(ctx, workspaceID, receiptID)
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
	condition := query.And(
		query.Equal("id", receiptID),
		query.Equal("status", string(idempotency.StatusProcessing)),
		query.Equal("lease_owner", leaseOwner),
		query.Equal("fencing_token", value.FencingToken),
	)
	statement, arguments, err := query.NewWorkspaceDeleteBuilder(s.store.SQLRenderer, runtimeschema.ReportExportPrepareReceiptTable, workspaceID).
		Where(query.And(condition, query.Equal("payload_json", ""), query.Equal("business_job_key", ""), query.Equal("job_id", ""))).Build()
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
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, runtimeschema.ReportExportPrepareReceiptTable, workspaceID).
		Set("status", string(status)).
		Set("terminal_error_code", errorCode).
		Set("lease_owner", "").
		Set("lease_expires_at", "").
		Set("updated_at", now.Format(time.RFC3339Nano)).
		Set("expires_at", value.ExpiresAt.UTC().Format(time.RFC3339Nano)).
		Where(query.And(
			query.Equal("id", receiptID),
			query.Equal("status", string(idempotency.StatusProcessing)),
			query.Equal("lease_owner", leaseOwner),
			query.Equal("fencing_token", value.FencingToken),
		)).Build()
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
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, runtimeschema.ReportExportPrepareReceiptTable, workspaceID).
		Set("status", string(idempotency.StatusSucceeded)).
		Set("job_id", strings.TrimSpace(binding.JobID)).
		Set("completion_artifact_id", strings.TrimSpace(binding.ArtifactID)).
		Set("completion_fingerprint", strings.TrimSpace(binding.CompletionFingerprint)).
		Set("expires_at", binding.ExpiresAt.UTC().Format(time.RFC3339Nano)).
		Set("updated_at", normalizedReportExportPrepareTime(binding.Now).Format(time.RFC3339Nano)).
		Where(query.And(
			query.Equal("id", receiptID),
			query.Equal("payload_json", payloadJSON),
			query.NotEqual("business_job_key", ""),
			query.Or(
				query.And(query.Equal("status", string(idempotency.StatusSucceeded)), query.Equal("job_id", strings.TrimSpace(binding.JobID))),
				query.And(query.Or(query.Equal("status", string(idempotency.StatusProcessing)), query.Equal("status", string(idempotency.StatusFailedRetryable))), query.Equal("job_id", "")),
			),
			query.Equal("completion_artifact_id", ""),
			query.Equal("completion_fingerprint", ""),
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
	current, found, err = s.findReportExportPrepareByID(ctx, workspaceID, receiptID)
	if err != nil {
		return reportmodel.ReportExportCompletionBindingResult{}, err
	}
	if !found || !reportExportCompletionOwnsReceipt(current, binding, payloadJSON) {
		return reportmodel.ReportExportCompletionBindingResult{Decision: reportmodel.ReportExportCompletionConflict, Receipt: current}, nil
	}
	if current.CompletionFingerprint != strings.TrimSpace(binding.CompletionFingerprint) || current.CompletionArtifactID != strings.TrimSpace(binding.ArtifactID) {
		return reportmodel.ReportExportCompletionBindingResult{Decision: reportmodel.ReportExportCompletionConflict, Receipt: current}, nil
	}
	decision := reportmodel.ReportExportCompletionReplay
	if rows == 1 {
		decision = reportmodel.ReportExportCompletionBound
	}
	return reportmodel.ReportExportCompletionBindingResult{Decision: decision, Receipt: current}, nil
}

func (s *ReportExportPrepareReceiptStore) findReportExportPrepareByOperation(ctx context.Context, workspaceID, operationID string) (reportmodel.ReportExportPrepareReceipt, bool, error) {
	return s.findReportExportPrepare(ctx, workspaceID, query.Equal("operation_id", strings.TrimSpace(operationID)))
}

func (s *ReportExportPrepareReceiptStore) findReportExportPrepareByID(ctx context.Context, workspaceID, receiptID string) (reportmodel.ReportExportPrepareReceipt, bool, error) {
	return s.findReportExportPrepare(ctx, workspaceID, query.Equal("id", strings.TrimSpace(receiptID)))
}

func (s *ReportExportPrepareReceiptStore) findReportExportPrepare(ctx context.Context, workspaceID string, predicate query.Predicate) (reportmodel.ReportExportPrepareReceipt, bool, error) {
	statement, arguments, err := query.NewWorkspaceSelectBuilder(s.store.SQLRenderer, runtimeschema.ReportExportPrepareReceiptTable, strings.TrimSpace(workspaceID)).
		Columns(reportExportPrepareReceiptColumns()...).Where(predicate).Limit(1).Build()
	if err != nil {
		return reportmodel.ReportExportPrepareReceipt{}, false, fmt.Errorf("build report export prepare receipt lookup: %w", err)
	}
	var value reportmodel.ReportExportPrepareReceipt
	err = s.store.DB().QueryRowContext(ctx, statement, arguments...).Scan(reportExportPrepareReceiptScanTargets(&value)...)
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
	return []string{
		"id", "operation_id", "workspace_id", "requester_user_id", "use_case", "report_key", "object_key", "audit_id", "idempotency_key", "request_fingerprint", "status", "payload_json", "business_job_key", "job_id", "completion_artifact_id", "completion_fingerprint", "terminal_error_code", "lease_owner", "lease_expires_at", "fencing_token", "created_at", "updated_at", "expires_at", "retry_of_job_id",
	}
}

func reportExportPrepareReceiptValues(value reportmodel.ReportExportPrepareReceipt) []any {
	return []any{
		value.ID, value.OperationID, value.WorkspaceID, value.RequesterUserID, value.UseCase, value.ReportKey, value.ObjectKey, value.AuditID, value.CallerKey, value.RequestFingerprint, value.Status, value.PayloadJSON, value.BusinessJobKey, value.JobID, value.CompletionArtifactID, value.CompletionFingerprint, value.TerminalErrorCode, value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken, value.CreatedAt, value.UpdatedAt, value.ExpiresAt, value.RetryOfJobID,
	}
}

func reportExportPrepareReceiptScanTargets(value *reportmodel.ReportExportPrepareReceipt) []any {
	return []any{
		&value.ID, &value.OperationID, &value.WorkspaceID, &value.RequesterUserID, &value.UseCase, &value.ReportKey, &value.ObjectKey, &value.AuditID, &value.CallerKey, &value.RequestFingerprint, &value.Status, &value.PayloadJSON, &value.BusinessJobKey, &value.JobID, &value.CompletionArtifactID, &value.CompletionFingerprint, &value.TerminalErrorCode, &value.LeaseOwner, &value.LeaseExpiresAt, &value.FencingToken, &value.CreatedAt, &value.UpdatedAt, &value.ExpiresAt, &value.RetryOfJobID,
	}
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
