package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-connector-sdk"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	projectconnector "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit/projectconnector"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type projectConnectorAcceptanceDeliveryRepository struct {
	independentDeliveryRepository
	sequence int
}

func (r *projectConnectorAcceptanceDeliveryRepository) InsertOutbox(_ context.Context, _ string, value integrationmodel.IntegrationOutboxMessage) (integrationmodel.IntegrationOutboxMessage, error) {
	r.sequence++
	value.ID = fmt.Sprintf("project-outbox-%d", r.sequence)
	r.outboxes = append(r.outboxes, value)
	return value, nil
}

func TestProjectOwnedConnectorCompletesRuntimeCapabilityLifecycle(t *testing.T) {
	projectAdapter := projectconnector.NewProjectDeliverySandboxAdapter(nil)
	providers := connector.NewRegistry()
	if err := providers.RegisterProviderSet(connector.ProviderSet{Providers: []connector.Adapter{projectAdapter}}); err != nil {
		t.Fatal(err)
	}
	providers.Freeze()
	registry := NewConnectorRegistryWithProviders(projectConnectorAcceptanceSchema(), providers)

	connection := integrationmodel.IntegrationConnection{
		Key: "project-primary", WorkspaceID: "workspace", ConnectorKey: projectconnector.ProjectDeliverySandboxConnectorKey,
		ProviderKey: projectconnector.ProjectDeliverySandboxProviderKey, Status: "active",
		Config: map[string]any{"endpoint": "https://project.example"},
		SecretRefs: map[string]string{
			"access_token":   "secret:project-access",
			"webhook_secret": "secret:project-webhook",
		},
	}
	config := &independentConfigRepository{
		connections: map[string]integrationmodel.IntegrationConnection{connection.Key: connection},
		secrets: map[string]integrationmodel.IntegrationSecret{
			"project-access":  {Key: "project-access", WorkspaceID: "workspace", Kind: "bearer_token", Status: "active", ValueRef: "material:project-access"},
			"project-webhook": {Key: "project-webhook", WorkspaceID: "workspace", Kind: "signing_secret", Status: "active", ValueRef: "material:project-webhook"},
		},
		materials: map[string]string{
			"workspace:project-access":  "old-access-token",
			"workspace:project-webhook": "webhook-signature",
		},
	}
	events := &integrationInboundEventRepository{}
	delivery := &projectConnectorAcceptanceDeliveryRepository{}
	worker := &integrationOutboxWorkerEdgeRepository{claimOK: true}
	var service *IntegrationApplicationService
	service = NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: config, EventRepository: events, DeliveryRepository: delivery, WorkerRepository: worker,
		Registry: registry,
		AdapterOutboxSender: func(ctx context.Context, message integrationmodel.IntegrationOutboxMessage, principal principalmodel.Principal) (OutboxSendResult, error) {
			return service.SendAdapterOutboxMessage(ctx, message, principal)
		},
		Worker: workerplatform.Dependencies{Clock: integrationFixedClock{now: time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)}},
	})
	service.RegisterIntegrationOutboxSender(projectconnector.ProjectDeliverySandboxConnectorKey, service)
	principal := integrationManagementPrincipal(PermissionInvoke, PermissionRetry)
	notifyIntent := runtimeext.DurableIntent{
		ConsumerKey: projectconnector.ProjectDeliverySandboxConnectorKey, ConnectionKey: connection.Key, OperationKey: projectconnector.ProjectDeliverySandboxNotifyOperationKey,
		ContractSHA256: projectconnector.ProjectDeliverySandboxNotifyContractSHA256, Payload: map[string]any{"recipient": "member@example.com", "message": "ready"},
	}
	if err := service.ValidateActionDurableIntent(t.Context(), notifyIntent, principal); err != nil {
		t.Fatalf("valid Action durable intent rejected: %v", err)
	}
	staleIntent := notifyIntent
	staleIntent.ContractSHA256 = strings.Repeat("f", 64)
	if err := service.ValidateActionDurableIntent(t.Context(), staleIntent, principal); testErrorCode(err) != "backend.action.durable_intent.operation_contract_mismatch" {
		t.Fatalf("stale Action durable intent error=%v", err)
	}
	readIntent := notifyIntent
	readIntent.OperationKey, readIntent.ContractSHA256 = projectconnector.ProjectDeliverySandboxLookupOperationKey, projectconnector.ProjectDeliverySandboxLookupContractSHA256
	readIntent.Payload = map[string]any{"record_id": "record-1"}
	if err := service.ValidateActionDurableIntent(t.Context(), readIntent, principal); testErrorCode(err) != runtimeext.ConnectorActionSideEffectOutboxErrorCode {
		t.Fatalf("read operation staged as side effect error=%v", err)
	}
	staleMessage := integrationmodel.IntegrationOutboxMessage{
		ID: "durable_intent:execution-1:0", ConnectorKey: projectconnector.ProjectDeliverySandboxConnectorKey, ConnectionKey: connection.Key,
		Operation: projectconnector.ProjectDeliverySandboxNotifyOperationKey, RequestFingerprint: strings.Repeat("f", 64), AttemptCount: 1,
	}
	if _, err := service.prepareRegisteredOutboxCall(t.Context(), staleMessage, connection, principal, notifyIntent.Payload); testErrorCode(err) != "backend.action.durable_intent.operation_contract_mismatch" {
		t.Fatalf("stale persisted Action intent error=%v", err)
	}

	syncResult, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{
		ConnectorKey: projectconnector.ProjectDeliverySandboxConnectorKey, ConnectionKey: connection.Key,
		Operation: projectconnector.ProjectDeliverySandboxLookupOperationKey, OperationMode: string(connector.ModeCall),
		ContractSHA256: projectconnector.ProjectDeliverySandboxLookupContractSHA256, Request: map[string]any{"record_id": "record-1"},
		RequestRef: "project-sync-1",
	}, principal)
	if err != nil || syncResult.Response["record_id"] != "record-1" || syncResult.Response["name"] != "Project record" || syncResult.ActionInvocation.Status != "succeeded" {
		t.Fatalf("sync result=%#v error=%v", syncResult, err)
	}
	if config.materials["workspace:project-access"] != "rotated-access-token" {
		t.Fatalf("Runtime did not persist project Secret rotation: %#v", config.materials)
	}
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{
		ConnectorKey: projectconnector.ProjectDeliverySandboxConnectorKey, ConnectionKey: connection.Key, Operation: projectconnector.ProjectDeliverySandboxNotifyOperationKey,
		OperationMode: string(connector.ModeEnqueue), OperationEffect: string(connector.EffectWrite), ContractSHA256: projectconnector.ProjectDeliverySandboxNotifyContractSHA256,
		Request: notifyIntent.Payload, ActionExecution: true,
	}, principal); testErrorCode(err) != runtimeext.ConnectorActionSideEffectOutboxErrorCode {
		t.Fatalf("Action synchronous external side effect error=%v", err)
	}
	if err := service.validateSyncOperationIdentity(connection, SyncCallRequest{Operation: projectconnector.ProjectDeliverySandboxLookupOperationKey, OperationMode: string(connector.ModeCall), ContractSHA256: projectconnector.ProjectDeliverySandboxLookupContractSHA256}); err != nil {
		t.Fatalf("published operation identity rejected: %v", err)
	}
	if err := service.validateSyncOperationIdentity(connection, SyncCallRequest{Operation: projectconnector.ProjectDeliverySandboxLookupOperationKey, OperationMode: string(connector.ModeEnqueue), ContractSHA256: projectconnector.ProjectDeliverySandboxLookupContractSHA256}); testErrorCode(err) != "backend.integration.sync_call.operation_mode_mismatch" {
		t.Fatalf("stale operation mode error=%v", err)
	}
	if err := service.validateSyncOperationIdentity(connection, SyncCallRequest{Operation: projectconnector.ProjectDeliverySandboxLookupOperationKey, OperationMode: string(connector.ModeCall), ContractSHA256: strings.Repeat("f", 64)}); testErrorCode(err) != "backend.integration.sync_call.operation_contract_mismatch" {
		t.Fatalf("stale operation contract error=%v", err)
	}

	notify, err := service.EnqueueIntegrationOutboxMessage(t.Context(), integrationmodel.IntegrationOutboxEnqueueRequest{
		ConnectorKey: projectconnector.ProjectDeliverySandboxConnectorKey, ConnectionKey: connection.Key,
		Operation:  projectconnector.ProjectDeliverySandboxNotifyOperationKey,
		Payload:    map[string]any{"recipient": "member@example.com", "message": "ready"},
		RequestRef: "project-notify-request-1",
	}, principal)
	if err != nil || notify.ID == "" || notify.Status != "queued" || len(delivery.outboxes) != 1 {
		t.Fatalf("notification enqueue=%#v persisted=%#v error=%v", notify, delivery.outboxes, err)
	}
	notify.AttemptCount, notify.LeaseOwner, notify.FencingToken = 1, "worker", 1
	worker.claim = notify
	retried, bucket := service.processDueOutboxMessage(t.Context(), notify, principal)
	if bucket != "retried" || retried.Status != "failed" || retried.NextAttemptAt == "" {
		t.Fatalf("retry result=%#v bucket=%q", retried, bucket)
	}
	worker.claim = retried
	sent, bucket := service.processDueOutboxMessage(t.Context(), retried, principal)
	if bucket != "sent" || sent.Status != "sent" || sent.ResponseRef != "project-notification:member@example.com" {
		t.Fatalf("notification result=%#v bucket=%q", sent, bucket)
	}

	export, err := service.EnqueueIntegrationOutboxMessage(t.Context(), integrationmodel.IntegrationOutboxEnqueueRequest{
		ConnectorKey: projectconnector.ProjectDeliverySandboxConnectorKey, ConnectionKey: connection.Key,
		Operation: projectconnector.ProjectDeliverySandboxExportOperationKey,
		Payload:   map[string]any{"scope": "workspace"}, RequestRef: "local-operation-1",
	}, principal)
	if err != nil || export.ID == "" || export.Status != "queued" || len(delivery.outboxes) != 2 {
		t.Fatalf("long operation enqueue=%#v persisted=%#v error=%v", export, delivery.outboxes, err)
	}
	export.AttemptCount, export.LeaseOwner, export.FencingToken = 1, "worker", 2
	worker.claim = export
	started, bucket := service.processDueOutboxMessage(t.Context(), export, principal)
	if bucket != "sent" || started.Status != "sent" || started.ResponseRef != "external-export:workspace" {
		t.Fatalf("long operation result=%#v bucket=%q", started, bucket)
	}

	internalAdapter, ok := registry.ProviderAdapter(projectconnector.ProjectDeliverySandboxConnectorKey, projectconnector.ProjectDeliverySandboxProviderKey)
	if !ok {
		t.Fatal("project provider disappeared from frozen Runtime registry")
	}
	reconciler, ok := internalAdapter.(integrationcontract.Reconciler)
	if !ok {
		t.Fatal("project long-running operation lost Reconciler")
	}
	resolvedSecrets, err := service.ResolveAdapterSecrets(t.Context(), connection)
	if err != nil {
		t.Fatal(err)
	}
	reconciled, err := reconciler.Reconcile(t.Context(), integrationcontract.ReconcileRequest{
		ConnectorKey: projectconnector.ProjectDeliverySandboxConnectorKey, Connection: connection, Operation: projectconnector.ProjectDeliverySandboxExportOperationKey,
		ContractSHA256: projectconnector.ProjectDeliverySandboxExportContractSHA256, Request: map[string]any{"scope": "workspace"},
		RequestRef: export.RequestRef, ResponseRef: started.ResponseRef, Secrets: resolvedSecrets, Principal: principal,
	})
	if err != nil || reconciled.Outcome != integrationcontract.ReconciliationSucceeded || reconciled.ResponseRef != "external-export:completed" {
		t.Fatalf("reconciliation=%#v error=%v", reconciled, err)
	}

	webhook, err := service.ReceiveIntegrationWebhookValuesForWorkspace(
		t.Context(), "workspace", connection.Key,
		map[string][]string{"X-Project-Signature": {"webhook-signature"}}, nil,
		[]byte(`{"event_id":"event-1","record_id":"record-1"}`),
	)
	if err != nil || webhook.Event == nil || webhook.Event.ExternalID != "event-1" || webhook.Event.EventType != "export.completed" || events.acceptCount != 1 {
		t.Fatalf("webhook=%#v accepted=%#v error=%v", webhook, events.accepted, err)
	}

	snapshot := projectAdapter.ExecutionSnapshot()
	if snapshot.SynchronousCalls != 1 || snapshot.NotificationAttempts != 2 || snapshot.NotificationsDelivered != 1 ||
		snapshot.LongOperationsStarted != 1 || snapshot.Reconciliations != 1 || snapshot.WebhooksVerified != 1 ||
		!snapshot.UsedRotatedAccessToken || snapshot.LastNotifyRequestRef != notify.RequestRef || snapshot.LastExportRequestRef != export.RequestRef {
		t.Fatalf("project Adapter evidence=%#v", snapshot)
	}
}

func projectConnectorAcceptanceSchema() integrationmodel.IntegrationSchema {
	return integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key: projectconnector.ProjectDeliverySandboxConnectorKey, Type: "project", Provider: projectconnector.ProjectDeliverySandboxProviderKey,
		Providers: []integrationmodel.ConnectorProviderSchema{{Key: projectconnector.ProjectDeliverySandboxProviderKey}},
		Operations: []integrationmodel.ConnectorOperationSchema{
			{
				Key: projectconnector.ProjectDeliverySandboxLookupOperationKey, Method: "GET", ExecutionMode: "sync", SideEffect: "read",
				Input:  []definitionmodel.FieldSchema{{Key: "record_id", Type: "text", Required: true}},
				Output: []definitionmodel.FieldSchema{{Key: "record_id", Type: "text", Required: true}, {Key: "name", Type: "text", Required: true}},
			},
			{
				Key: projectconnector.ProjectDeliverySandboxNotifyOperationKey, Method: "POST", ExecutionMode: "async", SideEffect: "write", IdempotencySupported: true,
				Input: []definitionmodel.FieldSchema{{Key: "recipient", Type: "text", Required: true}, {Key: "message", Type: "text", Required: true}},
			},
			{
				Key: projectconnector.ProjectDeliverySandboxExportOperationKey, Method: "POST", ExecutionMode: "async", SideEffect: "write", IdempotencySupported: true,
				Input: []definitionmodel.FieldSchema{{Key: "scope", Type: "text", Required: true}},
			},
		},
	}}}
}
