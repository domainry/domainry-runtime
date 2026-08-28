package action

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
)

func TestActionReadEffectAndPhaseRemainingConditions(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{Permissions: []string{"existing.read"}})
	if got := actionReadEffectAuthorizationPrincipal(principal, nil, "booking"); len(got.PermissionKeys()) != 1 {
		t.Fatalf("denied permissions=%v", got.PermissionKeys())
	}
	set := &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{{ObjectKey: "booking"}}}
	if got := actionReadEffectAuthorizationPrincipal(principal, set, " booking "); len(got.PermissionKeys()) != 2 || !got.HasPermission("booking.read") {
		t.Fatalf("authorized permissions=%v", got.PermissionKeys())
	}
	var machine *actionExecutionPhaseMachine
	if machine.current() != runtimeext.ExecutionPhaseRolledBack || machine.beginWriting() == nil || machine.transition(runtimeext.ExecutionPhaseCommitted) == nil {
		t.Fatal("nil phase machine accepted transition")
	}
}

func TestBusinessHandlerIdentityRemainingConditions(t *testing.T) {
	var nilExecutor *BusinessHandlerExecutor
	if len(nilExecutor.ValidationErrors()) != 1 {
		t.Fatal("nil executor validation missing")
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}
	invocation := actionmodel.ActionInvocation{Principal: principal}
	action := definitionmodel.ActionSchema{Key: "booking.reserve", ObjectKey: "booking"}
	descriptor := runtimeext.HandlerDescriptor{HandlerRevision: "handler"}
	static := NewBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{RuntimeRevision: "runtime", ProjectRevision: "project", MetadataRevision: "metadata"})
	if _, err := static.executionIdentity(t.Context(), invocation, action, descriptor, "execution"); err != nil {
		t.Fatalf("static identity: %v", err)
	}
	resolved := NewBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{RuntimeRevision: "runtime", ProjectRevision: "project", ResolveMetadataRevision: func(context.Context, principalmodel.Principal) (string, error) { return "metadata", nil }})
	if failures := resolved.ValidationErrors(); len(failures) != 0 {
		t.Fatalf("resolver validation=%v", failures)
	}
	if _, err := resolved.executionIdentity(t.Context(), invocation, action, descriptor, "execution"); err != nil {
		t.Fatalf("resolved identity: %v", err)
	}
	failing := NewBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{RuntimeRevision: "runtime", ProjectRevision: "project", ResolveMetadataRevision: func(context.Context, principalmodel.Principal) (string, error) { return "", errors.New("revision") }})
	if _, err := failing.executionIdentity(t.Context(), invocation, action, descriptor, "execution"); err == nil {
		t.Fatal("revision error ignored")
	}
	for _, dependencies := range []BusinessHandlerExecutionDependencies{
		{ProjectRevision: "project", MetadataRevision: "metadata"},
		{RuntimeRevision: "runtime", MetadataRevision: "metadata"},
		{RuntimeRevision: "runtime", ProjectRevision: "project"},
	} {
		if _, err := NewBusinessHandlerExecutor(dependencies).executionIdentity(t.Context(), invocation, action, descriptor, "execution"); err == nil {
			t.Fatalf("incomplete identity accepted: %+v", dependencies)
		}
	}
}

func TestActionRecordAndHandlerInputRemainingConditions(t *testing.T) {
	records := map[string]recordmodel.Record{"other\x00one": {ID: "other"}, "booking\x00one": {ID: "one"}}
	if record, found := singleActionObjectRecord(records, "booking"); !found || record.ID != "one" {
		t.Fatalf("record=%+v found=%v", record, found)
	}
	records["booking\x00two"] = recordmodel.Record{ID: "two"}
	if _, found := singleActionObjectRecord(records, "booking"); found {
		t.Fatal("ambiguous record resolved")
	}
	action := definitionmodel.ActionSchema{PayloadFields: []definitionmodel.ActionPayloadField{{Key: " "}, {Key: "present"}, {Key: "missing"}}}
	input := businessHandlerInput(action, map[string]any{"present": 1})
	if len(input) != 1 || input["present"] != 1 {
		t.Fatalf("input=%v", input)
	}
}

func TestActionInvocationNormalizationAndErrorClasses(t *testing.T) {
	invocation := ActionNormalizeInvocation(actionmodel.ActionInvocation{RequestID: " request ", Input: map[string]any{}})
	if invocation.RequestID != "request" {
		t.Fatalf("request id=%q", invocation.RequestID)
	}
	if normalizeActionInvocationError(nil) != nil {
		t.Fatal("nil error changed")
	}
	classified := []error{
		mutation.BusinessConflict("backend.booking.full", "booking", "one", "status"),
		mutation.MutationConflict("booking", "one", mutation.MutationConflictOptimistic, errors.New("changed")),
		mutation.TransactionTransient("booking", "one", mutation.TransactionTransientDeadlock, errors.New("deadlock")),
		mutation.TransactionCommitUnknown("booking", "one", errors.New("unknown")),
	}
	for _, err := range classified {
		if normalized := normalizeActionInvocationError(err); normalized == err || normalized == nil {
			t.Fatalf("error not normalized: %T", err)
		}
	}
	if result, err := failInvocation(actionmodel.ActionInvocationResult{}, nil); err != nil || result.Status != "failed" {
		t.Fatalf("nil failure result=%+v err=%v", result, err)
	}
}
