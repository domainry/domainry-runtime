package integrationtest

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/domainry/domainry-connector-sdk"
	. "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationruntime "github.com/domainry/domainry-runtime/runtime/domain/integration/runtime"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	actionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/action"
	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

const (
	p8GateConnectorKey      = "access_control"
	p8GateProviderKey       = "project_gate"
	p8GateConnectionKey     = "gym_primary_gate"
	p8GateOperationKey      = "grant_class_access"
	p8GateContractSHA256    = "7777777777777777777777777777777777777777777777777777777777777777"
	p8GateActionKey         = "group_class.book_class"
	p8GateBookingObjectKey  = "p8_gate_booking"
	p8GateActionRecordTable = `CREATE TABLE p8_gate_booking (
		workspace_id TEXT NOT NULL,
		id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		member_id TEXT NOT NULL,
		class_id TEXT NOT NULL,
		UNIQUE (workspace_id, id)
	)`
)

type p8GateRequest struct {
	MemberID  string `json:"member_id"`
	ClassID   string `json:"class_id"`
	BookingID string `json:"booking_id"`
}

type p8GateDelivery struct {
	mu         sync.Mutex
	calls      int
	request    p8GateRequest
	requestRef string
}

func (p *p8GateDelivery) record(request connector.TypedRequest[p8GateRequest]) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.request = request.Input
	p.requestRef = request.RequestRef
}

func (p *p8GateDelivery) snapshot() (int, p8GateRequest, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.request, p.requestRef
}

func TestProjectGateConnectorExecutesOnlyCommittedActionOutbox(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "committed-gate-outbox.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(p8GateActionRecordTable); err != nil {
		t.Fatal(err)
	}

	delivery := &p8GateDelivery{}
	providers := p8GateProviders(t, delivery)
	manifest := p8GateManifest()
	configStore := integrationpersistence.NewIntegrationConfigStore(store)
	if _, err := configStore.UpsertConnection(t.Context(), "default", integrationmodel.IntegrationConnection{
		Key: p8GateConnectionKey, WorkspaceID: "default",
		ConnectorKey: p8GateConnectorKey, ProviderKey: p8GateProviderKey,
		Name: "Gym primary gate", Status: "active", Config: map[string]any{}, SecretRefs: map[string]string{},
	}); err != nil {
		t.Fatal(err)
	}
	dependencies := objectActionTestDependencies(t.Context(), store)
	dependencies.ConnectorProviders = providers
	services := NewRuntimeServices(t.Context(), RuntimeServicesConfig{Manifest: manifest, Dependencies: dependencies})

	actionStore := actionpersistence.NewActionBusinessExecutionStore(store)
	bookingObject := definitionmodel.ObjectSchema{Key: p8GateBookingObjectKey, Fields: []definitionmodel.FieldSchema{
		{Key: "member_id", Type: "text"},
		{Key: "class_id", Type: "text"},
	}}
	failedOutboxID := commitP8GateAction(t, store, actionStore, bookingObject, "rolled-back", true)
	workerPrincipal := integrationruntime.IntegrationWorkerPrincipal("default")
	empty, err := services.Applications().Integrations.ProcessDueIntegrationOutbox(t.Context(), 10, workerPrincipal)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Sent != 0 || empty.Retried != 0 || empty.DeadLettered != 0 || empty.Skipped != 0 || len(empty.Messages) != 0 {
		t.Fatalf("worker observed rolled-back Outbox: %#v", empty)
	}
	if calls, _, _ := delivery.snapshot(); calls != 0 {
		t.Fatalf("gate Adapter ran for rolled-back Outbox: calls=%d", calls)
	}
	if _, found, err := integrationpersistence.NewIntegrationDeliveryStore(store).GetOutbox(t.Context(), "default", failedOutboxID); err != nil || found {
		t.Fatalf("rolled-back Outbox persisted: found=%v err=%v", found, err)
	}
	assertP8GateBookingCount(t, store, "booking-rolled-back", 0)

	committedOutboxID := commitP8GateAction(t, store, actionStore, bookingObject, "committed", false)
	assertP8GateBookingCount(t, store, "booking-committed", 1)
	queued, found, err := integrationpersistence.NewIntegrationDeliveryStore(store).GetOutbox(
		t.Context(), "default", committedOutboxID,
	)
	if err != nil || !found || queued.Status != "queued" {
		t.Fatalf("committed Outbox was not durably queued: %#v found=%v err=%v", queued, found, err)
	}
	if calls, _, _ := delivery.snapshot(); calls != 0 {
		t.Fatalf("gate Adapter ran inside Action commit: calls=%d", calls)
	}
	processed, err := services.Applications().Integrations.ProcessDueIntegrationOutbox(t.Context(), 10, workerPrincipal)
	if err != nil {
		t.Fatal(err)
	}
	if processed.Sent != 1 || processed.Retried != 0 || processed.DeadLettered != 0 || processed.Skipped != 0 ||
		len(processed.Messages) != 1 || processed.Messages[0].ID != committedOutboxID {
		t.Fatalf("committed gate Outbox result=%#v", processed)
	}
	calls, request, requestRef := delivery.snapshot()
	if calls != 1 || request.MemberID != "member-1" || request.ClassID != "class-1" ||
		request.BookingID != "booking-committed" || requestRef == "" {
		t.Fatalf("gate Adapter calls=%d request=%#v request_ref=%q", calls, request, requestRef)
	}
	persisted, found, err := integrationpersistence.NewIntegrationDeliveryStore(store).GetOutbox(
		t.Context(), "default", committedOutboxID,
	)
	if err != nil || !found || persisted.Status != "sent" ||
		persisted.ResponseRef != "gate-access:booking-committed" || persisted.FencingToken != 1 {
		t.Fatalf("committed Outbox=%#v found=%v err=%v", persisted, found, err)
	}
}

func p8GateManifest() manifestmodel.ManifestSchema {
	return manifestmodel.ManifestSchema{Integrations: integrationmodel.IntegrationSchema{
		Connectors: []integrationmodel.ConnectorSchema{{
			Key: p8GateConnectorKey, Type: "project", Provider: p8GateProviderKey,
			Providers: []integrationmodel.ConnectorProviderSchema{{
				Key: p8GateProviderKey, ProviderRevision: "project-gate-v1",
				OperationKeys: []string{p8GateOperationKey},
			}},
			Operations: []integrationmodel.ConnectorOperationSchema{{
				Key: p8GateOperationKey, Method: "POST", ExecutionMode: "async", SideEffect: "write",
				IdempotencySupported: true,
				Input: []definitionmodel.FieldSchema{
					{Key: "member_id", Type: "text", Required: true},
					{Key: "class_id", Type: "text", Required: true},
					{Key: "booking_id", Type: "text", Required: true},
				},
			}},
		}},
	}}
}

func p8GateProviders(t *testing.T, delivery *p8GateDelivery) *connector.Registry {
	t.Helper()
	operation := connector.EnqueueOperation[p8GateRequest]{
		ConnectorKey: p8GateConnectorKey, ProviderKey: p8GateProviderKey,
		Key: p8GateOperationKey, ContractSHA256: p8GateContractSHA256,
		Reliability: connector.ReliabilityContract{
			Effect: connector.EffectWrite,
			Idempotency: connector.IdempotencyContract{
				Strategy: connector.IdempotencyProviderKey, KeyRetentionSeconds: 86400,
			},
			Reconciliation: connector.ReconciliationNone,
			Compensation:   connector.CompensationContract{Mode: connector.CompensationNone},
		},
	}
	bound, err := connector.BindEnqueueDelivery(
		operation,
		func(_ context.Context, request connector.TypedRequest[p8GateRequest]) (connector.DeliveryResult, error) {
			delivery.record(request)
			return connector.DeliveryResult{ResponseRef: "gate-access:" + request.Input.BookingID}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := connector.NewProvider(connector.ProviderSchema{
		ConnectorKey: p8GateConnectorKey, ProviderKey: p8GateProviderKey, ProviderRevision: "project-gate-v1",
	}, bound)
	if err != nil {
		t.Fatal(err)
	}
	registry := connector.NewRegistry()
	if err := registry.RegisterProviderSet(connector.ProviderSet{Providers: []connector.Adapter{provider}}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	return registry
}

func commitP8GateAction(
	t *testing.T,
	store *persistence.RuntimeStore,
	repository actionpersistence.ActionBusinessExecutionStore,
	object definitionmodel.ObjectSchema,
	suffix string,
	failReceipt bool,
) string {
	t.Helper()
	now := time.Date(2026, 7, 23, 18, 0, 0, 0, time.UTC)
	claim, err := repository.TryBeginExecution(t.Context(), actionmodel.ActionExecutionClaimRequest{
		Execution: actionmodel.ActionBusinessExecution{
			WorkspaceID: "default", ObjectKey: "group_class", ActionKey: p8GateActionKey,
			IdempotencyKey: "p8-gate-" + suffix, ActorID: "member-1", RoleKey: "member",
		},
		RequestFingerprint: "p8-gate-fingerprint-" + suffix,
		LeaseOwner:         "runtime-a", LeaseTTL: time.Minute, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	triggerName := "p8_fail_gate_receipt"
	if failReceipt {
		statement := fmt.Sprintf(
			`CREATE TRIGGER %s BEFORE UPDATE OF status ON business_action_executions
			WHEN NEW.id = %s AND NEW.status = 'succeeded'
			BEGIN SELECT RAISE(ABORT, 'injected gate receipt failure'); END`,
			p8QuoteSQLiteIdentifier(triggerName),
			p8QuoteSQLiteString(claim.Execution.ID),
		)
		if _, err := store.DB().Exec(statement); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = store.DB().Exec("DROP TRIGGER IF EXISTS " + p8QuoteSQLiteIdentifier(triggerName)) })
	}

	transaction, err := repository.BeginExecutionTransaction(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	recordID := "booking-" + suffix
	outboxID := "durable_intent:" + claim.Execution.ID + ":0"
	_, commitErr := transaction.Commit(t.Context(), []transactionmodel.RecordMutationCommit{{
		Operation: "create", Object: object,
		Record: recordmodel.Record{
			ID: recordID, CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano),
			Data: map[string]any{"member_id": "member-1", "class_id": "class-1"},
		},
		Outbox: []integrationmodel.IntegrationOutboxMessage{{
			ID: outboxID, WorkspaceID: "default",
			ConnectorKey: p8GateConnectorKey, ConnectionKey: p8GateConnectionKey,
			Operation: p8GateOperationKey, RequestRef: claim.Execution.ID,
			DedupKey: claim.Execution.ID + ":gate:0", RequestFingerprint: p8GateContractSHA256,
			Payload: map[string]any{
				"member_id": "member-1", "class_id": "class-1", "booking_id": recordID,
			},
			CreatedBy: "member-1",
		}},
	}}, actionmodel.ActionExecutionCompletion{
		Execution: claim.Execution, ExecutionID: claim.Execution.ID,
		LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken,
		Result: map[string]any{"booking_id": recordID}, ResponseStatus: 200,
		ExpiresAt: now.Add(24 * time.Hour), Now: now.Add(time.Second),
	})
	if failReceipt {
		if commitErr == nil {
			t.Fatal("receipt fault unexpectedly committed Action Outbox")
		}
		if !strings.Contains(commitErr.Error(), "injected gate receipt failure") {
			t.Fatalf("Action failed outside injected receipt window: %v", commitErr)
		}
		if _, err := store.DB().Exec("DROP TRIGGER " + p8QuoteSQLiteIdentifier(triggerName)); err != nil {
			t.Fatal(err)
		}
		return outboxID
	}
	if commitErr != nil {
		t.Fatalf("commit gate Action: %v", commitErr)
	}
	return outboxID
}

func p8QuoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func p8QuoteSQLiteString(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}

func assertP8GateBookingCount(t *testing.T, store *persistence.RuntimeStore, bookingID string, expected int) {
	t.Helper()
	var count int
	if err := store.DB().QueryRow(
		"SELECT COUNT(*) FROM "+store.Identifier(p8GateBookingObjectKey)+
			" WHERE "+store.Identifier("workspace_id")+" = "+store.Placeholder(1)+
			" AND "+store.Identifier("id")+" = "+store.Placeholder(2),
		"default",
		bookingID,
	).Scan(&count); err != nil || count != expected {
		t.Fatalf("booking %s count=%d want=%d err=%v", bookingID, count, expected, err)
	}
}
