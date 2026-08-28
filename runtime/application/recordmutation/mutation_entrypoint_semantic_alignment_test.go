package recordmutation

import (
	"context"
	"reflect"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestMutationEntrypointSourcesPreserveCanonicalCommitSemantics(t *testing.T) {
	planner := NewMutationPlannerApplicationService(func(context.Context, principalmodel.Principal) (string, error) { return "revision-1", nil })
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-1", UserID: "user-1"}, RequestID: "request-1"}
	object := definitionmodel.ObjectSchema{Key: "customer", Config: map[string]any{"write_policy": "direct_crud"}}
	audit := auditmodel.AuditEvent{ID: "audit-1", Event: "record_updated", Before: map[string]any{"status": "draft"}, After: map[string]any{"status": "active"}}
	base := transactionmodel.RecordMutationCommit{
		Object: object, Record: recordmodel.Record{ID: "customer-1", Data: map[string]any{"status": "active"}}, Audit: &audit,
		Outbox:          []integrationmodel.IntegrationOutboxMessage{{ID: "outbox-1", Payload: map[string]any{"status": "active"}}},
		WorkflowIntents: []workflowmodel.WorkflowExecution{{ID: "workflow-1", WorkflowKey: "customer.after_update", Status: "pending"}},
	}
	before := map[string]any{"status": "draft"}
	sources := []MutationInvocation{
		{Source: transactionmodel.MutationSourceHTTP},
		{Source: transactionmodel.MutationSourceAction, ActionKey: "customer.activate"},
		{Source: transactionmodel.MutationSourceWorkflow, WorkflowKey: "customer.lifecycle"},
		{Source: transactionmodel.MutationSourceAutomation, AutomationKey: "customer.updated"},
	}
	for _, operation := range []string{"create", "update", "delete", "restore"} {
		commit := base
		commit.Operation = operation
		var canonical transactionmodel.RecordMutationCommit
		var writeSet []transactionmodel.MutationWriteReference
		for index, invocation := range sources {
			plan, err := planner.Plan(WithMutationInvocation(t.Context(), invocation), principal, commit, before)
			if err != nil {
				t.Fatalf("operation=%s source=%s err=%v", operation, invocation.Source, err)
			}
			if plan.Context().Source() != invocation.Source {
				t.Fatalf("operation=%s source metadata=%s", operation, plan.Context().Source())
			}
			if index == 0 {
				canonical, writeSet = plan.CanonicalCommit(), plan.WriteSet()
				continue
			}
			if !reflect.DeepEqual(plan.CanonicalCommit(), canonical) || !reflect.DeepEqual(plan.WriteSet(), writeSet) {
				t.Fatalf("operation=%s source=%s changed canonical semantics", operation, invocation.Source)
			}
		}
	}
}
