// Integration application service secret-redaction tests.
package integration

import (
	"context"
	"encoding/json"
	"errors"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"strings"
	"testing"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"
)

type secretEchoAdapter struct{}

func (secretEchoAdapter) Call(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return integrationcontract.CallResult{Response: map[string]any{
		"safe": "ok", "api_key": "literal-secret", "nested": map[string]any{"access_token": "literal-secret"},
	}, ResponseRef: "provider:200"}, nil
}

type secretErrorAdapter struct{}

func (secretErrorAdapter) Call(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return integrationcontract.CallResult{}, errors.New("provider rejected literal-secret")
}

type countingSyncAdapter struct{ calls *int }

func (a countingSyncAdapter) Call(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	(*a.calls)++
	return integrationcontract.CallResult{ResponseRef: "provider:200"}, nil
}

type blockingSyncAdapter struct {
	entered chan struct{}
	release chan struct{}
}

func secretProbeConnector(providers ...string) integrationmodel.ConnectorSchema {
	definitions := make([]integrationmodel.ConnectorProviderSchema, 0, len(providers))
	for _, provider := range providers {
		definitions = append(definitions, integrationmodel.ConnectorProviderSchema{Key: provider, SecretFields: []definitionmodel.FieldSchema{{Key: "api_key", Type: "text"}}})
	}
	return integrationmodel.ConnectorSchema{
		Key: "probe", Provider: providers[0], Providers: definitions,
		Operations: []integrationmodel.ConnectorOperationSchema{{Key: "echo", SideEffect: "read"}, {Key: "fail", SideEffect: "read"}},
	}
}

func (a blockingSyncAdapter) Call(ctx context.Context, _ integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	close(a.entered)
	select {
	case <-ctx.Done():
		return integrationcontract.CallResult{}, ctx.Err()
	case <-a.release:
		return integrationcontract.CallResult{ResponseRef: "provider:200"}, nil
	}
}

func TestSyncProviderCallDoesNotStartWithoutDurablePreparedInvocation(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{"primary": {Key: "primary", WorkspaceID: "default", ConnectorKey: "probe", ProviderKey: "echo", Status: "active"}}}
	want := errors.New("injected invocation persistence failure")
	delivery := &independentDeliveryRepository{insertInvocationErr: want}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{secretProbeConnector("echo")}})
	application := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, DeliveryRepository: delivery, Registry: registry})
	calls := 0
	registerTestProviderAdapter(application, "probe", "echo", countingSyncAdapter{calls: &calls})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "default", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	if _, err := application.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "probe", ConnectionKey: "primary", Operation: "echo"}, principal); !errors.Is(err, want) {
		t.Fatalf("error=%v", err)
	}
	if calls != 0 {
		t.Fatalf("provider called %d times without durable intent", calls)
	}
}

func TestSyncProviderCallUsesWorkspaceAndProviderCapacity(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{"primary": {Key: "primary", WorkspaceID: "workspace-a", ConnectorKey: "probe", ProviderKey: "echo", Status: "active"}}}
	delivery := &independentDeliveryRepository{}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{secretProbeConnector("echo")}})
	controller := capacityplatform.NewController(capacityplatform.Limits{GlobalInFlight: 8, WorkspaceInFlight: 8, UseCaseInFlight: 8, RetryInFlight: 2, GlobalRate: 10, WorkspaceRate: 1, UseCaseRate: 10, RateWindow: time.Minute}, nil)
	application := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, DeliveryRepository: delivery, Registry: registry, ConnectorCapacity: controller})
	calls := 0
	registerTestProviderAdapter(application, "probe", "echo", countingSyncAdapter{calls: &calls})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	request := SyncCallRequest{ConnectorKey: "probe", ConnectionKey: "primary", Operation: "echo"}
	if _, err := application.ExecuteIntegrationSyncCall(t.Context(), request, principal); err != nil {
		t.Fatal(err)
	}
	_, err := application.ExecuteIntegrationSyncCall(t.Context(), request, principal)
	if apperror.KindOf(err) != apperror.KindRateLimited || apperror.CodeOf(err) != "backend.integration.sync_call.capacity_exhausted" {
		t.Fatalf("unexpected capacity error: kind=%q code=%q err=%v", apperror.KindOf(err), apperror.CodeOf(err), err)
	}
	if calls != 1 {
		t.Fatalf("provider called %d times after capacity exhaustion", calls)
	}
}

func TestSlowProviderDoesNotConsumeAnotherProviderCapacity(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{
		"slow-a": {Key: "slow-a", WorkspaceID: "workspace-a", ConnectorKey: "probe", ProviderKey: "slow", Status: "active"},
		"slow-b": {Key: "slow-b", WorkspaceID: "workspace-b", ConnectorKey: "probe", ProviderKey: "slow", Status: "active"},
		"fast-b": {Key: "fast-b", WorkspaceID: "workspace-b", ConnectorKey: "probe", ProviderKey: "fast", Status: "active"},
	}}
	delivery := &independentDeliveryRepository{}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{secretProbeConnector("slow", "fast")}})
	controller := capacityplatform.NewController(capacityplatform.Limits{GlobalInFlight: 2, WorkspaceInFlight: 1, UseCaseInFlight: 1, RetryInFlight: 1, GlobalRate: 100, WorkspaceRate: 100, UseCaseRate: 100}, nil)
	application := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, DeliveryRepository: delivery, Registry: registry, ConnectorCapacity: controller})
	entered, release := make(chan struct{}), make(chan struct{})
	registerTestProviderAdapter(application, "probe", "slow", blockingSyncAdapter{entered: entered, release: release})
	fastCalls := 0
	registerTestProviderAdapter(application, "probe", "fast", countingSyncAdapter{calls: &fastCalls})
	principalA := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin-a"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	principalB := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-b", UserID: "admin-b"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	firstDone := make(chan error, 1)
	go func() {
		_, err := application.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "probe", ConnectionKey: "slow-a", Operation: "echo"}, principalA)
		firstDone <- err
	}()
	<-entered
	if _, err := application.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "probe", ConnectionKey: "slow-b", Operation: "echo"}, principalB); apperror.KindOf(err) != apperror.KindRateLimited {
		t.Fatalf("same slow provider was not isolated: %v", err)
	}
	if _, err := application.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "probe", ConnectionKey: "fast-b", Operation: "echo"}, principalB); err != nil || fastCalls != 1 {
		t.Fatalf("independent provider was starved: calls=%d err=%v", fastCalls, err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationSecretNeverLeavesThroughResponseInvocationOrAudit(t *testing.T) {
	repository := &independentConfigRepository{
		connections: map[string]integrationmodel.IntegrationConnection{"primary": {Key: "primary", WorkspaceID: "default", ConnectorKey: "probe", ProviderKey: "echo", Status: "active", SecretRefs: map[string]string{"api_key": "secret:credential"}}},
		secrets:     map[string]integrationmodel.IntegrationSecret{"credential": {Key: "credential", WorkspaceID: "default", Kind: "api_key", Status: "active", ValueRef: "material:credential"}},
		materials:   map[string]string{"default:credential": "literal-secret"},
	}
	delivery := &independentDeliveryRepository{}
	audits := []map[string]any{}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{secretProbeConnector("echo")}})
	application := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository, DeliveryRepository: delivery, Registry: registry,
		Audit: func(_ context.Context, _, _, _ string, _ principalmodel.Principal, _ string, before, after, metadata map[string]any) {
			audits = append(audits, map[string]any{"before": before, "after": after, "metadata": metadata})
		},
	})
	registerTestProviderAdapter(application, "probe", "echo", secretEchoAdapter{})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "default", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	result, err := application.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "probe", ConnectionKey: "primary", Operation: "echo", Request: map[string]any{"password": "literal-secret"}}, principal)
	if err != nil {
		t.Fatal(err)
	}
	for label, value := range map[string]any{"response": result.Response, "invocation": result.ActionInvocation, "audits": audits} {
		payload, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if strings.Contains(string(payload), "literal-secret") {
			t.Fatalf("Secret leaked through %s: %s", label, payload)
		}
	}
	if result.Response["safe"] != "ok" || result.Response["api_key"] != "[REDACTED]" {
		t.Fatalf("unexpected redacted response: %#v", result.Response)
	}
}

func TestIntegrationSecretIsRedactedFromProviderErrorAndFailedInvocation(t *testing.T) {
	repository := &independentConfigRepository{
		connections: map[string]integrationmodel.IntegrationConnection{"primary": {Key: "primary", WorkspaceID: "default", ConnectorKey: "probe", ProviderKey: "error", Status: "active", SecretRefs: map[string]string{"api_key": "secret:credential"}}},
		secrets:     map[string]integrationmodel.IntegrationSecret{"credential": {Key: "credential", WorkspaceID: "default", Kind: "api_key", Status: "active", ValueRef: "material:credential"}},
		materials:   map[string]string{"default:credential": "literal-secret"},
	}
	delivery := &independentDeliveryRepository{}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{secretProbeConnector("error")}})
	application := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, DeliveryRepository: delivery, Registry: registry})
	registerTestProviderAdapter(application, "probe", "error", secretErrorAdapter{})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "default", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	_, err := application.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "probe", ConnectionKey: "primary", Operation: "fail"}, principal)
	if err == nil || strings.Contains(err.Error(), "literal-secret") {
		t.Fatalf("Secret leaked through Provider error: %v", err)
	}
	payload, marshalErr := json.Marshal(delivery.invocations)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(payload), "literal-secret") || !strings.Contains(string(payload), "[REDACTED]") {
		t.Fatalf("failed Invocation did not redact Secret: %s", payload)
	}
}

func TestProviderResponsePreservesBusinessCursorButRedactsActualSecret(t *testing.T) {
	response := RedactProviderResponse(map[string]any{
		"nextSyncToken": "calendar-next", "opaque_cursor": "cursor-1",
		"echo": "prefix literal-secret suffix", "access_token": "different-value",
	}, map[string]string{"api_key": "literal-secret"})
	if response["nextSyncToken"] != "calendar-next" || response["opaque_cursor"] != "cursor-1" {
		t.Fatalf("domain cursors were redacted: %#v", response)
	}
	if response["echo"] != "prefix [REDACTED] suffix" || response["access_token"] != "[REDACTED]" {
		t.Fatalf("credential output was not redacted: %#v", response)
	}
}
