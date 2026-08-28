// Integration application service outbox tests.
package integration

import (
	"context"
	"errors"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestSendIntegrationOutboxMessageDispatchesAutomation(t *testing.T) {
	executed := false
	service := NewIntegrationApplicationService(ApplicationDependencies{AutomationOutboxExecutor: func(_ context.Context, message integrationmodel.IntegrationOutboxMessage) error {
		executed = true
		if message.Operation != "recalculate" {
			t.Fatalf("operation = %q", message.Operation)
		}
		return nil
	}, AdapterOutboxSender: func(context.Context, integrationmodel.IntegrationOutboxMessage, principalmodel.Principal) (OutboxSendResult, error) {
		t.Fatal("adapter sender must not receive automation messages")
		return OutboxSendResult{}, nil
	}})

	result, err := service.SendIntegrationOutboxMessage(context.Background(), integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace", ConnectorKey: " __automation__ ", Operation: "recalculate"}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}})
	if err != nil {
		t.Fatal(err)
	}
	if !executed || result.Status != "sent" || result.ResponseRef != "automation:recalculate" {
		t.Fatalf("result = %#v, executed = %v", result, executed)
	}
}

func TestSendIntegrationOutboxMessageAuthorizationAndAutomationFailureEdges(t *testing.T) {
	wantErr := errors.New("automation failed")
	service := NewIntegrationApplicationService(ApplicationDependencies{AutomationOutboxExecutor: func(context.Context, integrationmodel.IntegrationOutboxMessage) error { return wantErr }})
	message := integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace", ConnectorKey: "__automation__", Operation: "run"}
	if _, err := service.SendIntegrationOutboxMessage(t.Context(), message, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error=%v", err)
	}
	missingWorkspace := message
	missingWorkspace.WorkspaceID = ""
	if _, err := service.SendIntegrationOutboxMessage(t.Context(), missingWorkspace, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("message workspace error=%v", err)
	}
	if _, err := service.SendIntegrationOutboxMessage(t.Context(), message, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "other"}}); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("workspace mismatch error=%v", err)
	}
	if _, err := service.SendIntegrationOutboxMessage(t.Context(), message, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}); !errors.Is(err, wantErr) {
		t.Fatalf("automation error=%v", err)
	}
}

func TestSendIntegrationOutboxMessageDispatchesAdapterAndPreservesError(t *testing.T) {
	wantErr := errors.New("provider unavailable")
	service := NewIntegrationApplicationService(ApplicationDependencies{AdapterOutboxSender: func(_ context.Context, message integrationmodel.IntegrationOutboxMessage, principal principalmodel.Principal) (OutboxSendResult, error) {
		if message.ConnectorKey != "email" || principal.UserID != "user_1" {
			t.Fatalf("message = %#v, principal = %#v", message, principal)
		}
		return OutboxSendResult{ResponseRef: "provider:failed"}, wantErr
	}})

	result, err := service.SendIntegrationOutboxMessage(context.Background(), integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace", ConnectorKey: "email"}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "user_1"}})
	if !errors.Is(err, wantErr) || result.ResponseRef != "provider:failed" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}
