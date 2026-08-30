package pipeline

import (
	"context"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/mutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type pipelineTransitionRepositoryProbe struct {
	recordrepository.RecordRepository
	records map[string]map[string]recordmodel.Record
	listErr error
}

func (r *pipelineTransitionRepositoryProbe) GetRecord(_ context.Context, _ string, object definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
	record, ok := r.records[object.Key][id]
	return record, ok, nil
}

func (r *pipelineTransitionRepositoryProbe) ListRecords(_ context.Context, _ string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	if r.listErr != nil {
		return recordmodel.RecordPageResult{}, r.listErr
	}
	items := []recordmodel.Record{}
	for _, record := range r.records[object.Key] {
		if pipeline, filtered := query.Filters["pipeline"]; filtered && record.Data["pipeline"] != pipeline {
			continue
		}
		items = append(items, record)
	}
	return recordmodel.RecordPageResult{Items: items, Total: len(items)}, nil
}

func TestPipelineTransitionPlansItemAndHistoryThroughCanonicalMutationKernel(t *testing.T) {
	service, object, record, commits := pipelineTransitionFixture(t, nil)
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-1", UserID: "user-1"}}
	result, err := service.Persist(t.Context(), object, record, record.Data, definitionmodel.ActionSchema{Key: "pipeline_item.advance"}, nil, "stage-old", "stage-new", principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(*commits) != 2 || (*commits)[0].Operation != "update" || (*commits)[0].Record.Data["current_stage"] != "stage-new" || (*commits)[1].Operation != "create" || (*commits)[1].Object.Key != "pipeline_item_history" {
		t.Fatalf("commits=%#v", *commits)
	}
	if (*commits)[0].Audit == nil || (*commits)[1].Audit == nil || len((*commits)[0].WorkflowIntents) != 1 {
		t.Fatalf("canonical facts missing: %#v", *commits)
	}
	if len(result.TriggeredWorkflows) != 1 {
		t.Fatalf("result=%#v", result)
	}
}

func TestPipelineTransitionPlanDoesNotOwnCommit(t *testing.T) {
	service, object, record, commits := pipelineTransitionFixture(t, nil)
	result, plans, err := service.PlanPersist(t.Context(), object, record, record.Data, definitionmodel.ActionSchema{Key: "pipeline_item.advance"}, nil, "stage-old", "stage-new", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-1", UserID: "user-1"}})
	if err != nil || result.RecordID != record.ID || len(plans) != 2 || len(*commits) != 0 {
		t.Fatalf("result=%+v plans=%d committed=%d error=%v", result, len(plans), len(*commits), err)
	}
}

func TestPipelineTransitionMapsCanonicalBatchConflictAndSkipsPostCommit(t *testing.T) {
	conflict := mutation.MutationConflict("pipeline_item", "item-1", mutation.MutationConflictOptimistic, nil)
	service, object, record, commits := pipelineTransitionFixture(t, conflict)
	_, err := service.Persist(t.Context(), object, record, record.Data, definitionmodel.ActionSchema{Key: "pipeline_item.advance"}, nil, "stage-old", "stage-new", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-1", UserID: "user-1"}})
	if apperror.CodeOf(err) != "backend.record.version_conflict" || len(*commits) != 0 {
		t.Fatalf("err=%v commits=%d", err, len(*commits))
	}
}

func pipelineTransitionFixture(t *testing.T, commitErr error) (*PipelineTransitionApplicationService, definitionmodel.ObjectSchema, recordmodel.Record, *[]transactionmodel.RecordMutationCommit) {
	t.Helper()
	text := func(key string) definitionmodel.FieldSchema {
		return definitionmodel.FieldSchema{Key: key, Type: "text"}
	}
	itemObject := definitionmodel.ObjectSchema{Key: "pipeline_item", Fields: []definitionmodel.FieldSchema{text("pipeline"), text("current_stage"), {Key: "version", Type: "number"}, text("status"), {Key: "last_transition_at", Type: "datetime"}, {Key: "due_at", Type: "datetime"}, {Key: "completed_at", Type: "datetime"}, text("failure_reason")}}
	historyObject := definitionmodel.ObjectSchema{Key: "pipeline_item_history", Fields: []definitionmodel.FieldSchema{text("pipeline_item"), text("from_stage"), text("to_stage"), text("actor"), text("owner"), text("note"), text("reason"), text("field_changes"), {Key: "transitioned_at", Type: "datetime"}}}
	stageObject := definitionmodel.ObjectSchema{Key: "pipeline_stage", Fields: []definitionmodel.FieldSchema{text("pipeline"), {Key: "sort_order", Type: "number"}, {Key: "sla_hours", Type: "number"}}}
	record := recordmodel.Record{ID: "item-1", UpdatedAt: "before", Data: map[string]any{"pipeline": "pipeline-1", "current_stage": "stage-old", "version": 1.0, "status": "open"}}
	repository := &pipelineTransitionRepositoryProbe{records: map[string]map[string]recordmodel.Record{"pipeline_stage": {
		"stage-old": {ID: "stage-old", Data: map[string]any{"pipeline": "pipeline-1", "sort_order": 10.0}},
		"stage-new": {ID: "stage-new", Data: map[string]any{"pipeline": "pipeline-1", "sort_order": 20.0}},
	}}}
	objects := map[string]definitionmodel.ObjectSchema{"pipeline_item": itemObject, "pipeline_item_history": historyObject, "pipeline_stage": stageObject}
	pipeline := NewPipelineApplicationService(PipelineDependencies{Repository: repository, Object: func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
		object, ok := objects[key]
		return object, ok
	}, Now: func() time.Time { return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC) }})
	commits := []transactionmodel.RecordMutationCommit{}
	newPlan := func(principal principalmodel.Principal, commit transactionmodel.RecordMutationCommit, before map[string]any) (transactionmodel.MutationPlan, error) {
		context, err := transactionmodel.NewMutationContext(transactionmodel.MutationContextInput{WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID, Source: transactionmodel.MutationSourceAction, ActionKey: "pipeline_item.advance", CorrelationID: "correlation-1", ApplicationSchemaRevision: "test"})
		if err != nil {
			return transactionmodel.MutationPlan{}, err
		}
		return transactionmodel.NewMutationPlan(context, commit, before)
	}
	service := NewPipelineTransitionApplicationService(PipelineTransitionDependencies{
		Pipeline: pipeline,
		PlanUpdate: func(_ context.Context, objectKey, recordID string, patch map[string]any, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
			planned := record
			planned.Data = clonePipelineTestData(record.Data)
			for key, value := range patch {
				planned.Data[key] = value
			}
			audit := auditmodel.AuditEvent{ID: "update-audit"}
			commit := transactionmodel.RecordMutationCommit{Operation: "update", Object: objects[objectKey], Record: planned, Audit: &audit, WorkflowIntents: []workflowmodel.WorkflowExecution{{ID: "workflow-1", WorkflowKey: "after-transition", Status: "pending"}}}
			plan, err := newPlan(principal, commit, record.Data)
			return plan, planned, err
		},
		PlanCreate: func(_ context.Context, objectKey string, data map[string]any, _ string, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
			created := recordmodel.Record{ID: "history-1", Data: clonePipelineTestData(data)}
			audit := auditmodel.AuditEvent{ID: "create-audit"}
			plan, err := newPlan(principal, transactionmodel.RecordMutationCommit{Operation: "create", Object: objects[objectKey], Record: created, Audit: &audit}, nil)
			return plan, created, err
		},
		CommitPlans: func(_ context.Context, plans []transactionmodel.MutationPlan) error {
			if commitErr != nil {
				return commitErr
			}
			for _, plan := range plans {
				commits = append(commits, plan.CanonicalCommit())
			}
			return nil
		},
		Now: func() time.Time { return time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC) },
	})
	return service, itemObject, record, &commits
}

func clonePipelineTestData(data map[string]any) map[string]any {
	result := make(map[string]any, len(data))
	for key, value := range data {
		result[key] = value
	}
	return result
}
