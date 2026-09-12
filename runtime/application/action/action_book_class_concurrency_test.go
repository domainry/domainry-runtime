package action

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionruntime "github.com/domainry/domainry-runtime/runtime/domain/action/runtime"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	actionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/action"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type concurrentBookClassHandler struct {
	descriptor runtimeext.HandlerDescriptor
}

func (h concurrentBookClassHandler) Descriptor() runtimeext.HandlerDescriptor {
	return h.descriptor
}

func (h concurrentBookClassHandler) Invoke(ctx context.Context, execution runtimeext.ActionExecution, _ json.RawMessage) (json.RawMessage, error) {
	locked, err := execution.QueryRecords(ctx, runtimeext.RecordQuery{
		Operation: runtimeext.QueryGetForUpdate, ObjectKey: "concurrent_group_class", RecordID: "class-1",
	})
	if err != nil {
		return nil, err
	}
	if len(locked.Records) != 1 {
		return nil, fmt.Errorf("locked class count=%d", len(locked.Records))
	}
	capacity, capacityOK := locked.Records[0].Fields["remaining_capacity"].(int64)
	_, waitlistOK := locked.Records[0].Fields["remaining_waitlist_capacity"].(int64)
	if !capacityOK || !waitlistOK {
		return nil, fmt.Errorf("locked class capacities=%#v", locked.Records[0].Fields)
	}
	field, code, status := "remaining_capacity", "gym.class_capacity_full", "booked"
	if capacity < 1 {
		field, code, status = "remaining_waitlist_capacity", "gym.class_waitlist_full", "waitlisted"
	}
	if _, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{
		Operation: runtimeext.MutationConditionalUpdate, ObjectKey: "concurrent_group_class", RecordID: "class-1",
		Predicates: []runtimeext.Predicate{{Field: field, Operator: "gte", Value: int64(1), ErrorCode: code}},
		Arithmetic: []runtimeext.Arithmetic{{Field: field, Operation: "decrement", Operand: int64(1)}},
	}); err != nil {
		return nil, err
	}
	memberID := execution.Principal().UserID
	booking, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{
		Operation: runtimeext.MutationCreate, ObjectKey: "concurrent_class_booking",
		Fields: map[string]any{"class_id": "class-1", "member_id": memberID, "status": status},
	})
	if err != nil {
		return nil, err
	}
	if _, err := execution.StageDurableIntent(ctx, runtimeext.DurableIntent{
		ConsumerKey: "member_center", ConnectionKey: "primary", OperationKey: "send_notice",
		ContractSHA256: strings.Repeat("c", 64),
		Payload:        map[string]any{"member_id": memberID, "sequence": int64(1)},
	}); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"booking_id": booking.Record.ID, "status": status})
}

func TestBookClassHundredConcurrentRequestsDoNotOversellAndIdempotentRetryDoesNotDuplicateFacts(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "book-class-concurrency.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE concurrent_group_class (
		workspace_id TEXT NOT NULL,
		id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		remaining_capacity INTEGER NOT NULL,
		remaining_waitlist_capacity INTEGER NOT NULL,
		UNIQUE (workspace_id, id)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE concurrent_class_booking (
		workspace_id TEXT NOT NULL,
		id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		class_id TEXT NOT NULL,
		member_id TEXT NOT NULL,
		status TEXT NOT NULL,
		UNIQUE (workspace_id, id)
	)`); err != nil {
		t.Fatal(err)
	}
	groupClass := definitionmodel.ObjectSchema{Key: "concurrent_group_class", Fields: []definitionmodel.FieldSchema{
		{Key: "remaining_capacity", Type: "integer"},
		{Key: "remaining_waitlist_capacity", Type: "integer"},
	}}
	classBooking := definitionmodel.ObjectSchema{Key: "concurrent_class_booking", Fields: []definitionmodel.FieldSchema{
		{Key: "class_id", Type: "relation"},
		{Key: "member_id", Type: "relation"},
		{Key: "status", Type: "select"},
	}}
	records := recordpersistence.NewRecordStore(store)
	stamp := "2026-07-23T12:00:00Z"
	if err := records.InsertRecord(t.Context(), "workspace-a", groupClass, recordmodel.Record{
		ID: "class-1", CreatedAt: stamp, UpdatedAt: stamp,
		Data: map[string]any{"remaining_capacity": int64(10), "remaining_waitlist_capacity": int64(20)},
	}); err != nil {
		t.Fatal(err)
	}

	connectorGrant := runtimeext.ActionConnectorCapability{
		ConnectorKey: "member_center", ConnectionKey: "primary", OperationKey: "send_notice",
		ContractSHA256: strings.Repeat("c", 64), Mode: runtimeext.ConnectorModeEnqueue, Effect: runtimeext.ConnectorEffectWrite,
	}
	handler := concurrentBookClassHandler{descriptor: actionTestHandlerDescriptor(
		"group_class.book_class",
		[]runtimeext.ActionObjectCapability{
			{ObjectKey: groupClass.Key, Operations: []string{"get_for_update", "conditional_update"}},
			{ObjectKey: classBooking.Key, Operations: []string{"create"}},
		},
		connectorGrant,
	)}
	registry := runtimeext.NewBusinessHandlerRegistry()
	if err := registry.Register(handler); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	action := actionTestPublishedContract(definitionmodel.ActionSchema{
		Key: "group_class.book_class", ObjectKey: groupClass.Key, Kind: definitionmodel.ActionKindObjectOperation,
		AuditEvent: "gym.class_booked",
		EffectSet: &definitionmodel.ActionEffectSet{
			Read: []definitionmodel.ActionObjectEffect{{
				ObjectKey: groupClass.Key, Operations: []string{"get_for_update"},
				Fields: []string{"remaining_capacity", "remaining_waitlist_capacity"},
			}},
			Write: []definitionmodel.ActionObjectEffect{
				{
					ObjectKey: groupClass.Key, Operations: []string{"conditional_update"},
					Fields: []string{"remaining_capacity", "remaining_waitlist_capacity"},
				},
				{
					ObjectKey: classBooking.Key, Operations: []string{"create"},
					Fields: []string{"class_id", "member_id", "status"},
				},
			},
		},
	})
	var revision atomic.Int64
	newPlan := func(principal principalmodel.Principal, operation string, object definitionmodel.ObjectSchema, record recordmodel.Record, predicates []transactionmodel.MutationPredicate, audit auditmodel.AuditEvent) (transactionmodel.MutationPlan, error) {
		mutationContext, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{
			WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID, RoleKey: principal.RoleKey,
			Source: transactionmodel.MutationSourceAction, ActionKey: action.Key,
			CorrelationID: principal.RequestID, ApplicationSchemaRevision: "snapshot-1",
		})
		if err != nil {
			return transactionmodel.MutationPlan{}, err
		}
		return transactionmodel.NewMutationPlan(mutationContext, transactionmodel.RecordMutationCommit{
			Operation: operation, Object: object, Record: record, Predicates: predicates, Audit: &audit,
		}, nil)
	}
	actionStore := actionpersistence.NewActionBusinessExecutionStore(store)
	system := NewSystemOperationCatalog()
	service := NewActionApplication(ActionApplicationDependencies{
		Catalog:          NewActionCatalog([]definitionmodel.ActionSchema{action}, system, registry),
		SystemOperations: NewSystemOperationExecutor(system),
		BusinessHandlers: newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{
			GetRecordForUpdate: func(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordmodel.Record, error) {
				if objectKey != groupClass.Key || recordID != "class-1" {
					return recordmodel.Record{}, fmt.Errorf("unexpected class lock %s/%s", objectKey, recordID)
				}
				page, err := records.ListRecords(ctx, principal.WorkspaceID, groupClass, recordmodel.RecordListQuery{
					Page: 1, PageSize: 1, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, Filters: map[string]any{"id__in": []any{recordID}},
					LockIntent: recordmodel.RecordQueryLockForUpdate,
				})
				if err != nil {
					return recordmodel.Record{}, err
				}
				if len(page.Items) != 1 {
					return recordmodel.Record{}, fmt.Errorf("locked class count=%d", len(page.Items))
				}
				return page.Items[0], nil
			},
			PlanConditionalUpdate: func(ctx context.Context, objectKey, recordID string, input transactionmodel.ConditionalUpdateInput, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
				if objectKey != groupClass.Key || recordID != "class-1" ||
					len(input.Predicates) != 1 || len(input.Arithmetic) != 1 {
					return transactionmodel.MutationPlan{}, recordmodel.Record{}, fmt.Errorf("unexpected class mutation")
				}
				record, found, err := records.GetRecord(ctx, principal.WorkspaceID, groupClass, recordID)
				if err != nil || !found {
					return transactionmodel.MutationPlan{}, recordmodel.Record{}, fmt.Errorf("read locked class: found=%v: %w", found, err)
				}
				predicate := input.Predicates[0]
				current, ok := record.Data[predicate.Field].(int64)
				if !ok {
					return transactionmodel.MutationPlan{}, recordmodel.Record{}, fmt.Errorf("capacity field %s=%T", predicate.Field, record.Data[predicate.Field])
				}
				if current < 1 {
					return transactionmodel.MutationPlan{}, recordmodel.Record{}, apperror.New(
						apperror.KindConflict, predicate.ErrorCode, nil, map[string]string{"field": predicate.Field},
					)
				}
				record.Data[predicate.Field] = current - 1
				record.UpdatedAt = time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC).
					Add(time.Duration(revision.Add(1)) * time.Nanosecond).
					Format(time.RFC3339Nano)
				audit := auditmodel.AuditEvent{
					ID: "class-audit-" + principal.UserID, WorkspaceID: principal.WorkspaceID,
					Event: "record_updated", ObjectKey: groupClass.Key, RecordID: recordID,
					ActorID: principal.UserID, CreatedAt: stamp,
				}
				plan, err := newPlan(principal, "update", groupClass, record, input.Predicates, audit)
				return plan, record, err
			},
			PlanCreateMutation: func(_ context.Context, objectKey string, fields map[string]any, _ string, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
				if objectKey != classBooking.Key {
					return transactionmodel.MutationPlan{}, recordmodel.Record{}, fmt.Errorf("unexpected booking object %s", objectKey)
				}
				record := recordmodel.Record{
					ID: "booking-" + principal.UserID, CreatedAt: stamp, UpdatedAt: stamp, Data: fields,
				}
				audit := auditmodel.AuditEvent{
					ID: "booking-audit-" + principal.UserID, WorkspaceID: principal.WorkspaceID,
					Event: "record_created", ObjectKey: classBooking.Key, RecordID: record.ID,
					ActorID: principal.UserID, CreatedAt: stamp,
				}
				plan, err := newPlan(principal, "create", classBooking, record, nil, audit)
				return plan, record, err
			},
			ValidateDurableIntent: func(_ context.Context, intent runtimeext.DurableIntent, _ principalmodel.Principal) error {
				if intent.ConsumerKey != connectorGrant.ConnectorKey || intent.ConnectionKey != connectorGrant.ConnectionKey ||
					intent.OperationKey != connectorGrant.OperationKey || intent.ContractSHA256 != connectorGrant.ContractSHA256 {
					return fmt.Errorf("unexpected intent identity")
				}
				return nil
			},
		}),
		UnitOfWork: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(actionStore)),
		Audit: ActionAudit{BuildSuccess: func(_ context.Context, _ definitionmodel.ActionSchema, _ actionmodel.ActionInvocation, result actionmodel.ActionInvocationResult) auditmodel.AuditEvent {
			return auditmodel.AuditEvent{
				ID: "action-audit-" + result.InvocationID, WorkspaceID: "workspace-a",
				Event: "gym.class_booked", ObjectKey: groupClass.Key, ActorID: result.InvocationID, CreatedAt: stamp,
			}
		}},
	})

	type outcome struct {
		index     int
		key       string
		status    string
		bookingID string
		err       error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 100)
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			principal := actionTestPrincipal("group_class.book_class")
			principal.UserID = fmt.Sprintf("member-%03d", index)
			principal.RequestID = fmt.Sprintf("request-%03d", index)
			result, err := service.Invoke(context.Background(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
				ActionKey: action.Key, ObjectKey: action.ObjectKey,
				IdempotencyKey: fmt.Sprintf("book-class-%03d", index), Principal: principal,
			})
			status, bookingID := "", ""
			if result.Object != nil {
				status, _ = result.Object.Output["status"].(string)
				bookingID, _ = result.Object.Output["booking_id"].(string)
			}
			outcomes <- outcome{
				index: index, key: fmt.Sprintf("book-class-%03d", index),
				status: status, bookingID: bookingID, err: err,
			}
		}()
	}
	close(start)
	wait.Wait()
	close(outcomes)

	booked, waitlisted, rejected := 0, 0, 0
	var successful, terminalFailure outcome
	for current := range outcomes {
		switch {
		case current.err == nil && current.status == "booked":
			booked++
			if successful.key == "" {
				successful = current
			}
		case current.err == nil && current.status == "waitlisted":
			waitlisted++
			if successful.key == "" {
				successful = current
			}
		case apperror.CodeOf(current.err) == "gym.class_waitlist_full":
			rejected++
			if terminalFailure.key == "" {
				terminalFailure = current
			}
		default:
			t.Fatalf("unexpected concurrent result status=%q err=%v", current.status, current.err)
		}
	}
	if booked != 10 || waitlisted != 20 || rejected != 70 {
		t.Fatalf("outcomes booked=%d waitlisted=%d rejected=%d", booked, waitlisted, rejected)
	}
	storedClass, found, err := records.GetRecord(t.Context(), "workspace-a", groupClass, "class-1")
	if err != nil || !found || storedClass.Data["remaining_capacity"] != int64(0) ||
		storedClass.Data["remaining_waitlist_capacity"] != int64(0) {
		t.Fatalf("stored class=%#v found=%v err=%v", storedClass, found, err)
	}
	page, err := records.ListRecords(t.Context(), "workspace-a", classBooking, recordmodel.RecordListQuery{Page: 1, PageSize: 100, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted})
	if err != nil || page.Total != 30 || len(page.Items) != 30 {
		t.Fatalf("bookings total=%d items=%d err=%v", page.Total, len(page.Items), err)
	}
	storedBooked, storedWaitlisted := 0, 0
	for _, record := range page.Items {
		switch record.Data["status"] {
		case "booked":
			storedBooked++
		case "waitlisted":
			storedWaitlisted++
		default:
			t.Fatalf("booking %s status=%v", record.ID, record.Data["status"])
		}
	}
	if storedBooked != booked || storedWaitlisted != waitlisted {
		t.Fatalf("stored bookings booked=%d waitlisted=%d outcomes=(%d,%d)", storedBooked, storedWaitlisted, booked, waitlisted)
	}
	for table, want := range map[string]int{
		"_publication_outbox": 30,
		"_audit_events":       160,
	} {
		var count int
		if err := store.DB().QueryRow("SELECT COUNT(*) FROM " + store.Identifier(table)).Scan(&count); err != nil || count != want {
			t.Fatalf("table=%s count=%d want=%d err=%v", table, count, want, err)
		}
	}
	var totalReceipts, succeededReceipts, terminalReceipts, processingReceipts int
	if err := store.DB().QueryRow(
		`SELECT COUNT(*),
			SUM(CASE WHEN status = ? THEN 1 ELSE 0 END),
			SUM(CASE WHEN status = ? THEN 1 ELSE 0 END),
			SUM(CASE WHEN status = ? THEN 1 ELSE 0 END)
		FROM _action_executions`,
		string(idempotency.StatusSucceeded),
		string(idempotency.StatusFailedTerminal),
		string(idempotency.StatusProcessing),
	).Scan(&totalReceipts, &succeededReceipts, &terminalReceipts, &processingReceipts); err != nil {
		t.Fatal(err)
	}
	if totalReceipts != 100 || succeededReceipts != 30 || terminalReceipts != 70 || processingReceipts != 0 {
		t.Fatalf(
			"receipts total=%d succeeded=%d terminal=%d processing=%d",
			totalReceipts, succeededReceipts, terminalReceipts, processingReceipts,
		)
	}

	retryPrincipal := actionTestPrincipal("group_class.book_class")
	retryPrincipal.UserID = fmt.Sprintf("member-%03d", successful.index)
	retryPrincipal.RequestID = "retry-" + successful.key
	replayed, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: action.Key, ObjectKey: action.ObjectKey,
		IdempotencyKey: successful.key, Principal: retryPrincipal,
	})
	if err != nil || replayed.Object == nil ||
		replayed.Object.Message != "backend.action.idempotent_replay" ||
		replayed.Object.Output["booking_id"] != successful.bookingID ||
		replayed.Object.Output["status"] != successful.status {
		t.Fatalf("replayed=%#v successful=%#v error=%v", replayed, successful, err)
	}
	failurePrincipal := actionTestPrincipal("group_class.book_class")
	failurePrincipal.UserID = fmt.Sprintf("member-%03d", terminalFailure.index)
	failurePrincipal.RequestID = "retry-" + terminalFailure.key
	failedReplay, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: action.Key, ObjectKey: action.ObjectKey,
		IdempotencyKey: terminalFailure.key, Principal: failurePrincipal,
	})
	if apperror.CodeOf(err) != "gym.class_waitlist_full" ||
		apperror.ParamsOf(err)["field"] != "remaining_waitlist_capacity" ||
		failedReplay.Status != "failed" || failedReplay.Retryable {
		t.Fatalf("failed replay=%#v original=%#v error=%v", failedReplay, terminalFailure, err)
	}
	var failureStatus, failureCode string
	var failureResponseStatus int
	if err := store.DB().QueryRow(
		`SELECT status, error_code, response_status
		FROM _action_executions
		WHERE idempotency_key = ?`,
		terminalFailure.key,
	).Scan(&failureStatus, &failureCode, &failureResponseStatus); err != nil {
		t.Fatal(err)
	}
	if failureStatus != string(idempotency.StatusFailedTerminal) ||
		failureCode != "gym.class_waitlist_full" || failureResponseStatus != 409 {
		t.Fatalf(
			"failure receipt status=%q code=%q response_status=%d",
			failureStatus, failureCode, failureResponseStatus,
		)
	}
	for table, want := range map[string]int{
		"concurrent_class_booking": 30,
		"_publication_outbox":      30,
		"_audit_events":            160,
		"_action_executions":       100,
	} {
		var count int
		if err := store.DB().QueryRow("SELECT COUNT(*) FROM " + store.Identifier(table)).Scan(&count); err != nil || count != want {
			t.Fatalf("after replay table=%s count=%d want=%d err=%v", table, count, want, err)
		}
	}
}
