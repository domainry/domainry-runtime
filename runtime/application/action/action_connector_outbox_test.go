package action

import (
	"context"
	"errors"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestBusinessActionExecutionRejectsConnectorCallsOutsidePublishedGrant(t *testing.T) {
	readGrant := runtimeext.ActionConnectorCapability{
		ConnectorKey: "directory", ConnectionKey: "primary", OperationKey: "lookup",
		ContractSHA256: strings.Repeat("a", 64), Mode: runtimeext.ConnectorModeCall, Effect: runtimeext.ConnectorEffectRead,
	}
	execution := &businessActionExecution{unitOfWork: newActionTestUnitOfWork(), connectorGrants: []runtimeext.ActionConnectorCapability{readGrant}}
	if lease, err := execution.AcquireSynchronousConnectorCall(readGrant); err != nil {
		t.Fatal(err)
	} else {
		lease.Release()
	}
	for name, requested := range map[string]runtimeext.ActionConnectorCapability{
		"ungranted operation": {ConnectorKey: "directory", ConnectionKey: "primary", OperationKey: "search", ContractSHA256: strings.Repeat("b", 64), Mode: runtimeext.ConnectorModeCall, Effect: runtimeext.ConnectorEffectRead},
		"forged contract":     {ConnectorKey: "directory", ConnectionKey: "primary", OperationKey: "lookup", ContractSHA256: strings.Repeat("b", 64), Mode: runtimeext.ConnectorModeCall, Effect: runtimeext.ConnectorEffectRead},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := execution.AcquireSynchronousConnectorCall(requested); apperror.CodeOf(err) != runtimeext.ConnectorActionGrantDeniedErrorCode {
				t.Fatalf("error=%v", err)
			}
		})
	}
	writeCall := readGrant
	writeCall.Effect = runtimeext.ConnectorEffectWrite
	if _, err := execution.AcquireSynchronousConnectorCall(writeCall); apperror.CodeOf(err) != runtimeext.ConnectorActionSideEffectOutboxErrorCode {
		t.Fatalf("synchronous side effect error=%v", err)
	}
}

func TestBusinessActionExecutionStagesOnlyValidatedGrantedOutboxIntent(t *testing.T) {
	grant := runtimeext.ActionConnectorCapability{
		ConnectorKey: "email", ConnectionKey: "primary", OperationKey: "send",
		ContractSHA256: strings.Repeat("c", 64), Mode: runtimeext.ConnectorModeEnqueue, Effect: runtimeext.ConnectorEffectWrite,
	}
	validated := 0
	execution := &businessActionExecution{
		unitOfWork: newActionTestUnitOfWork(), identity: runtimeext.ExecutionIdentity{ExecutionID: "execution-1"},
		invocation: actionTestInvocation(), connectorGrants: []runtimeext.ActionConnectorCapability{grant},
		dependencies: BusinessHandlerExecutionDependencies{ValidateDurableIntent: func(_ context.Context, intent runtimeext.DurableIntent, principal principalmodel.Principal) error {
			validated++
			if intent.OperationKey != "send" || principal.WorkspaceID != "workspace-a" {
				t.Fatalf("intent=%+v principal=%+v", intent, principal)
			}
			return nil
		}},
	}
	intent := runtimeext.DurableIntent{ConsumerKey: "email", ConnectionKey: "primary", OperationKey: "send", ContractSHA256: strings.Repeat("c", 64), Payload: map[string]any{"to": "member@example.com"}}
	receipt, err := execution.StageDurableIntent(t.Context(), intent)
	if err != nil || receipt.ID != "durable_intent:execution-1:0" || validated != 1 || len(execution.intents) != 1 {
		t.Fatalf("receipt=%+v validated=%d intents=%+v error=%v", receipt, validated, execution.intents, err)
	}

	for name, mutate := range map[string]func(*runtimeext.DurableIntent){
		"self-bound ungranted connection": func(value *runtimeext.DurableIntent) { value.ConnectionKey = "secondary" },
		"forged contract":                 func(value *runtimeext.DurableIntent) { value.ContractSHA256 = strings.Repeat("d", 64) },
		"self-bound ungranted operation":  func(value *runtimeext.DurableIntent) { value.OperationKey = "admin_send" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := intent
			mutate(&candidate)
			if _, err := execution.StageDurableIntent(t.Context(), candidate); apperror.CodeOf(err) != runtimeext.ConnectorActionGrantDeniedErrorCode {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if validated != 1 || len(execution.intents) != 1 {
		t.Fatalf("rejected intent reached validator or UoW: validated=%d intents=%d", validated, len(execution.intents))
	}

	want := errors.New("catalog rejected")
	execution.dependencies.ValidateDurableIntent = func(context.Context, runtimeext.DurableIntent, principalmodel.Principal) error { return want }
	if _, err := execution.StageDurableIntent(t.Context(), intent); !errors.Is(err, want) || len(execution.intents) != 1 {
		t.Fatalf("catalog rejection error=%v intents=%d", err, len(execution.intents))
	}
}

func actionTestInvocation() (invocation actionmodel.ActionInvocation) {
	invocation.Principal = principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-1"}}
	return invocation
}
