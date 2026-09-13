package action

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionruntime "github.com/domainry/domainry-runtime/runtime/domain/action/runtime"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type receiptReadStore struct {
	actionUnitOfWorkStoreProbe
	value         actionmodel.ActionBusinessExecution
	reads, claims int
}

func (s *receiptReadStore) FindExecution(context.Context, actionmodel.ActionBusinessExecution) (actionmodel.ActionBusinessExecution, bool, error) {
	s.reads++
	return s.value, true, nil
}
func (s *receiptReadStore) TryBeginExecution(context.Context, actionmodel.ActionExecutionClaimRequest) (actionmodel.ActionExecutionClaimResult, error) {
	s.claims++
	return actionmodel.ActionExecutionClaimResult{}, fmt.Errorf("receipt reader must not claim execution")
}

func TestActionIndependentReceiptReadPreservesOwnershipAllReferencesAndNoEffects(t *testing.T) {
	definition := definitionmodel.ActionSchema{Key: "order.rename", ObjectKey: "order", Kind: definitionmodel.ActionKindRecordUpdate, PayloadFields: []definitionmodel.ActionPayloadField{{Key: "name", Type: "text", Required: true}}}
	permission := "action.receipt.order.rename.read"
	principal := func(grants ...string) principalmodel.Principal {
		return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "reader", WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: grants, DataPolicies: accessfixture.DataPoliciesForPermissions(grants, identitysdk.DataScopeOwner)})
	}
	in := actionmodel.ActionInvocation{ActionKey: definition.Key, ObjectKey: "order", RecordID: "target", IdempotencyKey: "original", Input: map[string]any{"name": "Approved"}, Principal: principal(permission, "order.read")}
	fingerprint, err := idempotency.Fingerprint(actionInvocationFingerprint(in, in.ActionKey))
	if err != nil {
		t.Fatal(err)
	}
	refs := []actionmodel.ActionObjectRecordRef{}
	for i := 0; i < 101; i++ {
		refs = append(refs, actionmodel.ActionObjectRecordRef{ObjectKey: "order", RecordID: fmt.Sprintf("ref-%d", i)})
	}
	raw, _ := json.Marshal(actionmodel.ActionResult{ActionKey: definition.Key, ObjectKey: "order", RecordID: "target", CreatedRecords: refs, Message: "private-message", Output: map[string]any{"private": "private-handler-secret"}})
	var saved map[string]any
	_ = json.Unmarshal(raw, &saved)
	store := &receiptReadStore{value: actionmodel.ActionBusinessExecution{WorkspaceID: "workspace", ActorID: "reader", ActionKey: definition.Key, ObjectKey: "order", RecordID: "target", IdempotencyKey: "original", RequestFingerprint: fingerprint, Status: string(idempotency.StatusSucceeded), Result: saved, ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano)}}
	system := NewRuntimeSystemOperationCatalog()
	handlers := runtimeext.NewBusinessHandlerRegistry()
	handlers.Freeze()
	denied, assurance := "", 0
	service := NewActionApplication(ActionApplicationDependencies{Catalog: NewActionCatalog([]definitionmodel.ActionSchema{definition}, system, handlers), UnitOfWork: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)), Authorization: ActionAuthorization{ObjectForAction: func(p principalmodel.Principal, key, op string) (definitionmodel.ObjectSchema, error) {
		if op != "read" || !p.HasExactPermission(key+".read") {
			return definitionmodel.ObjectSchema{}, receiptReadDenied()
		}
		return definitionmodel.ObjectSchema{Key: key}, nil
	}}, ReceiptRecordReadable: func(_ context.Context, object, record string, p principalmodel.Principal) (bool, error) {
		return p.UserID == "reader" && p.WorkspaceID == "workspace" && object == "order" && p.HasExactPermission("order.read") && record != denied, nil
	}, Assurance: ActionAssurance{Validate: func(context.Context, actionmodel.ActionInvocation) (map[string]string, error) {
		assurance++
		return nil, nil
	}}})
	out, err := service.ReadInvocationReceipt(t.Context(), in)
	if err != nil || !out.Found || out.InvocationID != "original" || len(out.CreatedRecords) != 101 {
		t.Fatal(out, err)
	}
	encoded, _ := json.Marshal(out)
	if strings.Contains(string(encoded), "private") {
		t.Fatal("raw owner data escaped", string(encoded))
	}
	if _, err := service.InspectInvocation(t.Context(), in); err == nil {
		t.Fatal("execution inspection no longer requires write grant")
	}
	for _, change := range []string{"receipt-grant", "record-grant", "guardrail", "actor", "workspace", "input", "receipt-expiry", "malformed-result", "target", "reference-after-100", "run-as", "missing-record-port"} {
		t.Run(change, func(t *testing.T) {
			bad := in
			old := store.value
			port := service.dependencies.ReceiptRecordReadable
			defer func() { store.value = old; service.dependencies.ReceiptRecordReadable = port; denied = "" }()
			switch change {
			case "receipt-grant":
				bad.Principal = principal("order.read")
			case "record-grant":
				bad.Principal = principal(permission)
			case "guardrail":
				bad.Principal = accessfixture.Attach(in.Principal, accessfixture.Bundle{Permissions: []string{permission, "order.read"}, DataPolicies: accessfixture.DataPoliciesForPermissions([]string{permission, "order.read"}, identitysdk.DataScopeOwner), Guardrails: []accessfixture.GuardrailFixture{{Key: "receipt-deny", DeniedPermissionKeys: []string{permission}}}})
			case "actor":
				store.value.ActorID = "foreign"
			case "workspace":
				store.value.WorkspaceID = "foreign"
			case "input":
				bad.Input = map[string]any{"name": "Changed"}
			case "receipt-expiry":
				store.value.ExpiresAt = time.Now().Add(-time.Minute).Format(time.RFC3339Nano)
			case "malformed-result":
				store.value.Result = map[string]any{"action_key": "foreign", "object_key": in.ObjectKey, "record_id": in.RecordID}
			case "target":
				denied = "target"
			case "reference-after-100":
				denied = "ref-100"
			case "run-as":
				bad.RunAs = in.Principal
			case "missing-record-port":
				service.dependencies.ReceiptRecordReadable = nil
			}
			if _, err := service.ReadInvocationReceipt(t.Context(), bad); err == nil {
				t.Fatal("receipt policy bypassed", change)
			}
		})
	}
	if store.claims != 0 || store.beginTransactionCalls != 0 || store.completeCalls != 0 || assurance != 0 {
		t.Fatal("read caused execution effects", store, assurance)
	}
}
