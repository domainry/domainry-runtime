package runtime

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type actionReceiptReadProbe struct {
	executionRepositoryProbe
	found bool
	reads int
	scope actionmodel.ActionBusinessExecution
}

func (p *actionReceiptReadProbe) FindExecution(_ context.Context, scope actionmodel.ActionBusinessExecution) (actionmodel.ActionBusinessExecution, bool, error) {
	p.reads++
	p.scope = scope
	return p.execution, p.found, nil
}

func (*actionReceiptReadProbe) TryBeginExecution(context.Context, actionmodel.ActionExecutionClaimRequest) (actionmodel.ActionExecutionClaimResult, error) {
	panic("receipt inspection must not acquire an execution")
}

func TestExecutionRuntimeReadReceiptRequiresOwnedExactUnexpiredScope(t *testing.T) {
	p := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "owner"}}
	input := idempotency.FingerprintInput{UseCase: "action.invoke", ResourceType: "action", TargetID: "customer.rename", Payload: map[string]any{"name": "new"}}
	fingerprint, err := idempotency.Fingerprint(input)
	if err != nil {
		t.Fatal(err)
	}
	base := actionmodel.ActionBusinessExecution{ID: "receipt", WorkspaceID: p.WorkspaceID, ObjectKey: "customer", RecordID: "customer-1", ActionKey: "customer.rename", IdempotencyKey: "call", ActorID: p.UserID, RequestFingerprint: fingerprint, Status: string(idempotency.StatusSucceeded), Result: map[string]any{"action_key": "customer.rename"}, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}
	for _, status := range []idempotency.Status{idempotency.StatusSucceeded, idempotency.StatusProcessing, idempotency.StatusFailedTerminal, idempotency.StatusFailedRetryable} {
		receipt := base
		receipt.Status = string(status)
		repo := &actionReceiptReadProbe{executionRepositoryProbe: executionRepositoryProbe{execution: receipt}, found: true}
		got, found, err := NewActionExecutionRuntime(repo).ReadReceipt(t.Context(), "customer", "customer-1", "customer.rename", "call", input, p)
		if err != nil || !found || repo.reads != 1 || !reflect.DeepEqual(got, receipt) || !reflect.DeepEqual(repo.execution, receipt) {
			t.Fatal(got, found, err)
		}
	}
	for _, field := range []string{"actor", "workspace", "object", "record", "action", "key", "fingerprint", "expired", "invalid-expiry"} {
		t.Run(field, func(t *testing.T) {
			receipt := base
			switch field {
			case "actor":
				receipt.ActorID = "other"
			case "workspace":
				receipt.WorkspaceID = "other"
			case "object":
				receipt.ObjectKey = "other"
			case "record":
				receipt.RecordID = "other"
			case "action":
				receipt.ActionKey = "other"
			case "key":
				receipt.IdempotencyKey = "other"
			case "fingerprint":
				receipt.RequestFingerprint = "other"
			case "expired":
				receipt.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
			case "invalid-expiry":
				receipt.ExpiresAt = "not-a-time"
			}
			repo := &actionReceiptReadProbe{executionRepositoryProbe: executionRepositoryProbe{execution: receipt}, found: true}
			got, found, err := NewActionExecutionRuntime(repo).ReadReceipt(t.Context(), "customer", "customer-1", "customer.rename", "call", input, p)
			if err == nil || found || got.ID != "" || !reflect.DeepEqual(repo.execution, receipt) {
				t.Fatal("invalid receipt accepted or modified", got, found, err)
			}
		})
	}
	repo := &actionReceiptReadProbe{}
	if _, found, err := NewActionExecutionRuntime(repo).ReadReceipt(t.Context(), "customer", "customer-1", "customer.rename", "missing", input, p); err != nil || found || repo.scope.IdempotencyKey != "missing" {
		t.Fatal(found, err)
	}
	p.Known = false
	if _, _, err := NewActionExecutionRuntime(repo).ReadReceipt(t.Context(), "customer", "customer-1", "customer.rename", "missing", input, p); apperror.KindOf(err) != apperror.KindForbidden || repo.reads != 1 {
		t.Fatal("unknown caller reached lookup", err)
	}
}
