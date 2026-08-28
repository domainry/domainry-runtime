package pipeline

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func pipelineTransitionStageRepository(t *testing.T, service *PipelineTransitionApplicationService) *pipelineTransitionRepositoryProbe {
	t.Helper()
	repository, ok := service.dependencies.Pipeline.dependencies.Repository.(*pipelineTransitionRepositoryProbe)
	if !ok {
		t.Fatal("unexpected pipeline repository")
	}
	return repository
}

func TestPipelineTransitionOptionalPortsAndAdvanceFailures(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-1"}}
	action := definitionmodel.ActionSchema{Key: "pipeline_item.advance", ObjectKey: "pipeline_item"}

	service, object, record := pipelineExecuteFixture(t)
	service.dependencies.ActionAllowed = nil
	service.dependencies.CanAccess = nil
	service.dependencies.CheckPrecondition = func(context.Context, definitionmodel.ActionSchema, recordmodel.Record, principalmodel.Principal) error {
		return nil
	}
	if _, err := service.Execute(t.Context(), object.Key, record.ID, action, map[string]any{"to_stage": "stage-new"}, principal); err != nil {
		t.Fatal(err)
	}
	service, object, record = pipelineExecuteFixture(t)
	if _, err := service.advance(t.Context(), object, record, record.Data, action, map[string]any{"to_stage": "stage-new"}, principal); err != nil {
		t.Fatalf("direct advance success=%v", err)
	}

	service, object, record = pipelineExecuteFixture(t)
	audited := false
	service.dependencies.Audit = func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
		audited = true
	}
	service.dependencies.ActionAllowed = func(principalmodel.Principal, definitionmodel.ActionSchema) bool { return false }
	_, _ = service.Execute(t.Context(), object.Key, record.ID, action, nil, principal)
	if !audited {
		t.Fatal("denial audit was not delegated")
	}

	service, object, record = pipelineExecuteFixture(t)
	if _, err := service.advance(t.Context(), object, record, record.Data, action, map[string]any{"to_stage": "missing"}, principal); err == nil {
		t.Fatal("missing destination accepted")
	}
	service, object, record = pipelineExecuteFixture(t)
	delete(pipelineTransitionStageRepository(t, service).records["pipeline_stage"], "stage-new")
	if _, err := service.advance(t.Context(), object, record, record.Data, action, nil, principal); err == nil {
		t.Fatal("empty next stage accepted")
	}
	service, object, record = pipelineExecuteFixture(t)
	repository := pipelineTransitionStageRepository(t, service)
	stage := repository.records["pipeline_stage"]["stage-new"]
	stage.Data["advance_permission"] = "pipeline.advance"
	repository.records["pipeline_stage"]["stage-new"] = stage
	if _, err := service.advance(t.Context(), object, record, record.Data, action, map[string]any{"to_stage": "stage-new"}, principal); err == nil {
		t.Fatal("stage permission ignored")
	}
	service, object, record = pipelineExecuteFixture(t)
	repository = pipelineTransitionStageRepository(t, service)
	stage = repository.records["pipeline_stage"]["stage-new"]
	stage.Data["required_fields"] = "required_name"
	repository.records["pipeline_stage"]["stage-new"] = stage
	if _, err := service.advance(t.Context(), object, record, record.Data, action, map[string]any{"to_stage": "stage-new"}, principal); err == nil {
		t.Fatal("required stage field ignored")
	}
}

func TestPipelineTransitionDestinationAndReopenFailureConditions(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-1", UserID: "user-1"}}
	action := definitionmodel.ActionSchema{Key: "pipeline_item.reopen"}

	service, object, record := pipelineExecuteFixture(t)
	repository := pipelineTransitionStageRepository(t, service)
	repository.listErr = errors.New("stage list failure")
	if _, err := service.reopen(t.Context(), object, record, record.Data, action, nil, principal); err == nil {
		t.Fatal("first-stage error ignored")
	}
	service, object, record = pipelineExecuteFixture(t)
	repository = pipelineTransitionStageRepository(t, service)
	repository.records["pipeline_stage"] = map[string]recordmodel.Record{}
	if _, err := service.reopen(t.Context(), object, record, record.Data, action, nil, principal); err == nil {
		t.Fatal("empty first stage accepted")
	}
	service, object, record = pipelineExecuteFixture(t)
	record.Data["current_stage"] = ""
	if _, err := service.reopen(t.Context(), object, record, record.Data, action, map[string]any{"to_stage": "stage-new"}, principal); err != nil {
		t.Fatal(err)
	}
	service, object, record = pipelineExecuteFixture(t)
	repository = pipelineTransitionStageRepository(t, service)
	stage := repository.records["pipeline_stage"]["stage-new"]
	stage.Data["pipeline"] = "other"
	repository.records["pipeline_stage"]["stage-new"] = stage
	if _, err := service.reopen(t.Context(), object, record, record.Data, action, map[string]any{"to_stage": "stage-new"}, principal); err == nil {
		t.Fatal("reopen pipeline mismatch accepted")
	}

	service, _, _ = pipelineExecuteFixture(t)
	if _, err := service.destinationStage(t.Context(), principal.WorkspaceID, "", "", map[string]any{"to_stage": "stage-new"}); err != nil {
		t.Fatal(err)
	}
	service, _, _ = pipelineExecuteFixture(t)
	delete(pipelineTransitionStageRepository(t, service).records["pipeline_stage"], "stage-old")
	if _, err := service.destinationStage(t.Context(), principal.WorkspaceID, "", "stage-old", map[string]any{"to_stage": "stage-new"}); err == nil {
		t.Fatal("missing current stage ignored")
	}
	service, _, _ = pipelineExecuteFixture(t)
	repository = pipelineTransitionStageRepository(t, service)
	stage = repository.records["pipeline_stage"]["stage-new"]
	stage.Data["pipeline"] = "other"
	repository.records["pipeline_stage"]["stage-new"] = stage
	if _, err := service.destinationStage(t.Context(), principal.WorkspaceID, "pipeline-1", "stage-old", map[string]any{"to_stage": "stage-new"}); err == nil {
		t.Fatal("destination pipeline mismatch accepted")
	}
	service, _, _ = pipelineExecuteFixture(t)
	if stage, err := service.destinationStage(t.Context(), principal.WorkspaceID, "pipeline-1", "stage-old", nil); err != nil || stage.ID != "stage-new" {
		t.Fatalf("explicit pipeline next stage=%#v err=%v", stage, err)
	}
}

func TestPipelineTransitionPersistFailureAndWorkflowConditions(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-1", UserID: "user-1"}}
	action := definitionmodel.ActionSchema{Key: "pipeline_item.advance"}
	wantErr := errors.New("transition failure")

	for _, missing := range []string{"update", "create", "commit"} {
		service, object, record, _ := pipelineTransitionFixture(t, nil)
		switch missing {
		case "update":
			service.dependencies.PlanUpdate = nil
		case "create":
			service.dependencies.PlanCreate = nil
		case "commit":
			service.dependencies.CommitPlans = nil
		}
		if _, err := service.Persist(t.Context(), object, record, record.Data, action, nil, "stage-old", "stage-new", principal); err == nil {
			t.Fatalf("missing %s dependency accepted", missing)
		}
	}

	service, object, record, _ := pipelineTransitionFixture(t, nil)
	delete(pipelineTransitionStageRepository(t, service).records["pipeline_stage"], "stage-new")
	if _, err := service.Persist(t.Context(), object, record, record.Data, action, nil, "stage-old", "stage-new", principal); err == nil {
		t.Fatal("missing persisted destination accepted")
	}
	service, object, record, _ = pipelineTransitionFixture(t, nil)
	record.Data["version"] = "invalid"
	if _, err := service.Persist(t.Context(), object, record, record.Data, action, map[string]any{"status": "closed"}, "stage-old", "stage-new", principal); err != nil {
		t.Fatal(err)
	}

	service, object, record, _ = pipelineTransitionFixture(t, nil)
	service.dependencies.PlanUpdate = func(context.Context, string, string, map[string]any, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, wantErr
	}
	if _, err := service.Persist(t.Context(), object, record, record.Data, action, nil, "stage-old", "stage-new", principal); !errors.Is(err, wantErr) {
		t.Fatalf("update plan error=%v", err)
	}
	service, object, record, _ = pipelineTransitionFixture(t, nil)
	service.dependencies.PlanCreate = func(context.Context, string, map[string]any, string, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, wantErr
	}
	if _, err := service.Persist(t.Context(), object, record, record.Data, action, nil, "stage-old", "stage-new", principal); !errors.Is(err, wantErr) {
		t.Fatalf("create plan error=%v", err)
	}
	service, object, record, _ = pipelineTransitionFixture(t, wantErr)
	if _, err := service.Persist(t.Context(), object, record, record.Data, action, nil, "stage-old", "stage-new", principal); err == nil {
		t.Fatal("non-conflict commit error ignored")
	}

	service, object, record, _ = pipelineTransitionFixture(t, nil)
	executed := 0
	service.dependencies.ExecuteWorkflows = func(_ context.Context, values []workflowmodel.WorkflowExecution, _ principalmodel.Principal) {
		executed += len(values)
	}
	if _, err := service.Persist(t.Context(), object, record, record.Data, action, nil, "stage-old", "stage-new", principal); err != nil || executed != 1 {
		t.Fatalf("executed=%d err=%v", executed, err)
	}

	service, object, record, _ = pipelineTransitionFixture(t, nil)
	originalUpdate := service.dependencies.PlanUpdate
	service.dependencies.PlanUpdate = func(ctx context.Context, objectKey, recordID string, patch map[string]any, current principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
		plan, planned, err := originalUpdate(ctx, objectKey, recordID, patch, current)
		if err != nil {
			return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
		}
		commit := plan.CanonicalCommit()
		commit.WorkflowIntents = nil
		plan, err = transactionmodel.NewMutationPlan(plan.Context(), commit, plan.Before())
		return plan, planned, err
	}
	service.dependencies.ExecuteWorkflows = func(context.Context, []workflowmodel.WorkflowExecution, principalmodel.Principal) {
		t.Fatal("empty workflows executed")
	}
	if _, err := service.Persist(t.Context(), object, record, record.Data, action, nil, "stage-old", "stage-new", principal); err != nil {
		t.Fatal(err)
	}
}
