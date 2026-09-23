package operations

import (
	"context"
	"fmt"
	"strings"

	sharedoperation "github.com/domainry/domainry-foundation/operation"
	"github.com/domainry/domainry-orm/query"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// SharedCommandStore is the deployment-neutral adapter over Runtime's
// canonical _operations ledger. Embedded modules use this port instead of
// creating owner-private idempotency receipt tables.
type SharedCommandStore struct {
	store *database.RuntimeStore
}

func NewSharedCommandStore(store *database.RuntimeStore) SharedCommandStore {
	return SharedCommandStore{store: store}
}

func (s SharedCommandStore) Claim(ctx context.Context, command sharedoperation.Command) (sharedoperation.Receipt, bool, error) {
	if s.store == nil || s.store.DB() == nil {
		return sharedoperation.Receipt{}, false, fmt.Errorf("shared Operation store is unavailable")
	}
	if err := command.Validate(); err != nil {
		return sharedoperation.Receipt{}, false, err
	}
	createdAt := command.CreatedAt.UTC()
	startedAt := createdAt
	receipt := operationsmodel.OperationsReceipt{
		Command: operationsmodel.OperationsCommand{
			ID: strings.TrimSpace(command.ID), Owner: strings.TrimSpace(command.Owner), Kind: strings.TrimSpace(command.Kind),
			ActionKey: strings.TrimSpace(command.ActionKey), Scope: operationsmodel.OperationsScope{
				WorkspaceID: strings.TrimSpace(command.Scope.WorkspaceID), SystemPurpose: strings.TrimSpace(command.Scope.SystemPurpose),
				ResourceType: strings.TrimSpace(command.Scope.ResourceType), ResourceID: strings.TrimSpace(command.Scope.ResourceID),
			},
			IdempotencyKey: strings.TrimSpace(command.IdempotencyKey), RequestFingerprint: strings.TrimSpace(command.RequestFingerprint),
			RequestedBy: strings.TrimSpace(command.RequestedBy), Reason: strings.TrimSpace(command.Reason), Reference: strings.TrimSpace(command.Reference),
			Status: operationsmodel.OperationsStatusStarted, CreatedAt: createdAt, StartedAt: &startedAt, UpdatedAt: createdAt,
		},
		StatusURL: strings.TrimSpace(command.StatusURL),
	}
	values, columns := operationsReceiptValues(receipt), operationsReceiptColumns()
	var statement string
	var args []any
	var err error
	if workspaceID := receipt.Command.Scope.WorkspaceID; workspaceID != "" {
		insertColumns := append(append([]string{}, columns[:1]...), columns[2:]...)
		insertValues := append(append([]any{}, values[:1]...), values[2:]...)
		builder, buildErr := s.store.SubjectEvidenceInsertBuilder(workspaceID, "_operations", insertColumns, insertValues)
		if buildErr != nil {
			return sharedoperation.Receipt{}, false, buildErr
		}
		statement, args, err = builder.Build()
	} else {
		statement, args, err = query.NewInsertBuilder(s.store.SQLRenderer, "_operations").Columns(columns...).Values(values...).Build()
	}
	if err != nil {
		return sharedoperation.Receipt{}, false, err
	}
	result, insertErr := s.store.DB().ExecContext(ctx, statement, args...)
	if insertErr == nil {
		affected, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return sharedoperation.Receipt{}, false, rowsErr
		}
		if affected != 1 {
			return sharedoperation.Receipt{}, false, fmt.Errorf("runtime.subject_erased")
		}
		return sharedOperationReceipt(receipt), true, nil
	}
	existing, found, readErr := NewOperationsStore(s.store).GetOperationsReceiptByKey(ctx, receipt.Command.Scope, receipt.Command.Kind, receipt.Command.IdempotencyKey)
	if readErr != nil || !found {
		if readErr != nil {
			return sharedoperation.Receipt{}, false, readErr
		}
		return sharedoperation.Receipt{}, false, insertErr
	}
	if strings.TrimSpace(existing.Command.RequestFingerprint) != strings.TrimSpace(command.RequestFingerprint) {
		return sharedoperation.Receipt{}, false, sharedoperation.ErrIdempotencyConflict
	}
	return sharedOperationReceipt(existing), false, nil
}

func (s SharedCommandStore) Complete(ctx context.Context, completion sharedoperation.Completion) error {
	if s.store == nil || s.store.DB() == nil {
		return fmt.Errorf("shared Operation store is unavailable")
	}
	if err := completion.Validate(); err != nil {
		return err
	}
	resultEvidence, err := inlineOperationsResult(completion.Result)
	if err != nil {
		return err
	}
	scope := operationsmodel.OperationsScope{WorkspaceID: strings.TrimSpace(completion.Scope.WorkspaceID), SystemPurpose: strings.TrimSpace(completion.Scope.SystemPurpose)}
	workspaceID, predicate, err := operationsScopePredicate(scope)
	if err != nil {
		return err
	}
	predicate = combineOperationsPredicate(predicate, query.And(
		query.Equal("id", strings.TrimSpace(completion.ID)),
		query.Equal("owner", strings.TrimSpace(completion.Owner)), query.Equal("kind", strings.TrimSpace(completion.Kind)),
		query.Equal("idempotency_key", strings.TrimSpace(completion.IdempotencyKey)),
		query.Equal("request_fingerprint", strings.TrimSpace(completion.RequestFingerprint)),
		query.Equal("status", string(operationsmodel.OperationsStatusStarted)),
	))
	builder := query.NewUpdateBuilder(s.store.SQLRenderer, "_operations")
	if workspaceID != "" {
		builder = query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_operations", workspaceID)
		predicate = combineOperationsPredicate(predicate, s.store.SubjectEvidenceWriteAllowed(workspaceID, "_operations", strings.TrimSpace(completion.ID)))
	}
	completedAt := completion.CompletedAt.UTC().Format(timeFormat)
	statement, args, err := builder.Set("status", string(operationsmodel.OperationsStatusSucceeded)).Set("result_json", string(resultEvidence)).
		Set("finished_at", completedAt).Set("updated_at", completedAt).Where(predicate).Build()
	if err != nil {
		return err
	}
	result, err := s.store.DB().ExecContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("shared Operation is not started")
	}
	return nil
}

func sharedOperationReceipt(receipt operationsmodel.OperationsReceipt) sharedoperation.Receipt {
	command := receipt.Command
	return sharedoperation.Receipt{
		Command: sharedoperation.Command{
			ID: command.ID, Scope: sharedoperation.Scope{
				WorkspaceID: command.Scope.WorkspaceID, SystemPurpose: command.Scope.SystemPurpose,
				ResourceType: command.Scope.ResourceType, ResourceID: command.Scope.ResourceID,
			},
			Owner: command.Owner, Kind: command.Kind, ActionKey: command.ActionKey,
			IdempotencyKey: command.IdempotencyKey, RequestFingerprint: command.RequestFingerprint,
			RequestedBy: command.RequestedBy, Reason: command.Reason, Reference: command.Reference,
			StatusURL: receipt.StatusURL, CreatedAt: command.CreatedAt,
		},
		Status: string(command.Status), Result: append([]byte(nil), receipt.Result...),
	}
}

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

var _ sharedoperation.Store = SharedCommandStore{}
