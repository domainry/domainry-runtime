package action

import (
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func actionMutationEdgePlan(t *testing.T, operation, objectKey, recordID string) transactionmodel.MutationPlan {
	t.Helper()
	mutationContext, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{
		WorkspaceID: "workspace", Source: transactionmodel.MutationSourceAction,
		ActionKey: "action", CorrelationID: "correlation", MetadataRevision: "metadata",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := transactionmodel.NewMutationPlan(mutationContext, transactionmodel.RecordMutationCommit{
		Operation: operation, Object: definitionmodel.ObjectSchema{Key: objectKey},
		Record: recordmodel.Record{ID: recordID}, RecordID: recordID,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestBusinessActionConnectorAndIntentEdges(t *testing.T) {
	var nilExecution *businessActionExecution
	if _, err := nilExecution.AcquireSynchronousConnectorCall(runtimeext.ActionConnectorCapability{}); apperror.CodeOf(err) != runtimeext.ConnectorActionExecutionRequiredErrorCode {
		t.Fatalf("nil execution error=%v", err)
	}
	readGrant := runtimeext.ActionConnectorCapability{
		ConnectorKey: "directory", ConnectionKey: "primary", OperationKey: "lookup",
		ContractSHA256: strings.Repeat("a", 64), Mode: runtimeext.ConnectorModeCall, Effect: runtimeext.ConnectorEffectRead,
	}
	execution := &businessActionExecution{unitOfWork: newActionTestUnitOfWork(), connectorGrants: []runtimeext.ActionConnectorCapability{readGrant}}
	badMode := readGrant
	badMode.Mode = runtimeext.ConnectorModeEnqueue
	if _, err := execution.AcquireSynchronousConnectorCall(badMode); apperror.CodeOf(err) != runtimeext.ConnectorActionSideEffectOutboxErrorCode {
		t.Fatalf("bad mode error=%v", err)
	}
	if execution.hasConnectorGrant(runtimeext.ActionConnectorCapability{}) {
		t.Fatal("invalid connector grant matched")
	}

	if _, err := execution.StageDurableIntent(t.Context(), runtimeext.DurableIntent{}); apperror.CodeOf(err) != "backend.action.durable_intent_invalid" {
		t.Fatalf("invalid intent error=%v", err)
	}
	intent := runtimeext.DurableIntent{
		ConsumerKey: "email", ConnectionKey: "primary", OperationKey: "send",
		ContractSHA256: strings.Repeat("c", 64),
	}
	for _, grant := range []runtimeext.ActionConnectorCapability{
		{ConnectorKey: "email", ConnectionKey: "primary", OperationKey: "send", ContractSHA256: intent.ContractSHA256, Mode: runtimeext.ConnectorModeCall, Effect: runtimeext.ConnectorEffectWrite},
		{ConnectorKey: "email", ConnectionKey: "primary", OperationKey: "send", ContractSHA256: intent.ContractSHA256, Mode: runtimeext.ConnectorModeEnqueue, Effect: runtimeext.ConnectorEffectRead},
	} {
		execution.connectorGrants = []runtimeext.ActionConnectorCapability{grant}
		if _, err := execution.StageDurableIntent(t.Context(), intent); apperror.CodeOf(err) != runtimeext.ConnectorActionSideEffectOutboxErrorCode {
			t.Fatalf("side-effect grant=%+v error=%v", grant, err)
		}
	}
	writeGrant := runtimeext.ActionConnectorCapability{
		ConnectorKey: "email", ConnectionKey: "primary", OperationKey: "send",
		ContractSHA256: intent.ContractSHA256, Mode: runtimeext.ConnectorModeEnqueue, Effect: runtimeext.ConnectorEffectWrite,
	}
	execution.connectorGrants = []runtimeext.ActionConnectorCapability{writeGrant}
	if _, err := execution.StageDurableIntent(t.Context(), intent); apperror.CodeOf(err) != "backend.action.durable_intent_validator_required" {
		t.Fatalf("missing validator error=%v", err)
	}
	execution.connectorGrants = []runtimeext.ActionConnectorCapability{{ConnectorKey: "other"}}
	if _, ok := execution.durableIntentGrant(intent); ok {
		t.Fatal("mismatched durable intent grant matched")
	}

	execution.plans = []transactionmodel.MutationPlan{actionMutationEdgePlan(t, "create", "booking", "booking-1")}
	intent.IdempotencyKey = "intent-dedup"
	intent.IntentID = "intent-1"
	execution.intents = []runtimeext.DurableIntent{intent}
	commits, err := execution.canonicalCommits()
	if err != nil || len(commits) != 1 || len(commits[0].Outbox) != 1 || commits[0].Outbox[0].DedupKey != "intent-dedup" {
		t.Fatalf("commits=%+v error=%v", commits, err)
	}
	intent.IdempotencyKey = ""
	intent.Payload = map[string]any{"batch_key": "batch-1", "entry_key": "entry-1"}
	execution.intents = []runtimeext.DurableIntent{intent}
	commits, err = execution.canonicalCommits()
	if err != nil || commits[0].Outbox[0].DedupKey != "batch:7:batch-1:entry:7:entry-1" {
		t.Fatalf("journal intent commits=%+v error=%v", commits, err)
	}
}

func TestBusinessActionMutationValidationAndMissingPorts(t *testing.T) {
	newExecution := func() *businessActionExecution {
		return &businessActionExecution{
			action:     definitionmodel.ActionSchema{Key: "booking.change", EffectSet: &definitionmodel.ActionEffectSet{Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "booking"}}}},
			invocation: actionmodel.ActionInvocation{Principal: principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}},
			unitOfWork: newActionTestUnitOfWork(),
		}
	}
	if _, err := newExecution().ApplyRecordMutation(t.Context(), runtimeext.RecordMutation{}); apperror.CodeOf(err) != "backend.action.mutation_invalid" {
		t.Fatalf("invalid mutation error=%v", err)
	}
	if _, err := newExecution().ApplyRecordMutation(t.Context(), runtimeext.RecordMutation{Operation: runtimeext.MutationCreate, ObjectKey: "other"}); apperror.CodeOf(err) != "backend.action.effect_authority_denied" {
		t.Fatalf("authority error=%v", err)
	}
	tests := []runtimeext.RecordMutation{
		{Operation: runtimeext.MutationCreate, ObjectKey: "booking"},
		{Operation: runtimeext.MutationUpdate, ObjectKey: "booking", RecordID: "booking-1"},
		{Operation: runtimeext.MutationDelete, ObjectKey: "booking", RecordID: "booking-1"},
		{Operation: runtimeext.MutationRestore, ObjectKey: "booking", RecordID: "booking-1"},
		{Operation: runtimeext.MutationConditionalUpdate, ObjectKey: "booking", RecordID: "booking-1", Predicates: []runtimeext.Predicate{{Field: "status", Operator: "eq", Value: "open"}}},
	}
	for _, mutation := range tests {
		if _, err := newExecution().ApplyRecordMutation(t.Context(), mutation); apperror.CodeOf(err) != "backend.internal" {
			t.Fatalf("operation=%s error=%v", mutation.Operation, err)
		}
	}
}

func TestBusinessActionPlannedRelationsAndConcurrencyEdges(t *testing.T) {
	var nilExecution *businessActionExecution
	ctx := t.Context()
	if got := nilExecution.withPlannedRelationRecords(ctx); got != ctx {
		t.Fatal("nil execution changed context")
	}
	if got := (&businessActionExecution{}).withPlannedRelationRecords(ctx); got != ctx {
		t.Fatal("empty plans changed context")
	}

	execution := &businessActionExecution{plans: []transactionmodel.MutationPlan{
		{},
		actionMutationEdgePlan(t, "delete", "booking", "booking-1"),
		actionMutationEdgePlan(t, "create", "booking", "booking-1"),
		actionMutationEdgePlan(t, "create", "booking", "booking-2"),
		actionMutationEdgePlan(t, "delete", "booking", "booking-1"),
	}}
	_ = execution.withPlannedRelationRecords(ctx)
	for _, commit := range []transactionmodel.RecordMutationCommit{
		{Object: definitionmodel.ObjectSchema{Key: "booking"}},
		{Record: recordmodel.Record{ID: "record-1"}},
	} {
		if _, _, valid := plannedRelationRecordIdentity(commit); valid {
			t.Fatalf("invalid planned relation identity accepted: %+v", commit)
		}
	}

	declared := runtimeext.RecordMutation{
		ObjectKey: "booking", RecordID: "booking-1",
		Predicates: []runtimeext.Predicate{{Field: "version", Operator: "eq", ErrorCode: "backend.record.conflict"}},
	}
	if nilExecution.isDeclaredConcurrentRecordChange(declared) ||
		(&businessActionExecution{}).isDeclaredConcurrentRecordChange(declared) {
		t.Fatal("missing execution boundary reported concurrent change")
	}
	notDeclared := declared
	notDeclared.Predicates[0].ErrorCode = "business.conflict"
	if (&businessActionExecution{unitOfWork: newActionTestUnitOfWork()}).isDeclaredConcurrentRecordChange(notDeclared) {
		t.Fatal("business predicate reported concurrent change")
	}
	declared.Predicates[0].ErrorCode = "backend.record.conflict"
	withUnit := &businessActionExecution{unitOfWork: newActionTestUnitOfWork()}
	withUnit.unitOfWork.claim.Execution.CreatedAt = "invalid"
	if withUnit.isDeclaredConcurrentRecordChange(declared) {
		t.Fatal("invalid execution time reported concurrent change")
	}
	withUnit.unitOfWork.claim.Execution.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if withUnit.isDeclaredConcurrentRecordChange(declared) {
		t.Fatal("missing observed record reported concurrent change")
	}
	withUnit.observedRecords = map[string]recordmodel.Record{"booking\x00booking-1": {ID: "booking-1", UpdatedAt: "invalid"}}
	if withUnit.isDeclaredConcurrentRecordChange(declared) {
		t.Fatal("invalid record time reported concurrent change")
	}
}
