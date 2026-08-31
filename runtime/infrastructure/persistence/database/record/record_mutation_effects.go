package record

import (
	auditmoduleimpl "github.com/domainry/domainry-audit/module"
	"github.com/domainry/domainry-orm/query"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"

	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-foundation/telemetry"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	transactioncontract "github.com/domainry/domainry-runtime/runtime/domain/transaction/contract"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	"context"
	"encoding/json"
	"fmt"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"

	"strings"
	"time"

	runtimeauditmodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"

	notificationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notification"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/publicationhandoff"
)

func (r RecordStore) insertWorkflowIntentTx(ctx context.Context, tx TransactionExecutor, intent workflowmodel.WorkflowExecution) error {
	if strings.TrimSpace(intent.ID) == "" {
		return fmt.Errorf("mutation workflow intent requires deterministic id")
	}
	columns, values, err := workflowExecutionInsertValues(intent)
	if err != nil {
		return err
	}
	columns = append([]string{}, columns[1:]...)
	values = append([]any{}, values[1:]...)
	queryValue, args, buildErr := query.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "_workflow_executions", intent.WorkspaceID).Columns(columns...).Values(values...).Build()
	if buildErr != nil {
		return buildErr
	}
	if _, err := tx.ExecContext(ctx, queryValue, args...); err != nil {
		return fmt.Errorf("insert mutation workflow intent: %w", database.MutationConstraintError(err, "workflow_execution", intent.ID, mutation.MutationConflictIdempotency))
	}
	return nil
}

func workflowExecutionInsertValues(execution workflowmodel.WorkflowExecution) ([]string, []any, error) {
	action, err := json.Marshal(execution.Action)
	if err != nil {
		return nil, nil, fmt.Errorf("encode Workflow graph descriptor: %w", err)
	}
	payload, err := json.Marshal(execution.Payload)
	if err != nil {
		return nil, nil, fmt.Errorf("encode workflow payload: %w", err)
	}
	result, err := json.Marshal(execution.Result)
	if err != nil {
		return nil, nil, fmt.Errorf("encode workflow result: %w", err)
	}
	columns := []string{"workspace_id", "id", "workflow_key", "name", "trigger", "status", "action_type", "action_json", "payload_json", "result_json", "process_id", "node_id", "object_key", "record_id", "actor_id", "run_as", "idempotency_key", "attempt", "max_attempts", "next_run_at", "last_error", "message", "created_at", "updated_at"}
	values := []any{execution.WorkspaceID, execution.ID, execution.WorkflowKey, execution.Name, execution.Trigger, execution.Status, execution.ActionType, string(action), string(payload), string(result), execution.ProcessID, execution.NodeID, execution.ObjectKey, execution.RecordID, execution.ActorID, execution.RunAs, execution.IdempotencyKey, execution.Attempt, execution.MaxAttempts, database.NullableText(execution.NextRunAt), execution.LastError, execution.Message, execution.CreatedAt, execution.UpdatedAt}
	return columns, values, nil
}

func (r RecordStore) insertAuditEventTx(ctx context.Context, tx TransactionExecutor, event auditmodel.AuditEvent) error {
	if strings.TrimSpace(event.ID) == "" {
		return fmt.Errorf("mutation audit requires deterministic id")
	}
	err := auditmoduleimpl.AppendPreparedWithin(ctx, r.store.RuntimeRenderer(), runtimeauditmodule.NewTransaction(tx), event)
	if err != nil {
		return fmt.Errorf("insert mutation audit: %w", database.MutationConstraintError(err, "audit_event", event.ID, mutation.MutationConflictIdempotency))
	}
	return nil
}

func (r RecordStore) insertPublicationHandoffTx(ctx context.Context, tx TransactionExecutor, message publicationmodel.Message) error {
	s := r.store
	message.Payload = telemetry.EnsureAsyncPayload(ctx, message.Payload)
	now := time.Now().UTC().Format(time.RFC3339)
	message.WorkspaceID = publicationhandoff.WorkspaceID(message.WorkspaceID)
	message.DedupKey = strings.TrimSpace(message.DedupKey)
	if message.DedupKey == "" {
		return fmt.Errorf("mutation outbox requires stable dedup key")
	}
	if strings.TrimSpace(message.ID) == "" {
		message.ID = publicationhandoff.OutboxDedupID(message.WorkspaceID, message.ConnectorKey, message.ConnectionKey, message.Operation, message.DedupKey)
	}
	if strings.TrimSpace(message.Status) == "" {
		message.Status = "queued"
	}
	message.CreatedAt = now
	message.UpdatedAt = now
	payload, err := json.Marshal(database.NonNilMap(message.Payload))
	if err != nil {
		return fmt.Errorf("encode mutation outbox payload: %w", err)
	}
	queryValue, args, buildErr := query.NewWorkspaceInsertBuilder(s.SQLRenderer, "_publication_outbox", message.WorkspaceID).Columns("id", "publication_type", "connector_key", "connection_key", "operation", "status", "payload_json", "event_id", "request_ref", "dedup_key", "request_fingerprint", "response_ref", "error", "attempt_count", "next_attempt_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "created_by", "created_at", "updated_at").Values(message.ID, "integration.connector", message.ConnectorKey, message.ConnectionKey, message.Operation, message.Status, string(payload), message.EventID, message.RequestRef, message.DedupKey, message.RequestFingerprint, message.ResponseRef, message.Error, message.AttemptCount, message.NextAttemptAt, message.LastAttemptAt, message.LeaseOwner, message.LeaseExpiresAt, message.FencingToken, message.CreatedBy, message.CreatedAt, message.UpdatedAt).Build()
	if buildErr != nil {
		return buildErr
	}
	if _, err := tx.ExecContext(ctx, queryValue, args...); err != nil {
		return fmt.Errorf("insert mutation outbox: %w", database.MutationConstraintError(err, "runtime_publication_outbox", message.ID, mutation.MutationConflictIdempotency))
	}
	if err := publicationhandoff.RegisterWorkerScope(ctx, r.store, tx, message.WorkspaceID, now); err != nil {
		return err
	}
	if transactioncontract.ActiveTransaction(ctx) {
		locator := workerplatform.DurableTaskLocator{QueueKind: "runtime_publication_outbox", WorkspaceID: message.WorkspaceID, TaskID: message.ID}
		if err := transactioncontract.RegisterAfterCommit(ctx, transactioncontract.AfterCommitHook{
			Name: "wake integration outbox " + message.ID, Purpose: transactioncontract.AfterCommitDurableWorkWakeup, DurableRecovery: true,
			Run: func(context.Context) error { r.store.WorkerWakeups().Publish(locator); return nil },
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r RecordStore) PublishCommittedOutboxWakeups(_ context.Context, workspaceID string, commits []transactionmodel.RecordMutationCommit) {
	for _, commit := range commits {
		for _, message := range commit.Outbox {
			r.publishPublicationHandoffWakeup(workspaceID, message)
		}
	}
}

func (r RecordStore) PublishCommittedNotificationWakeups(ctx context.Context, workspaceID string, commits []transactionmodel.RecordMutationCommit) {
	notifications := notificationpersistence.NewInboxEventWriter(r.store)
	for _, commit := range commits {
		for _, event := range commit.NotificationEvents {
			event.WorkspaceID = workspaceID
			notifications.PublishCommittedEventWakeup(event)
		}
	}
}

func (r RecordStore) publishPublicationHandoffWakeup(workspaceID string, message publicationmodel.Message) {
	resolvedWorkspace := strings.TrimSpace(message.WorkspaceID)
	if len(resolvedWorkspace) == 0 {
		resolvedWorkspace = workspaceID
	}
	message.WorkspaceID = publicationhandoff.WorkspaceID(resolvedWorkspace)
	if strings.TrimSpace(message.ID) == "" {
		message.ID = publicationhandoff.OutboxDedupID(message.WorkspaceID, message.ConnectorKey, message.ConnectionKey, message.Operation, message.DedupKey)
	}
	r.store.WorkerWakeups().Publish(workerplatform.DurableTaskLocator{QueueKind: "runtime_publication_outbox", WorkspaceID: message.WorkspaceID, TaskID: message.ID})
}
