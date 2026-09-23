package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	sharedoperation "github.com/domainry/domainry-foundation/operation"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationspolicy "github.com/domainry/domainry-runtime/runtime/domain/operations/policy"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

var _ operationsrepository.OperationsRepository = OperationsStore{}

type OperationsStore struct {
	store     *database.RuntimeStore
	db        *sql.DB
	readiness func() database.DatabaseReadiness
	faults    workerplatform.FaultInjector
}

func (s OperationsStore) databaseReadiness() database.DatabaseReadiness {
	if s.readiness != nil {
		return s.readiness()
	}
	return s.store.DatabaseReadiness()
}

func NewOperationsStore(store *database.RuntimeStore) OperationsStore {
	return OperationsStore{store: store, faults: workerplatform.NoopFaultInjector{}}
}

func NewOperationsStoreWithFaults(store *database.RuntimeStore, faults workerplatform.FaultInjector) OperationsStore {
	if faults == nil {
		faults = workerplatform.NoopFaultInjector{}
	}
	return OperationsStore{store: store, faults: faults}
}

func (s OperationsStore) database() *sql.DB {
	if s.db != nil {
		return s.db
	}
	if s.store == nil {
		return nil
	}
	return s.store.DB()
}

func (s OperationsStore) ledger() (*sharedoperation.SQLStore, error) {
	if s.database() == nil || s.store == nil {
		return nil, fmt.Errorf("operations store unavailable")
	}
	return sharedoperation.NewSQLStore(s.database(), s.store.SQLRenderer), nil
}

type operationsScanner interface{ Scan(...any) error }

func (s OperationsStore) RegisterOperationsCommand(ctx context.Context, receipt operationsmodel.OperationsReceipt) (operationsmodel.OperationsReceipt, operationsmodel.OperationsSubmissionDecision, error) {
	ledger, err := s.ledger()
	if err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	if err := workerplatform.CheckFault(ctx, s.faults, workerplatform.FaultTransactionBeforeBegin); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	tx, err := s.database().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	defer func() { _ = tx.Rollback() }()
	if err := workerplatform.CheckFault(ctx, s.faults, workerplatform.FaultTransactionAfterBegin); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	if workspaceID, workspaceErr := principalmodel.NewWorkspaceID(receipt.Command.Scope.WorkspaceID); workspaceErr == nil {
		if err := s.store.GuardSubjectEvidenceWrite(ctx, tx, workspaceID.String(), sharedoperation.TableName,
			[]string{"id", "owner", "resource_type", "resource_id", "requested_by"},
			[]any{receipt.Command.ID, receipt.Command.Owner, receipt.Command.Scope.ResourceType, receipt.Command.Scope.ResourceID, receipt.Command.RequestedBy}); err != nil {
			return operationsmodel.OperationsReceipt{}, "", err
		}
	}
	if err := workerplatform.CheckFault(ctx, s.faults, workerplatform.FaultTransactionBeforeWrite); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	inserted, err := ledger.InsertRecord(sharedoperation.WithExecutor(ctx, tx), operationsRecord(receipt))
	if err != nil {
		_ = tx.Rollback()
		if replay, replayFound, replayErr := s.GetOperationsReceiptByKey(ctx, receipt.Command.Scope, receipt.Command.Kind, receipt.Command.IdempotencyKey); replayErr == nil && replayFound {
			return replay, operationspolicy.OperationsClassifySubmission(&replay, receipt.Command), nil
		}
		return operationsmodel.OperationsReceipt{}, "", err
	}
	if !inserted {
		return operationsmodel.OperationsReceipt{}, "", fmt.Errorf("runtime.subject_erased")
	}
	if err := workerplatform.CheckFault(ctx, s.faults, workerplatform.FaultTransactionAfterWrite); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	if err := workerplatform.CheckFault(ctx, s.faults, workerplatform.FaultTransactionBeforeCommit); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	if err := workerplatform.CheckFault(ctx, s.faults, workerplatform.FaultTransactionAfterCommit); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	return receipt, operationsmodel.OperationsSubmissionAccepted, nil
}

func (s OperationsStore) GetOperationsReceipt(ctx context.Context, scope operationsmodel.OperationsScope, id string) (operationsmodel.OperationsReceipt, bool, error) {
	filter, err := operationsRecordFilter(scope)
	if err != nil {
		return operationsmodel.OperationsReceipt{}, false, err
	}
	filter.ID = strings.TrimSpace(id)
	return s.getRecord(ctx, filter)
}

func (s OperationsStore) GetOperationsReceiptByKey(ctx context.Context, scope operationsmodel.OperationsScope, kind, key string) (operationsmodel.OperationsReceipt, bool, error) {
	filter, err := operationsRecordFilter(scope)
	if err != nil {
		return operationsmodel.OperationsReceipt{}, false, err
	}
	filter.Kind, filter.IdempotencyKey = strings.TrimSpace(kind), strings.TrimSpace(key)
	return s.getRecord(ctx, filter)
}

func (s OperationsStore) ListOperationsReceipts(ctx context.Context, scope operationsmodel.OperationsScope, status operationsmodel.OperationsStatus, limit int) ([]operationsmodel.OperationsReceipt, error) {
	ledger, err := s.ledger()
	if err != nil {
		return nil, err
	}
	filter, err := operationsRecordFilter(scope)
	if err != nil {
		return nil, err
	}
	filter.Status, filter.Limit = string(status), limit
	records, err := ledger.ListRecords(ctx, filter)
	if err != nil {
		return nil, err
	}
	result := make([]operationsmodel.OperationsReceipt, 0, len(records))
	for _, record := range records {
		receipt, mapErr := operationsReceipt(record)
		if mapErr != nil {
			return nil, mapErr
		}
		result = append(result, receipt)
	}
	return result, nil
}

func (s OperationsStore) SearchOperationsReceipts(ctx context.Context, scope operationsmodel.OperationsScope, filter operationsmodel.OperationsReceiptFilter) (operationsmodel.OperationsReceiptPage, error) {
	ledger, err := s.ledger()
	if err != nil {
		return operationsmodel.OperationsReceiptPage{}, err
	}
	recordFilter, err := operationsRecordFilter(scope)
	if err != nil {
		return operationsmodel.OperationsReceiptPage{}, err
	}
	recordFilter.Status = string(filter.Status)
	recordFilter.FailureClass = string(filter.FailureClass)
	recordFilter.Owner, recordFilter.Kind, recordFilter.ParentID = filter.Owner, filter.Kind, filter.ParentID
	recordFilter.ResourceType, recordFilter.ResourceID = filter.ResourceType, filter.ResourceID
	recordFilter.RequestedBy, recordFilter.Correlation = filter.RequestedBy, filter.Correlation
	recordFilter.CreatedFrom, recordFilter.CreatedTo, recordFilter.Search, recordFilter.Limit = filter.CreatedFrom, filter.CreatedTo, filter.Search, filter.Limit
	page, err := ledger.SearchRecords(ctx, recordFilter, true)
	if err != nil {
		return operationsmodel.OperationsReceiptPage{}, err
	}
	result := operationsmodel.OperationsReceiptPage{
		Items: make([]operationsmodel.OperationsReceipt, 0, len(page.Items)), Count: page.Count,
		Summary: operationsmodel.OperationsReceiptSummary{
			Created:            page.Summary.Statuses[string(operationsmodel.OperationsStatusCreated)],
			Started:            page.Summary.Statuses[string(operationsmodel.OperationsStatusStarted)],
			Succeeded:          page.Summary.Statuses[string(operationsmodel.OperationsStatusSucceeded)],
			Failed:             page.Summary.Statuses[string(operationsmodel.OperationsStatusFailed)],
			ManualIntervention: page.Summary.FailureClasses[string(operationsmodel.OperationsFailureManualIntervention)],
		},
	}
	for _, record := range page.Items {
		receipt, mapErr := operationsReceipt(record)
		if mapErr != nil {
			return operationsmodel.OperationsReceiptPage{}, mapErr
		}
		result.Items = append(result.Items, receipt)
	}
	return result, nil
}

func (s OperationsStore) UpdateOperationsReceipt(ctx context.Context, receipt operationsmodel.OperationsReceipt, expected operationsmodel.OperationsStatus) (bool, error) {
	ledger, err := s.ledger()
	if err != nil {
		return false, err
	}
	record := operationsRecord(receipt)
	if workspaceID, workspaceErr := principalmodel.NewWorkspaceID(receipt.Command.Scope.WorkspaceID); workspaceErr == nil {
		tx, beginErr := s.database().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if beginErr != nil {
			return false, beginErr
		}
		defer func() { _ = tx.Rollback() }()
		if err := s.store.GuardSubjectEvidenceWrite(ctx, tx, workspaceID.String(), sharedoperation.TableName,
			[]string{"id", "owner", "resource_type", "resource_id", "requested_by"},
			[]any{record.ID, record.Owner, record.ResourceType, record.ResourceID, record.RequestedBy}); err != nil {
			return false, err
		}
		changed, updateErr := ledger.UpdateRecord(sharedoperation.WithExecutor(ctx, tx), record, string(expected))
		if updateErr != nil || !changed {
			return changed, updateErr
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	return ledger.UpdateRecord(ctx, record, string(expected))
}

func (s OperationsStore) getRecord(ctx context.Context, filter sharedoperation.RecordFilter) (operationsmodel.OperationsReceipt, bool, error) {
	ledger, err := s.ledger()
	if err != nil {
		return operationsmodel.OperationsReceipt{}, false, err
	}
	record, found, err := ledger.GetRecord(ctx, filter)
	if err != nil || !found {
		return operationsmodel.OperationsReceipt{}, found, err
	}
	receipt, err := operationsReceipt(record)
	return receipt, err == nil, err
}

func operationsRecordFilter(scope operationsmodel.OperationsScope) (sharedoperation.RecordFilter, error) {
	if workspaceID, err := principalmodel.NewWorkspaceID(scope.WorkspaceID); err == nil {
		return sharedoperation.RecordFilter{WorkspaceID: workspaceID.String()}, nil
	}
	if systemPurpose := strings.TrimSpace(scope.SystemPurpose); systemPurpose != "" {
		return sharedoperation.RecordFilter{SystemPurpose: systemPurpose}, nil
	}
	return sharedoperation.RecordFilter{}, fmt.Errorf("operations scope requires workspace_id or system_purpose")
}

func operationsRecord(receipt operationsmodel.OperationsReceipt) sharedoperation.Record {
	resultJSON := receipt.Result
	if len(resultJSON) == 0 {
		resultJSON = json.RawMessage(`{}`)
	}
	metadataJSON := receipt.Metadata
	if len(metadataJSON) == 0 {
		metadataJSON = json.RawMessage(`{}`)
	}
	relatedJSON, _ := json.Marshal(receipt.RelatedIDs)
	evidenceJSON, _ := json.Marshal(receipt.Evidence)
	startedAt, finishedAt := "", ""
	if receipt.Command.StartedAt != nil {
		startedAt = receipt.Command.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if receipt.Command.FinishedAt != nil {
		finishedAt = receipt.Command.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	return sharedoperation.Record{
		ID: receipt.Command.ID, WorkspaceID: receipt.Command.Scope.WorkspaceID, SystemPurpose: receipt.Command.Scope.SystemPurpose,
		Owner: receipt.Command.Owner, Kind: receipt.Command.Kind, ActionKey: receipt.Command.ActionKey, ParentID: receipt.Command.ParentID,
		ResourceType: receipt.Command.Scope.ResourceType, ResourceID: receipt.Command.Scope.ResourceID,
		IdempotencyKey: receipt.Command.IdempotencyKey, RequestFingerprint: receipt.Command.RequestFingerprint,
		RequestedBy: receipt.Command.RequestedBy, Reason: receipt.Command.Reason, Reference: receipt.Command.Reference,
		Status: string(receipt.Command.Status), StatusURL: receipt.StatusURL, ResultJSON: resultJSON, MetadataJSON: metadataJSON,
		ErrorCode: receipt.ErrorCode, FailureClass: string(receipt.FailureClass), NextAction: receipt.NextAction,
		RelatedIDsJSON: relatedJSON, Correlation: receipt.Correlation, EvidenceJSON: evidenceJSON,
		LeaseOwner: receipt.LeaseOwner, LeaseExpiresAt: receipt.LeaseExpires, FencingToken: receipt.FencingToken, ExpiresAt: receipt.ExpiresAt,
		CreatedAt: receipt.Command.CreatedAt.UTC().Format(time.RFC3339Nano), StartedAt: startedAt, FinishedAt: finishedAt,
		UpdatedAt: receipt.Command.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func operationsReceipt(record sharedoperation.Record) (operationsmodel.OperationsReceipt, error) {
	receipt := operationsmodel.OperationsReceipt{
		Command: operationsmodel.OperationsCommand{
			ID: record.ID, Owner: record.Owner, Kind: record.Kind, ActionKey: record.ActionKey, ParentID: record.ParentID,
			Scope:          operationsmodel.OperationsScope{WorkspaceID: record.WorkspaceID, SystemPurpose: record.SystemPurpose, ResourceType: record.ResourceType, ResourceID: record.ResourceID},
			IdempotencyKey: record.IdempotencyKey, RequestFingerprint: record.RequestFingerprint, RequestedBy: record.RequestedBy,
			Reason: record.Reason, Reference: record.Reference, Status: operationsmodel.OperationsStatus(record.Status),
		},
		StatusURL: record.StatusURL, Result: append(json.RawMessage(nil), record.ResultJSON...), Metadata: append(json.RawMessage(nil), record.MetadataJSON...),
		ErrorCode: record.ErrorCode, FailureClass: operationsmodel.OperationsFailureClass(record.FailureClass), NextAction: record.NextAction,
		Correlation: record.Correlation, LeaseOwner: record.LeaseOwner, LeaseExpires: record.LeaseExpiresAt,
		FencingToken: record.FencingToken, ExpiresAt: record.ExpiresAt,
	}
	if err := json.Unmarshal(record.RelatedIDsJSON, &receipt.RelatedIDs); err != nil {
		return operationsmodel.OperationsReceipt{}, err
	}
	if err := json.Unmarshal(record.EvidenceJSON, &receipt.Evidence); err != nil {
		return operationsmodel.OperationsReceipt{}, err
	}
	var err error
	receipt.Command.CreatedAt, err = time.Parse(time.RFC3339Nano, record.CreatedAt)
	if err != nil {
		return operationsmodel.OperationsReceipt{}, err
	}
	receipt.Command.UpdatedAt, err = time.Parse(time.RFC3339Nano, record.UpdatedAt)
	if err != nil {
		return operationsmodel.OperationsReceipt{}, err
	}
	if record.StartedAt != "" {
		value, parseErr := time.Parse(time.RFC3339Nano, record.StartedAt)
		if parseErr != nil {
			return operationsmodel.OperationsReceipt{}, parseErr
		}
		receipt.Command.StartedAt = &value
	}
	if record.FinishedAt != "" {
		value, parseErr := time.Parse(time.RFC3339Nano, record.FinishedAt)
		if parseErr != nil {
			return operationsmodel.OperationsReceipt{}, parseErr
		}
		receipt.Command.FinishedAt = &value
	}
	return receipt, nil
}
