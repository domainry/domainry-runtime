// Integration application service independent-path tests.
package integration

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"

	"context"
	"errors"
	"strings"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

type independentConfigRepository struct {
	connections   map[string]integrationmodel.IntegrationConnection
	secrets       map[string]integrationmodel.IntegrationSecret
	materials     map[string]string
	identities    map[string]integrationmodel.IntegrationExternalIdentity
	subscriptions []integrationmodel.IntegrationWebhookSubscription
	apiKeys       map[string]integrationmodel.IntegrationAPIKey
}

func (r *independentConfigRepository) ListSecrets(context.Context, string) ([]integrationmodel.IntegrationSecret, error) {
	return mapValues(r.secrets), nil
}
func (r *independentConfigRepository) UpsertSecret(_ context.Context, _ string, value integrationmodel.IntegrationSecret) (integrationmodel.IntegrationSecret, error) {
	r.secrets[value.Key] = value
	return value, nil
}
func (r *independentConfigRepository) ListConnections(context.Context, string) ([]integrationmodel.IntegrationConnection, error) {
	return mapValues(r.connections), nil
}
func (r *independentConfigRepository) UpsertConnection(_ context.Context, _ string, value integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
	r.connections[value.Key] = value
	return value, nil
}
func (r *independentConfigRepository) ListExternalIdentities(context.Context, string) ([]integrationmodel.IntegrationExternalIdentity, error) {
	return mapValues(r.identities), nil
}
func (r *independentConfigRepository) UpsertExternalIdentity(_ context.Context, _ string, value integrationmodel.IntegrationExternalIdentity) (integrationmodel.IntegrationExternalIdentity, error) {
	if r.identities == nil {
		r.identities = map[string]integrationmodel.IntegrationExternalIdentity{}
	}
	r.identities[value.Key] = value
	return value, nil
}
func (r *independentConfigRepository) ListWebhookSubscriptions(ctx context.Context, _ string, _, _, _ string, _ int) ([]integrationmodel.IntegrationWebhookSubscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]integrationmodel.IntegrationWebhookSubscription(nil), r.subscriptions...), nil
}

func (r *independentConfigRepository) UpsertWebhookSubscription(_ context.Context, _ string, value integrationmodel.IntegrationWebhookSubscription) (integrationmodel.IntegrationWebhookSubscription, error) {
	for index := range r.subscriptions {
		if r.subscriptions[index].Key == value.Key && r.subscriptions[index].WorkspaceID == value.WorkspaceID {
			r.subscriptions[index] = value
			return value, nil
		}
	}
	r.subscriptions = append(r.subscriptions, value)
	return value, nil
}
func (r *independentConfigRepository) ListAPIKeys(_ context.Context, workspaceID string) ([]integrationmodel.IntegrationAPIKey, error) {
	values := []integrationmodel.IntegrationAPIKey{}
	for _, value := range r.apiKeys {
		if value.WorkspaceID == workspaceID {
			values = append(values, value)
		}
	}
	return values, nil
}
func (r *independentConfigRepository) UpsertAPIKey(_ context.Context, _ string, value integrationmodel.IntegrationAPIKey) (integrationmodel.IntegrationAPIKey, error) {
	if r.apiKeys == nil {
		r.apiKeys = map[string]integrationmodel.IntegrationAPIKey{}
	}
	r.apiKeys[value.WorkspaceID+":"+value.Key] = value
	return value, nil
}
func (r *independentConfigRepository) FindAPIKeyByTokenHash(_ context.Context, workspaceID, tokenHash string) (integrationmodel.IntegrationAPIKey, bool, error) {
	for _, value := range r.apiKeys {
		if value.WorkspaceID == workspaceID && value.TokenHash == tokenHash {
			return value, true, nil
		}
	}
	return integrationmodel.IntegrationAPIKey{}, false, nil
}
func (r *independentConfigRepository) UpdateAPIKeyLastUsed(_ context.Context, workspaceID, key, usedAt string) (integrationmodel.IntegrationAPIKey, error) {
	value := r.apiKeys[workspaceID+":"+key]
	value.LastUsedAt = usedAt
	r.apiKeys[workspaceID+":"+key] = value
	return value, nil
}
func (r *independentConfigRepository) PutSecretMaterial(ctx context.Context, workspaceID, secretKey, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.materials[workspaceID+":"+secretKey] = value
	return nil
}
func (r *independentConfigRepository) ResolveSecretMaterial(ctx context.Context, workspaceID, secretKey string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return r.materials[workspaceID+":"+secretKey], nil
}
func (r *independentConfigRepository) DeleteConnection(ctx context.Context, _ string, connectionKey string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, ok := r.connections[connectionKey]; !ok {
		return false, nil
	}
	delete(r.connections, connectionKey)
	return true, nil
}

type independentDeliveryRepository struct {
	invocations         []integrationmodel.IntegrationInvocation
	outboxes            []integrationmodel.IntegrationOutboxMessage
	insertInvocationErr error
	insertOutboxErr     error
}

func (r *independentDeliveryRepository) ListInvocations(context.Context, string, string, string, string, string, int) ([]integrationmodel.IntegrationInvocation, error) {
	return append([]integrationmodel.IntegrationInvocation(nil), r.invocations...), nil
}
func (r *independentDeliveryRepository) InsertInvocation(_ context.Context, _ string, value integrationmodel.IntegrationInvocation) (integrationmodel.IntegrationInvocation, error) {
	if r.insertInvocationErr != nil {
		return integrationmodel.IntegrationInvocation{}, r.insertInvocationErr
	}
	value.ID = "independent-invocation"
	r.invocations = append(r.invocations, value)
	return value, nil
}
func (*independentDeliveryRepository) UpdateInvocationStatus(context.Context, string, string, string, int64, string, string) (integrationmodel.IntegrationInvocation, error) {
	return integrationmodel.IntegrationInvocation{}, nil
}
func (r *independentDeliveryRepository) CompleteInvocation(_ context.Context, _ string, id, status string, duration int64, responseRef, errorText string, metadata map[string]any) (integrationmodel.IntegrationInvocation, error) {
	for index := range r.invocations {
		if r.invocations[index].ID == id {
			r.invocations[index].Status, r.invocations[index].DurationMS = status, duration
			r.invocations[index].ResponseRef, r.invocations[index].Error, r.invocations[index].Metadata = responseRef, errorText, metadata
			return r.invocations[index], nil
		}
	}
	return integrationmodel.IntegrationInvocation{}, nil
}
func (r *independentDeliveryRepository) ListPreparedInvocationsForReconciliation(_ context.Context, _ principalmodel.SystemScope, limit int, staleBefore string) ([]integrationmodel.IntegrationInvocation, error) {
	values := []integrationmodel.IntegrationInvocation{}
	for _, invocation := range r.invocations {
		if invocation.Status == "prepared" && invocation.ResponseRef == "" && invocation.CreatedAt <= staleBefore {
			values = append(values, invocation)
			if len(values) == limit {
				break
			}
		}
	}
	return values, nil
}
func (r *independentDeliveryRepository) MarkInvocationReconciliationRequired(_ context.Context, workspaceID, invocationID, detectedAt string) (integrationmodel.IntegrationInvocation, bool, error) {
	for index := range r.invocations {
		invocation := &r.invocations[index]
		if invocation.WorkspaceID == workspaceID && invocation.ID == invocationID && invocation.Status == "prepared" && invocation.ResponseRef == "" {
			invocation.Status = "reconciliation_required"
			invocation.Error = "backend.integration.invocation.external_receipt_missing"
			invocation.UpdatedAt = detectedAt
			return *invocation, true, nil
		}
	}
	return integrationmodel.IntegrationInvocation{}, false, nil
}
func (*independentDeliveryRepository) ListOutbox(context.Context, string, string, string, int) ([]integrationmodel.IntegrationOutboxMessage, error) {
	return nil, nil
}

func (r *independentDeliveryRepository) InsertOutbox(_ context.Context, _ string, value integrationmodel.IntegrationOutboxMessage) (integrationmodel.IntegrationOutboxMessage, error) {
	if r.insertOutboxErr != nil {
		return integrationmodel.IntegrationOutboxMessage{}, r.insertOutboxErr
	}
	if strings.TrimSpace(value.ID) != "" {
		for _, existing := range r.outboxes {
			if existing.ID == value.ID {
				return integrationmodel.IntegrationOutboxMessage{}, errors.New("unique outbox id")
			}
		}
	}
	r.outboxes = append(r.outboxes, value)
	return value, nil
}

func TestNotificationFallbackUsesDeterministicChildAndEvidenceChain(t *testing.T) {
	repository := &independentDeliveryRepository{}
	service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: repository})
	parent := integrationmodel.IntegrationOutboxMessage{ID: "primary-1", WorkspaceID: "workspace-primary", Payload: map[string]any{
		"notification_fallback_hop":  0,
		"notification_fallback_plan": []any{map[string]any{"connector_key": "email", "connection_key": "email-primary", "operation": "send_email", "payload": map[string]any{"to": []any{"user@example.com"}, "subject": "Fallback", "text": "Fallback body"}}},
	}}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{UserID: "worker"}}
	if err := service.enqueueNotificationFallback(t.Context(), parent, principal); err != nil {
		t.Fatal(err)
	}
	if err := service.enqueueNotificationFallback(t.Context(), parent, principal); err != nil {
		t.Fatalf("deterministic duplicate was not idempotent: %v", err)
	}
	if len(repository.outboxes) != 1 {
		t.Fatalf("fallback children=%d, want 1", len(repository.outboxes))
	}
	child := repository.outboxes[0]
	if child.ConnectorKey != "email" || child.ConnectionKey != "email-primary" || child.Payload["notification_fallback_parent_id"] != parent.ID || fallbackInt(child.Payload["notification_fallback_hop"]) != 1 {
		t.Fatalf("fallback child lost routing evidence: %#v", child)
	}
}

func TestNotificationFallbackGuardAndParsingEdges(t *testing.T) {
	repository := &independentDeliveryRepository{}
	service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: repository})
	principal := principalmodel.Principal{Principal: identitysdk.Principal{UserID: "worker"}}
	for _, payload := range []map[string]any{
		{},
		{"notification_fallback_plan": []any{}, "notification_fallback_hop": 0},
		{"notification_fallback_plan": []any{map[string]any{}}, "notification_fallback_hop": -1},
		{"notification_fallback_plan": []any{map[string]any{}}, "notification_fallback_hop": 1},
		{"notification_fallback_plan": []any{map[string]any{}}, "notification_fallback_hop": notificationFallbackMaxHops},
	} {
		if err := service.enqueueNotificationFallback(t.Context(), integrationmodel.IntegrationOutboxMessage{ID: "parent", WorkspaceID: "workspace", Payload: payload}, principal); err != nil {
			t.Fatalf("guard payload=%#v err=%v", payload, err)
		}
	}
	longPlan := make([]any, notificationFallbackMaxHops+1)
	for index := range longPlan {
		longPlan[index] = map[string]any{}
	}
	if err := service.enqueueNotificationFallback(t.Context(), integrationmodel.IntegrationOutboxMessage{ID: "max-hop", WorkspaceID: "workspace", Payload: map[string]any{"notification_fallback_plan": longPlan, "notification_fallback_hop": notificationFallbackMaxHops}}, principal); err != nil {
		t.Fatalf("maximum fallback hop error=%v", err)
	}
	invalidPayload := integrationmodel.IntegrationOutboxMessage{ID: "parent", WorkspaceID: "workspace", Payload: map[string]any{"notification_fallback_plan": []any{map[string]any{"payload": "invalid"}}}}
	if err := service.enqueueNotificationFallback(t.Context(), invalidPayload, principal); err == nil {
		t.Fatal("invalid fallback payload accepted")
	}
	incomplete := integrationmodel.IntegrationOutboxMessage{ID: "parent", WorkspaceID: "workspace", Payload: map[string]any{"notification_fallback_plan": []any{map[string]any{"payload": map[string]any{}}}}}
	if err := service.enqueueNotificationFallback(t.Context(), incomplete, principal); err == nil {
		t.Fatal("incomplete fallback target accepted")
	}
	valid := integrationmodel.IntegrationOutboxMessage{ID: "parent", WorkspaceID: "workspace", Payload: map[string]any{
		"notification_fallback_root_id": "root", "notification_fallback_plan": []map[string]any{{"connector_key": "email", "operation": "send", "payload": map[string]any{"notification_deliver_after": "2026-07-20T00:00:00Z"}}},
	}}
	withoutRootOrDelay := integrationmodel.IntegrationOutboxMessage{ID: "parent-no-root", WorkspaceID: "workspace", Payload: map[string]any{"notification_fallback_root_id": "", "notification_fallback_plan": []any{map[string]any{"connector_key": "email", "operation": "send", "payload": map[string]any{"notification_deliver_after": ""}}}}}
	if err := service.enqueueNotificationFallback(t.Context(), withoutRootOrDelay, principal); err != nil {
		t.Fatalf("fallback without root or delay error=%v", err)
	}
	withoutOperation := integrationmodel.IntegrationOutboxMessage{ID: "parent-no-operation", WorkspaceID: "workspace", Payload: map[string]any{"notification_fallback_plan": []any{map[string]any{"connector_key": "email", "payload": map[string]any{}}}}}
	if err := service.enqueueNotificationFallback(t.Context(), withoutOperation, principal); err == nil {
		t.Fatal("fallback without operation accepted")
	}
	repository.insertOutboxErr = errIntegrationManagementTest
	if err := service.enqueueNotificationFallback(t.Context(), valid, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("fallback insert error=%v", err)
	}
	repository.insertOutboxErr = errors.New("duplicate key")
	if err := service.enqueueNotificationFallback(t.Context(), valid, principal); err != nil {
		t.Fatalf("duplicate fallback error=%v", err)
	}
	repository.insertOutboxErr = nil
	beforeSuccessfulInsert := len(repository.outboxes)
	if err := service.enqueueNotificationFallback(t.Context(), valid, principal); err != nil || len(repository.outboxes) != beforeSuccessfulInsert+1 || repository.outboxes[len(repository.outboxes)-1].NextAttemptAt == "" {
		t.Fatalf("fallback outboxes=%#v err=%v", repository.outboxes, err)
	}

	plan := fallbackPlan([]any{"invalid", map[string]any{"key": "value"}})
	if len(plan) != 1 || fallbackPlan("invalid") != nil || len(fallbackPlan([]map[string]any{{"key": "value"}})) != 1 {
		t.Fatalf("parsed plan=%#v", plan)
	}
	for input, want := range map[any]int{1: 1, float64(2): 2, " 3 ": 3, "invalid": 0} {
		if got := fallbackInt(input); got != want {
			t.Fatalf("fallback int %#v=%d want=%d", input, got, want)
		}
	}
}

func TestReconcileMissingIntegrationReceiptsMarksDurableBusinessFactOnce(t *testing.T) {
	repository := &independentDeliveryRepository{invocations: []integrationmodel.IntegrationInvocation{
		{ID: "missing", WorkspaceID: "tenant-a", ConnectorKey: "payments", ConnectionKey: "primary", Operation: "capture", Status: "prepared", RequestRef: "order:42", RecordID: "42", CreatedAt: "2020-01-01T00:00:00Z"},
		{ID: "complete", WorkspaceID: "tenant-a", ConnectorKey: "payments", Operation: "capture", Status: "succeeded", RequestRef: "order:43", ResponseRef: "provider:43", CreatedAt: "2020-01-01T00:00:00Z"},
	}}
	audits := []map[string]any{}
	service := NewIntegrationApplicationService(ApplicationDependencies{
		DeliveryRepository: repository,
		Audit: func(_ context.Context, event, resourceType, resourceID string, _ principalmodel.Principal, _ string, _, _ map[string]any, metadata map[string]any) {
			audits = append(audits, map[string]any{"event": event, "resource_type": resourceType, "resource_id": resourceID, "metadata": metadata})
		},
	})
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test integration receipt reconciliation")
	result, err := service.ReconcileMissingIntegrationReceipts(t.Context(), time.Minute, 10, scope)
	if err != nil || result.Examined != 1 || result.Marked != 1 || result.Skipped != 0 || len(result.Facts) != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if result.Facts[0].Status != "reconciliation_required" || result.Facts[0].RequestRef != "order:42" || result.Facts[0].RecordID != "42" {
		t.Fatalf("fact=%#v", result.Facts[0])
	}
	if len(audits) != 1 || audits[0]["event"] != "integration_invocation_reconciliation_required" {
		t.Fatalf("audits=%#v", audits)
	}
	metadata := audits[0]["metadata"].(map[string]any)
	if metadata["business_fact_exists"] != true || metadata["external_receipt_missing"] != true || metadata["request_ref"] != "order:42" {
		t.Fatalf("metadata=%#v", metadata)
	}
	result, err = service.ReconcileMissingIntegrationReceipts(t.Context(), time.Minute, 10, scope)
	if err != nil || result.Examined != 0 || result.Marked != 0 || len(audits) != 1 {
		t.Fatalf("second result=%#v audits=%#v err=%v", result, audits, err)
	}
}
func (*independentDeliveryRepository) UpdateOutboxStatus(context.Context, string, string, string, string, string) (integrationmodel.IntegrationOutboxMessage, error) {
	return integrationmodel.IntegrationOutboxMessage{}, nil
}
func (*independentDeliveryRepository) UpdateOutboxStatusByResponseRef(context.Context, string, string, string, string, string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	return integrationmodel.IntegrationOutboxMessage{}, false, nil
}
func (r *independentDeliveryRepository) GetOutbox(_ context.Context, workspaceID, messageID string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	for _, message := range r.outboxes {
		if message.WorkspaceID == workspaceID && message.ID == messageID {
			return message, true, nil
		}
	}
	return integrationmodel.IntegrationOutboxMessage{ID: messageID, WorkspaceID: workspaceID, ConnectorKey: "__automation__", Status: "failed"}, true, nil
}
func (r *independentDeliveryRepository) ScheduleOutboxRetry(_ context.Context, workspaceID, messageID string, _ int, errorText string) (integrationmodel.IntegrationOutboxMessage, error) {
	return integrationmodel.IntegrationOutboxMessage{ID: messageID, WorkspaceID: workspaceID, ConnectorKey: "__automation__", Status: "failed", Error: errorText, NextAttemptAt: "scheduled"}, nil
}

type independentAdapter struct{ provider string }

func (adapter independentAdapter) Call(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return integrationcontract.CallResult{Response: map[string]any{"provider": adapter.provider}}, nil
}

func TestApplicationCatalogAndConnectionDoNotRequireRuntimeServices(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}, secrets: map[string]integrationmodel.IntegrationSecret{}}
	delivery := &independentDeliveryRepository{}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key: "webhook", Type: "webhook", Provider: "http", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "http"}},
		Operations: []integrationmodel.ConnectorOperationSchema{{Key: "ping", Method: "POST", SideEffect: "read", Output: []definitionmodel.FieldSchema{{Key: "provider", Type: "text", Required: true}}}},
	}}})
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, DeliveryRepository: delivery, Registry: registry, ConnectorExists: func(key string) bool {
		for _, connector := range registry.Schema().Connectors {
			if connector.Key == key {
				return true
			}
		}
		return false
	}})
	registerTestProviderAdapter(service, "webhook", "http", independentAdapter{provider: "http"})
	admin := integrationWorkspaceAdmin("admin", "isolated")
	if _, err := service.UpsertIntegrationConnection(t.Context(), "hook", integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "webhook", ProviderKey: "http", Status: "configured", Config: map[string]any{"url": "https://example.invalid"}}, admin); err != nil {
		t.Fatal(err)
	}
	catalog, err := service.IntegrationConnectorCatalog(t.Context(), admin)
	if err != nil || len(catalog) != 1 || catalog[0].Readiness != "adapter_ready" {
		t.Fatalf("catalog=%#v err=%v", catalog, err)
	}
	result, err := service.TestConnectorOperation(t.Context(), "hook", ConnectorOperationTestRequest{Operation: "ping", Confirm: true}, admin)
	if err != nil || result.Connection.Status != "verified" || result.Response["provider"] != "http" || len(delivery.invocations) != 1 {
		t.Fatalf("result=%#v invocations=%#v err=%v", result, delivery.invocations, err)
	}
}

func TestBuiltinConnectorRegistrationOwnsContractAndComputesReadiness(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}, secrets: map[string]integrationmodel.IntegrationSecret{}}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "webhook", Type: "webhook", Provider: "custom", Name: "Workspace Webhook", Operations: []integrationmodel.ConnectorOperationSchema{{Key: "custom_send"}}}}})
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry, ConnectorExists: func(key string) bool {
		for _, connector := range registry.Schema().Connectors {
			if connector.Key == key {
				return true
			}
		}
		return false
	}})
	registerTestProviderAdapter(service, "webhook", "http", independentAdapter{provider: "http"})
	service.RegisterBuiltinConnectorDefinitions([]integrationmodel.ConnectorSchema{
		{Key: "webhook", Type: "webhook", Provider: "http", Name: "Builtin Webhook", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "http"}}},
		{Key: "email", Type: "custom", Provider: "smtp", Name: "Email", Source: "plugin:email", DefinitionReady: true},
	})
	admin := integrationWorkspaceAdmin("admin", "workspace")
	catalog, err := service.IntegrationConnectorCatalog(t.Context(), admin)
	if err != nil || len(catalog) != 2 || catalog[1].Provider != "http" || catalog[1].Name != "Builtin Webhook" || catalog[1].Readiness != "adapter_ready" {
		t.Fatalf("catalog=%#v err=%v", catalog, err)
	}
	if _, err := service.UpsertIntegrationConnection(t.Context(), "hook", integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "webhook", ProviderKey: "http", Name: "Hook", Status: "verified", Config: map[string]any{"url": "https://example.invalid/hook"}}, admin); err != nil {
		t.Fatal(err)
	}
	catalog, err = service.IntegrationConnectorCatalog(t.Context(), admin)
	if err != nil || catalog[1].Readiness != "connection_ready" || !catalog[1].ConnectionReady {
		t.Fatalf("catalog=%#v err=%v", catalog, err)
	}
}

func mapValues[K comparable, V any](values map[K]V) []V {
	out := make([]V, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func testErrorCode(err error) string {
	var appError *apperror.AppError
	if errors.As(err, &appError) {
		return appError.Code
	}
	return ""
}

func testErrorParams(err error) map[string]string {
	var appError *apperror.AppError
	if errors.As(err, &appError) {
		return appError.Params
	}
	return nil
}
