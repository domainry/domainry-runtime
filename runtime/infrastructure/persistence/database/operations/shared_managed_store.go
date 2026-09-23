package operations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	"github.com/domainry/domainry-orm/query"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// SharedManagedStore exposes Runtime's canonical _operations ledger to
// embedded owners that need an owner status machine plus a fenced worker
// lease. It never creates owner-private request or lock tables.
type SharedManagedStore struct{ store *database.RuntimeStore }

func NewSharedManagedStore(store *database.RuntimeStore) SharedManagedStore {
	return SharedManagedStore{store: store}
}

func (s SharedManagedStore) Create(ctx context.Context, value notificationmodulehost.ManagedOperation) error {
	if s.store == nil || s.store.DB() == nil {
		return fmt.Errorf("shared managed Operation store is unavailable")
	}
	if err := value.Validate(); err != nil {
		return err
	}
	resultEvidence, err := inlineOperationsResult(value.Result)
	if err != nil {
		return err
	}
	value.Result = resultEvidence
	receipt := managedReceipt(value)
	values, columns := operationsReceiptValues(receipt), operationsReceiptColumns()
	var statement string
	var args []any
	if workspaceID := strings.TrimSpace(value.Command.Scope.WorkspaceID); workspaceID != "" {
		insertColumns := append(append([]string{}, columns[:1]...), columns[2:]...)
		insertValues := append(append([]any{}, values[:1]...), values[2:]...)
		builder, buildErr := s.store.SubjectEvidenceInsertBuilder(workspaceID, "_operations", insertColumns, insertValues)
		if buildErr != nil {
			return buildErr
		}
		statement, args, err = builder.Build()
	} else {
		statement, args, err = query.NewInsertBuilder(s.store.SQLRenderer, "_operations").Columns(columns...).Values(values...).Build()
	}
	if err != nil {
		return err
	}
	executor := notificationmodulehost.OperationExecutorFromContext(ctx, s.store.DB())
	if _, err := executor.ExecContext(ctx, statement, args...); err != nil {
		if _, found, readErr := s.Get(ctx, managedIdentity(value)); readErr == nil && found {
			return notificationmodulehost.ErrManagedOperationIdentityConflict
		}
		return err
	}
	return nil
}

func (s SharedManagedStore) Get(ctx context.Context, identity notificationmodulehost.ManagedOperationIdentity) (notificationmodulehost.ManagedOperation, bool, error) {
	if s.store == nil || s.store.DB() == nil {
		return notificationmodulehost.ManagedOperation{}, false, fmt.Errorf("shared managed Operation store is unavailable")
	}
	if err := identity.Validate(); err != nil {
		return notificationmodulehost.ManagedOperation{}, false, err
	}
	workspaceID, predicate, err := operationsScopePredicate(operationScope(identity.Scope))
	if err != nil {
		return notificationmodulehost.ManagedOperation{}, false, err
	}
	predicate = combineOperationsPredicate(predicate, query.And(query.Equal("id", strings.TrimSpace(identity.ID)), query.Equal("owner", strings.TrimSpace(identity.Owner)), query.Equal("kind", strings.TrimSpace(identity.Kind))))
	statement, args, err := operationsSelectBuilder(s.store, workspaceID).Columns(operationsReceiptColumns()...).Where(predicate).Build()
	if err != nil {
		return notificationmodulehost.ManagedOperation{}, false, err
	}
	executor := notificationmodulehost.OperationExecutorFromContext(ctx, s.store.DB())
	receipt, err := operationsScanReceipt(executor.QueryRowContext(ctx, statement, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return notificationmodulehost.ManagedOperation{}, false, nil
	}
	return operationManaged(receipt), err == nil, err
}

func (s SharedManagedStore) List(ctx context.Context, filter notificationmodulehost.ManagedOperationQuery) ([]notificationmodulehost.ManagedOperation, error) {
	if s.store == nil || s.store.DB() == nil {
		return nil, fmt.Errorf("shared managed Operation store is unavailable")
	}
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	workspaceID, predicate, err := operationsScopePredicate(operationScope(filter.Scope))
	if err != nil {
		return nil, err
	}
	predicate = combineOperationsPredicate(predicate, query.And(query.Equal("owner", strings.TrimSpace(filter.Owner)), query.Equal("kind", strings.TrimSpace(filter.Kind))))
	if resourceID := strings.TrimSpace(filter.ResourceID); resourceID != "" {
		predicate = combineOperationsPredicate(predicate, query.Equal("resource_id", resourceID))
	}
	if len(filter.Statuses) != 0 {
		statuses := make([]any, len(filter.Statuses))
		for index, status := range filter.Statuses {
			statuses[index] = strings.TrimSpace(status)
		}
		predicate = combineOperationsPredicate(predicate, query.In("status", statuses...))
	}
	if value := strings.TrimSpace(filter.NextActionBefore); value != "" {
		predicate = combineOperationsPredicate(predicate, query.And(query.NotEqual("next_action", ""), query.LessThanOrEqual("next_action", value)))
	}
	if value := strings.TrimSpace(filter.LeaseExpiresBefore); value != "" {
		predicate = combineOperationsPredicate(predicate, query.And(query.NotEqual("lease_expires_at", ""), query.LessThanOrEqual("lease_expires_at", value)))
	}
	builder := operationsSelectBuilder(s.store, workspaceID).Columns(operationsReceiptColumns()...).Where(predicate).Limit(filter.Limit)
	if filter.OldestFirst {
		builder = builder.OrderBy(query.Ascending("next_action"), query.Ascending("created_at"), query.Ascending("id"))
	} else {
		builder = builder.OrderBy(query.Descending("created_at"), query.Descending("id"))
	}
	statement, args, err := builder.Build()
	if err != nil {
		return nil, err
	}
	executor := notificationmodulehost.OperationExecutorFromContext(ctx, s.store.DB())
	rows, err := executor.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []notificationmodulehost.ManagedOperation{}
	for rows.Next() {
		receipt, scanErr := operationsScanReceipt(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, operationManaged(receipt))
	}
	return values, rows.Err()
}

func (s SharedManagedStore) Transition(ctx context.Context, transition notificationmodulehost.ManagedOperationTransition) (notificationmodulehost.ManagedOperation, bool, error) {
	if err := transition.Validate(); err != nil {
		return notificationmodulehost.ManagedOperation{}, false, err
	}
	resultEvidence, err := inlineOperationsResult(transition.Result)
	if err != nil {
		return notificationmodulehost.ManagedOperation{}, false, err
	}
	transition.Result = resultEvidence
	current, found, err := s.Get(ctx, transition.Identity)
	if err != nil || !found {
		return notificationmodulehost.ManagedOperation{}, false, err
	}
	workspaceID, predicate, err := operationsScopePredicate(operationScope(transition.Identity.Scope))
	if err != nil {
		return notificationmodulehost.ManagedOperation{}, false, err
	}
	predicate = combineOperationsPredicate(predicate, query.And(query.Equal("id", transition.Identity.ID), query.Equal("owner", transition.Identity.Owner), query.Equal("kind", transition.Identity.Kind), query.Equal("status", transition.ExpectedStatus)))
	if transition.ExpectedFencingToken > 0 {
		predicate = combineOperationsPredicate(predicate, query.And(query.Equal("lease_owner", transition.ExpectedLeaseOwner), query.Equal("fencing_token", transition.ExpectedFencingToken)))
	}
	builder := query.NewUpdateBuilder(s.store.SQLRenderer, "_operations")
	if workspaceID != "" {
		builder = query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_operations", workspaceID)
		predicate = combineOperationsPredicate(predicate, s.store.SubjectEvidenceWriteAllowed(workspaceID, "_operations", transition.Identity.ID))
		predicate = combineOperationsPredicate(predicate, s.store.SubjectActorWriteAllowed(workspaceID, current.Command.RequestedBy))
	}
	builder = builder.Set("status", transition.Status).Set("metadata_json", string(transition.Metadata)).Set("result_json", string(transition.Result)).Set("error_code", transition.ErrorCode).Set("next_action", transition.NextAction).Set("updated_at", transition.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if transition.ClearLease {
		builder = builder.Set("lease_owner", "").Set("lease_expires_at", "")
	}
	statement, args, err := builder.Where(predicate).Build()
	if err != nil {
		return notificationmodulehost.ManagedOperation{}, false, err
	}
	executor := notificationmodulehost.OperationExecutorFromContext(ctx, s.store.DB())
	result, err := executor.ExecContext(ctx, statement, args...)
	if err != nil {
		return notificationmodulehost.ManagedOperation{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return notificationmodulehost.ManagedOperation{}, false, err
	}
	value, found, err := s.Get(ctx, transition.Identity)
	return value, found, err
}

func (s SharedManagedStore) Claim(ctx context.Context, claim notificationmodulehost.ManagedOperationClaim) (notificationmodulehost.ManagedOperation, bool, error) {
	if err := claim.Validate(); err != nil {
		return notificationmodulehost.ManagedOperation{}, false, err
	}
	current, found, err := s.Get(ctx, claim.Identity)
	if err != nil || !found {
		return notificationmodulehost.ManagedOperation{}, false, err
	}
	workspaceID, predicate, err := operationsScopePredicate(operationScope(claim.Identity.Scope))
	if err != nil {
		return notificationmodulehost.ManagedOperation{}, false, err
	}
	due := query.Or(
		query.And(query.Equal("status", claim.DueStatus), query.NotEqual("next_action", ""), query.LessThanOrEqual("next_action", claim.Now)),
		query.And(query.Equal("status", claim.ReclaimStatus), query.NotEqual("lease_expires_at", ""), query.LessThanOrEqual("lease_expires_at", claim.Now)),
	)
	predicate = combineOperationsPredicate(predicate, query.And(query.Equal("id", claim.Identity.ID), query.Equal("owner", claim.Identity.Owner), query.Equal("kind", claim.Identity.Kind), due))
	builder := query.NewUpdateBuilder(s.store.SQLRenderer, "_operations")
	if workspaceID != "" {
		builder = query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_operations", workspaceID)
		predicate = combineOperationsPredicate(predicate, s.store.SubjectEvidenceWriteAllowed(workspaceID, "_operations", claim.Identity.ID))
		predicate = combineOperationsPredicate(predicate, s.store.SubjectActorWriteAllowed(workspaceID, current.Command.RequestedBy))
	}
	statement, args, err := builder.Set("status", claim.Status).Set("lease_owner", claim.LeaseOwner).Set("lease_expires_at", claim.LeaseExpiresAt).
		SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).Set("updated_at", claim.UpdatedAt.UTC().Format(time.RFC3339Nano)).Where(predicate).Build()
	if err != nil {
		return notificationmodulehost.ManagedOperation{}, false, err
	}
	executor := notificationmodulehost.OperationExecutorFromContext(ctx, s.store.DB())
	result, err := executor.ExecContext(ctx, statement, args...)
	if err != nil {
		return notificationmodulehost.ManagedOperation{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return notificationmodulehost.ManagedOperation{}, false, err
	}
	value, found, err := s.Get(ctx, claim.Identity)
	return value, found, err
}

func managedReceipt(value notificationmodulehost.ManagedOperation) operationsmodel.OperationsReceipt {
	command := value.Command
	return operationsmodel.OperationsReceipt{
		Command: operationsmodel.OperationsCommand{
			ID: command.ID, Owner: command.Owner, Kind: command.Kind, ActionKey: command.ActionKey,
			Scope: operationScope(command.Scope), IdempotencyKey: command.IdempotencyKey, RequestFingerprint: command.RequestFingerprint,
			RequestedBy: command.RequestedBy, Reason: command.Reason, Reference: command.Reference,
			Status: operationsmodel.OperationsStatus(value.Status), CreatedAt: command.CreatedAt, UpdatedAt: value.UpdatedAt,
		},
		StatusURL: command.StatusURL, Result: value.Result, Metadata: value.Metadata, ErrorCode: value.ErrorCode,
		NextAction: value.NextAction, LeaseOwner: value.LeaseOwner, LeaseExpires: value.LeaseExpiresAt, FencingToken: value.FencingToken,
	}
}

func operationManaged(receipt operationsmodel.OperationsReceipt) notificationmodulehost.ManagedOperation {
	command := receipt.Command
	return notificationmodulehost.ManagedOperation{
		Command: notificationmodulehost.OperationCommand{
			ID: command.ID, Scope: notificationmodulehost.OperationScope{WorkspaceID: command.Scope.WorkspaceID, SystemPurpose: command.Scope.SystemPurpose, ResourceType: command.Scope.ResourceType, ResourceID: command.Scope.ResourceID},
			Owner: command.Owner, Kind: command.Kind, ActionKey: command.ActionKey, IdempotencyKey: command.IdempotencyKey,
			RequestFingerprint: command.RequestFingerprint, RequestedBy: command.RequestedBy, Reason: command.Reason, Reference: command.Reference,
			StatusURL: receipt.StatusURL, CreatedAt: command.CreatedAt,
		},
		Status: string(command.Status), Metadata: receipt.Metadata, Result: receipt.Result, ErrorCode: receipt.ErrorCode,
		NextAction: receipt.NextAction, LeaseOwner: receipt.LeaseOwner, LeaseExpiresAt: receipt.LeaseExpires, FencingToken: receipt.FencingToken, UpdatedAt: command.UpdatedAt,
	}
}

func operationScope(scope notificationmodulehost.OperationScope) operationsmodel.OperationsScope {
	return operationsmodel.OperationsScope{WorkspaceID: scope.WorkspaceID, SystemPurpose: scope.SystemPurpose, ResourceType: scope.ResourceType, ResourceID: scope.ResourceID}
}

func managedIdentity(value notificationmodulehost.ManagedOperation) notificationmodulehost.ManagedOperationIdentity {
	return notificationmodulehost.ManagedOperationIdentity{ID: value.Command.ID, Scope: value.Command.Scope, Owner: value.Command.Owner, Kind: value.Command.Kind}
}

var _ notificationmodulehost.ManagedOperationStore = SharedManagedStore{}
