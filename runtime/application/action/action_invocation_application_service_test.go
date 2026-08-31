package action

import (
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestActionInvocationNormalizationAndProjection(t *testing.T) {
	actor := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "actor-space"}, RequestID: "actor-request"}
	runAs := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "run-as-space"}, RequestID: "run-as-request"}
	normalized := ActionNormalizeInvocation(actionmodel.ActionInvocation{ActionKey: " approve ", ObjectKey: " order ", RecordID: " one ", Actor: actor, RunAs: runAs})
	if normalized.ActionKey != "approve" || normalized.ObjectKey != "order" || normalized.RecordID != "one" || normalized.Principal.WorkspaceID != "run-as-space" || normalized.RequestID != "run-as-request" || normalized.Input == nil || normalized.Source != "" {
		t.Fatalf("normalized=%+v", normalized)
	}
	for _, source := range []actionmodel.ActionSource{ActionSourceHTTP, ActionSourceWorkflow, ActionSourceAutomation, actionmodel.ActionSourceRecordTimer, ActionSourceIntegration, ActionSourceAgent, ActionSourceNested, ActionSourceBulk} {
		if !actionSourceValid(source) {
			t.Fatalf("valid source rejected: %q", source)
		}
	}
	if actionSourceValid("") || actionSourceValid("forged") {
		t.Fatal("empty or unknown source accepted")
	}
	recordOutput := ActionRecordInvocationOutput(actionmodel.ActionResult{ActionKey: "approve", Record: recordmodel.Record{ID: "one"}, Output: map[string]any{"approved": true}})
	if recordOutput["action_key"] != "approve" || recordOutput["record"] == nil || recordOutput["data"].(map[string]any)["approved"] != true {
		t.Fatalf("record output=%#v", recordOutput)
	}
	objectOutput := ActionObjectInvocationOutput(actionmodel.ActionObjectResult{ActionKey: "create", Output: map[string]any{"ok": true}})
	if objectOutput["action_key"] != "create" || objectOutput["data"] == nil {
		t.Fatalf("object output=%#v", objectOutput)
	}
	if NewActionInvocationID(t.Context()) == "" {
		t.Fatal("generated invocation id is empty")
	}
}

func TestActionInvocationFailureContract(t *testing.T) {
	want := &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.action.denied"}
	result, err := failInvocation(actionmodel.ActionInvocationResult{Status: "running"}, want)
	if !errors.Is(err, want) || result.Status != "failed" || result.ErrorCode != "backend.action.denied" || result.Retryable {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	internal := errors.New("internal")
	result, err = failInvocation(actionmodel.ActionInvocationResult{}, internal)
	if !errors.Is(err, internal) || !result.Retryable || result.ErrorCode != "backend.internal" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestActionReceiptResultUsesOwnerResultInsteadOfInvocationEnvelope(t *testing.T) {
	record := actionmodel.ActionResult{ActionKey: "order.update", RecordID: "order-1"}
	raw, err := actionReceiptResult(actionmodel.ActionInvocationResult{Record: &record})
	if got, ok := raw.(actionmodel.ActionResult); err != nil || !ok || got.RecordID != "order-1" {
		t.Fatalf("record receipt=%#v", got)
	}
	object := actionmodel.ActionObjectResult{ActionKey: "group_class.book_class", Output: map[string]any{"booking_id": "booking-1"}}
	raw, err = actionReceiptResult(actionmodel.ActionInvocationResult{Object: &object})
	if got, ok := raw.(actionmodel.ActionObjectResult); err != nil || !ok || got.Output["booking_id"] != "booking-1" {
		t.Fatalf("object receipt=%#v", got)
	}
	if _, err := actionReceiptResult(actionmodel.ActionInvocationResult{}); apperror.CodeOf(err) != "backend.action.result_invalid" {
		t.Fatalf("missing owner result error=%v", err)
	}
	if _, err := actionReceiptResult(actionmodel.ActionInvocationResult{Record: &record, Object: &object}); apperror.CodeOf(err) != "backend.action.result_invalid" {
		t.Fatalf("ambiguous owner result error=%v", err)
	}
}
