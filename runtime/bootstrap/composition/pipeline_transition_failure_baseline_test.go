package composition

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	apperror "github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type pipelineFailureRepository struct {
	recordrepository.RecordRepository
	records         map[string]map[string]recordmodel.Record
	commitCount     int
	failCommitAt    int
	failAudit       bool
	auditAttempts   int
	workflowIntents []workflowmodel.WorkflowExecution
}

func (r *pipelineFailureRepository) GetRecord(_ context.Context, _ string, object definitionmodel.ObjectSchema, recordID string) (recordmodel.Record, bool, error) {
	record, ok := r.records[object.Key][recordID]
	return cloneRecord(record), ok, nil
}

func (r *pipelineFailureRepository) ListRecords(_ context.Context, _ string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	items := []recordmodel.Record{}
	for _, record := range r.records[object.Key] {
		if pipelineID, filtered := query.Filters["pipeline"]; filtered && record.Data["pipeline"] != pipelineID {
			continue
		}
		items = append(items, cloneRecord(record))
	}
	return recordmodel.RecordPageResult{Items: items, Total: len(items)}, nil
}

func (r *pipelineFailureRepository) UniqueExists(_ context.Context, _, _, _, _ string, _ any) (bool, error) {
	return false, nil
}

func (r *pipelineFailureRepository) CommitRecordMutation(_ context.Context, _ string, commit transactionmodel.RecordMutationCommit) error {
	r.commitCount++
	if r.commitCount == r.failCommitAt {
		return errors.New("injected mutation failure")
	}
	if r.records[commit.Object.Key] == nil {
		r.records[commit.Object.Key] = map[string]recordmodel.Record{}
	}
	r.records[commit.Object.Key][commit.Record.ID] = cloneRecord(commit.Record)
	return nil
}

func (r *pipelineFailureRepository) CommitRecordMutationBatch(_ context.Context, _ string, commits []transactionmodel.RecordMutationCommit) error {
	snapshot := map[string]map[string]recordmodel.Record{}
	for objectKey, records := range r.records {
		snapshot[objectKey] = map[string]recordmodel.Record{}
		for id, record := range records {
			snapshot[objectKey][id] = cloneRecord(record)
		}
	}
	startingCommits := r.commitCount
	startingAudits := r.auditAttempts
	startingIntents := len(r.workflowIntents)
	for _, commit := range commits {
		r.commitCount++
		if r.commitCount == r.failCommitAt {
			r.records = snapshot
			r.commitCount = startingCommits
			r.auditAttempts = startingAudits
			r.workflowIntents = r.workflowIntents[:startingIntents]
			return errors.New("injected mutation failure")
		}
		if r.records[commit.Object.Key] == nil {
			r.records[commit.Object.Key] = map[string]recordmodel.Record{}
		}
		r.records[commit.Object.Key][commit.Record.ID] = cloneRecord(commit.Record)
		audits := len(commit.Audits)
		if commit.Audit != nil {
			audits++
		}
		for range audits {
			r.auditAttempts++
			if r.failAudit {
				r.records = snapshot
				r.commitCount = startingCommits
				r.auditAttempts = startingAudits
				r.workflowIntents = r.workflowIntents[:startingIntents]
				return errors.New("injected audit failure")
			}
		}
		r.workflowIntents = append(r.workflowIntents, commit.WorkflowIntents...)
	}
	return nil
}

func (r *pipelineFailureRepository) InsertAuditEvent(auditmodel.AuditEvent) error {
	r.auditAttempts++
	if r.failAudit {
		return errors.New("injected audit failure")
	}
	return nil
}

func TestPipelineTransitionFailureWindowsBaseline(t *testing.T) {
	t.Run("record failure leaves no transition state", func(t *testing.T) {
		service, repo, object, record, principal := pipelineFailureFixture(t, 1, false, false)
		_, err := newPipelineTransitionApplicationService(service).Persist(t.Context(), object, record, recordvalidation.RecordCloneData(record.Data), pipelineAction(), nil, "stage_old", "stage_new", principal)
		if err == nil {
			t.Fatal("expected injected record failure")
		}
		assertPipelineState(t, repo, "stage_old", 0)
	})

	t.Run("history failure rolls back transitioned record", func(t *testing.T) {
		service, repo, object, record, principal := pipelineFailureFixture(t, 2, false, false)
		_, err := newPipelineTransitionApplicationService(service).Persist(t.Context(), object, record, recordvalidation.RecordCloneData(record.Data), pipelineAction(), nil, "stage_old", "stage_new", principal)
		if err == nil {
			t.Fatal("expected injected history failure")
		}
		assertPipelineState(t, repo, "stage_old", 0)
	})

	t.Run("mandatory audit failure rolls back record and history", func(t *testing.T) {
		service, repo, object, record, principal := pipelineFailureFixture(t, 0, true, false)
		if _, err := newPipelineTransitionApplicationService(service).Persist(t.Context(), object, record, recordvalidation.RecordCloneData(record.Data), pipelineAction(), nil, "stage_old", "stage_new", principal); err == nil {
			t.Fatal("expected mandatory audit failure")
		}
		assertPipelineState(t, repo, "stage_old", 0)
		if repo.auditAttempts != 0 {
			t.Fatalf("expected audit rollback, got %d persisted attempt(s)", repo.auditAttempts)
		}
	})

	t.Run("invalid workflow trigger is rejected before commit", func(t *testing.T) {
		service, repo, object, record, principal := pipelineFailureFixture(t, 0, false, true)
		_, err := newPipelineTransitionApplicationService(service).Persist(t.Context(), object, record, recordvalidation.RecordCloneData(record.Data), pipelineAction(), nil, "stage_old", "stage_new", principal)
		if err == nil || apperror.CodeOf(err) != "backend.workflow.graph_v2_required" {
			t.Fatalf("expected injected workflow contract failure, got %v", err)
		}
		assertPipelineState(t, repo, "stage_old", 0)
		if repo.auditAttempts != 0 {
			t.Fatalf("expected no audit before workflow validation, got %d attempt(s)", repo.auditAttempts)
		}
	})

	t.Run("workflow trigger intent commits with transition", func(t *testing.T) {
		service, repo, object, record, principal := pipelineFailureFixture(t, 0, false, false)
		service.workflows["pipeline_after_action"] = definitionmodel.WorkflowSchema{Key: "pipeline_after_action", Name: "Pipeline after action", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "action_completed", ObjectKey: "pipeline_item", Event: "pipeline_item.advance"}, Action: map[string]any{"type": "workflow_graph"}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}}}}
		result, err := newPipelineTransitionApplicationService(service).Persist(t.Context(), object, record, recordvalidation.RecordCloneData(record.Data), pipelineAction(), nil, "stage_old", "stage_new", principal)
		if err != nil {
			t.Fatalf("persist pipeline transition: %v", err)
		}
		assertPipelineState(t, repo, "stage_new", 1)
		if len(repo.workflowIntents) != 1 || repo.workflowIntents[0].Status != "pending" {
			t.Fatalf("expected one pending transactional workflow intent, got %#v", repo.workflowIntents)
		}
		if len(result.TriggeredWorkflows) != 1 || result.TriggeredWorkflows[0].Status != "pending" {
			t.Fatalf("expected pending workflow summary, got %#v", result.TriggeredWorkflows)
		}
	})
}

func pipelineFailureFixture(t *testing.T, failCommitAt int, failAudit, failWorkflow bool) (*runtimeAssembly, *pipelineFailureRepository, definitionmodel.ObjectSchema, recordmodel.Record, principalmodel.Principal) {
	t.Helper()
	objects := []definitionmodel.ObjectSchema{
		{Key: "pipeline_item", Name: "Pipeline Item", Fields: []definitionmodel.FieldSchema{
			{Key: "pipeline", Name: "Pipeline", Type: "text"},
			{Key: "current_stage", Name: "Current Stage", Type: "text", Required: true},
			{Key: "version", Name: "Version", Type: "number", Required: true},
			{Key: "status", Name: "Status", Type: "text", Required: true},
			{Key: "last_transition_at", Name: "Last Transition At", Type: "datetime"},
			{Key: "due_at", Name: "Due At", Type: "datetime"},
			{Key: "completed_at", Name: "Completed At", Type: "datetime"},
			{Key: "failure_reason", Name: "Failure Reason", Type: "text"},
		}},
		{Key: "pipeline_stage", Name: "Pipeline Stage", Fields: []definitionmodel.FieldSchema{
			{Key: "pipeline", Name: "Pipeline", Type: "text"},
			{Key: "sla_hours", Name: "SLA Hours", Type: "number"},
		}},
		{Key: "pipeline_item_history", Name: "Pipeline History", Fields: []definitionmodel.FieldSchema{
			{Key: "pipeline_item", Name: "Pipeline Item", Type: "text", Required: true},
			{Key: "from_stage", Name: "From Stage", Type: "text"},
			{Key: "to_stage", Name: "To Stage", Type: "text", Required: true},
			{Key: "actor", Name: "Actor", Type: "text"},
			{Key: "owner", Name: "Owner", Type: "text"},
			{Key: "note", Name: "Note", Type: "text"},
			{Key: "reason", Name: "Reason", Type: "text"},
			{Key: "field_changes", Name: "Field Changes", Type: "text"},
			{Key: "transitioned_at", Name: "Transitioned At", Type: "datetime", Required: true},
		}},
	}
	record := recordmodel.Record{ID: "item_1", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", Data: map[string]any{"pipeline": "pipeline_1", "current_stage": "stage_old", "version": 1.0, "status": "open"}}
	repo := &pipelineFailureRepository{records: map[string]map[string]recordmodel.Record{
		"pipeline_item": {record.ID: cloneRecord(record)},
		"pipeline_stage": {
			"stage_old": {ID: "stage_old", Data: map[string]any{"pipeline": "pipeline_1"}},
			"stage_new": {ID: "stage_new", Data: map[string]any{"pipeline": "pipeline_1"}},
		},
	}, failCommitAt: failCommitAt, failAudit: failAudit}
	workflows := []definitionmodel.WorkflowSchema{}
	if failWorkflow {
		workflows = append(workflows, definitionmodel.WorkflowSchema{Key: "pipeline_after_action", Name: "Pipeline after action", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "action_completed", ObjectKey: "pipeline_item", Event: "pipeline_item.advance"}})
	}
	role := accessfixture.Bundle{
		Key: "pipeline-operator",
		Permissions: []string{
			"pipeline_item.advance", "pipeline_item.read", "pipeline_item.update",
			"pipeline_stage.read", "pipeline_item_history.create",
		},
		DataPolicies: []accessfixture.DataPolicyFixture{
			{ObjectKey: "pipeline_item", Scope: "all_records", Read: true, Write: true},
			{ObjectKey: "pipeline_stage", Scope: "all_records", Read: true},
			{ObjectKey: "pipeline_item_history", Scope: "all_records", Write: true},
		},
	}
	service := newRuntimeServicesAssembly(t.Context(), RuntimeServicesConfig{
		Manifest:     manifestmodel.ManifestSchema{TemplateID: "pipeline-failure", Version: "1", Name: "Pipeline Failure", Objects: objects, Actions: []definitionmodel.ActionSchema{pipelineAction()}, Workflows: workflows},
		Dependencies: RuntimeServicesDependencies{Records: repo, ActionExecutions: &runtimeServicesActionExecutionRepository{records: repo}},
	})
	return service, repo, objects[0], cloneRecord(record), accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "tester", WorkspaceID: "workspace-primary"}}, role)
}

func pipelineAction() definitionmodel.ActionSchema {
	return definitionmodel.ActionSchema{Key: "pipeline_item.advance", ObjectKey: "pipeline_item", Kind: definitionmodel.ActionKindRecordOperation, AuditEvent: "pipeline_item_transitioned"}
}

func assertPipelineState(t *testing.T, repo *pipelineFailureRepository, stage string, historyCount int) {
	t.Helper()
	item := repo.records["pipeline_item"]["item_1"]
	if item.Data["current_stage"] != stage {
		t.Fatalf("expected pipeline stage %q, got %#v", stage, item.Data)
	}
	if got := len(repo.records["pipeline_item_history"]); got != historyCount {
		t.Fatalf("expected %d history record(s), got %d", historyCount, got)
	}
}

func cloneRecord(record recordmodel.Record) recordmodel.Record {
	record.Data = recordvalidation.RecordCloneData(record.Data)
	return record
}
