package automation

import (
	"context"
	"errors"
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	automationvalidation "github.com/domainry/domainry-runtime/runtime/domain/automation/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestAutomationDefinitionValidationUsesEmptyAndResolvedCatalog(t *testing.T) {
	service := AutomationDefinitionValidationApplicationService{}
	if err := service.Validate(t.Context(), automationmodel.AutomationRuleSchema{}); err == nil {
		t.Fatal("invalid empty rule accepted")
	}
	service = AutomationDefinitionValidationApplicationService{
		Catalog: func() automationvalidation.AutomationDefinitionCatalog {
			return automationvalidation.AutomationDefinitionCatalog{Objects: []definitionmodel.ObjectSchema{{Key: "order"}}}
		},
		ListConnections: func(context.Context, string) ([]integrationmodel.IntegrationConnection, error) {
			return []integrationmodel.IntegrationConnection{}, nil
		},
	}
	rule := automationmodel.AutomationRuleSchema{Key: "rule", Name: "Rule", ObjectKey: "order", Trigger: automationmodel.AutomationTriggerSchema{Phase: "before", Operation: "create"}}
	if err := service.Validate(t.Context(), rule); err != nil {
		t.Fatal(err)
	}
}

func TestAutomationBeforeServiceAuthorizationDepthCancellationAndExecutionError(t *testing.T) {
	rule := enabledBeforeRule("rule", 1)
	service := NewAutomationBeforeApplicationService(BeforeServiceDependencies{Rules: beforeRuleRegistry{rules: []automationmodel.AutomationRuleSchema{rule}}, ExecuteRule: func(context.Context, automationmodel.AutomationRuleSchema, map[string]any, map[string]any, map[string]any, string, principalmodel.Principal) (automationprojection.AutomationRuleTrace, error) {
		return automationprojection.AutomationRuleTrace{RuleKey: "rule"}, errAutomationFacadeProbe
	}})
	if _, err := service.Run(t.Context(), "order", "create", "", nil, nil, map[string]any{}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization err=%v", err)
	}
	principal := automationFacadePrincipal()
	principal.AutomationDepth = 8
	if _, err := service.Run(t.Context(), "order", "create", "", nil, nil, map[string]any{}, principal); apperror.CodeOf(err) != "backend.automation.max_depth_exceeded" {
		t.Fatalf("depth err=%v", err)
	}
	principal.AutomationDepth = 0
	traces, err := service.Run(t.Context(), "order", "create", "", nil, nil, map[string]any{}, principal)
	if !errors.Is(err, errAutomationFacadeProbe) || len(traces) != 1 {
		t.Fatalf("traces=%#v err=%v", traces, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Run(ctx, "order", "create", "", nil, nil, map[string]any{}, principal); apperror.CodeOf(err) != "backend.automation.total_timeout" {
		t.Fatalf("cancel err=%v", err)
	}
	service = NewAutomationBeforeApplicationService(BeforeServiceDependencies{Rules: beforeRuleRegistry{rules: []automationmodel.AutomationRuleSchema{rule}}, ExecuteRule: func(_ context.Context, _ automationmodel.AutomationRuleSchema, _, _, candidate map[string]any, _ string, _ principalmodel.Principal) (automationprojection.AutomationRuleTrace, error) {
		candidate["status"] = "normalized"
		return automationprojection.AutomationRuleTrace{Status: "succeeded"}, nil
	}})
	candidate := map[string]any{"status": "draft"}
	if traces, err := service.Run(t.Context(), "order", "create", "", nil, nil, candidate, principal); err != nil || len(traces) != 1 || candidate["status"] != "normalized" {
		t.Fatalf("traces=%#v candidate=%v err=%v", traces, candidate, err)
	}
}

func TestAutomationInstructionExecutionClaimReplayFailureCompletionAndSimulation(t *testing.T) {
	request := afterInstructionRequest(func(context.Context) (automationmodel.AutomationInstructionResult, error) {
		return automationmodel.AutomationInstructionResult{Key: "send", Type: "emit_event", Status: "success"}, nil
	})
	if _, err := NewAutomationInstructionExecutionApplicationService(nil).Execute(t.Context(), AutomationInstructionExecutionRequest{WorkspaceID: ""}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace err=%v", err)
	}
	claimError := instructionRepositoryStub{claim: func(context.Context, automationmodel.AutomationInstructionExecution, string, string) (automationmodel.AutomationInstructionExecution, bool, error) {
		return automationmodel.AutomationInstructionExecution{}, false, errAutomationFacadeProbe
	}, complete: func(context.Context, string, string, string, int64, string, map[string]any, string) (automationmodel.AutomationInstructionExecution, error) {
		return automationmodel.AutomationInstructionExecution{}, nil
	}}
	if _, err := NewAutomationInstructionExecutionApplicationService(claimError).Execute(t.Context(), request); apperror.CodeOf(err) != "backend.internal" || !errors.Is(err, errAutomationFacadeProbe) {
		t.Fatalf("claim err=%v", err)
	}
	inProgress := instructionRepositoryStub{claim: func(context.Context, automationmodel.AutomationInstructionExecution, string, string) (automationmodel.AutomationInstructionExecution, bool, error) {
		return automationmodel.AutomationInstructionExecution{Status: "running"}, false, nil
	}, complete: func(context.Context, string, string, string, int64, string, map[string]any, string) (automationmodel.AutomationInstructionExecution, error) {
		return automationmodel.AutomationInstructionExecution{}, nil
	}}
	if _, err := NewAutomationInstructionExecutionApplicationService(inProgress).Execute(t.Context(), request); apperror.CodeOf(err) != "backend.automation.instruction_in_progress" {
		t.Fatalf("in-progress err=%v", err)
	}
	claimed := func(completeErr error, executeErr error) instructionRepositoryStub {
		return instructionRepositoryStub{
			claim: func(_ context.Context, execution automationmodel.AutomationInstructionExecution, _, _ string) (automationmodel.AutomationInstructionExecution, bool, error) {
				execution.LeaseOwner, execution.FencingToken = "worker", 2
				return execution, true, nil
			},
			complete: func(context.Context, string, string, string, int64, string, map[string]any, string) (automationmodel.AutomationInstructionExecution, error) {
				return automationmodel.AutomationInstructionExecution{}, completeErr
			},
		}
	}
	request.Execute = func(context.Context) (automationmodel.AutomationInstructionResult, error) {
		return automationmodel.AutomationInstructionResult{Key: "send", Status: "failed"}, errAutomationFacadeProbe
	}
	if result, err := NewAutomationInstructionExecutionApplicationService(claimed(nil, errAutomationFacadeProbe)).Execute(t.Context(), request); !errors.Is(err, errAutomationFacadeProbe) || result.Status != "failed" {
		t.Fatalf("execute result=%#v err=%v", result, err)
	}
	request.Execute = func(context.Context) (automationmodel.AutomationInstructionResult, error) {
		return automationmodel.AutomationInstructionResult{Key: "send", Status: "success"}, nil
	}
	if result, err := NewAutomationInstructionExecutionApplicationService(claimed(errAutomationFacadeProbe, nil)).Execute(t.Context(), request); apperror.CodeOf(err) != "backend.internal" || result.Status != "failed" {
		t.Fatalf("complete result=%#v err=%v", result, err)
	}
	simulationCalls := 0
	simulation := request
	simulation.Phase, simulation.Simulation = "before", true
	simulation.Execute = func(context.Context) (automationmodel.AutomationInstructionResult, error) {
		simulationCalls++
		return automationmodel.AutomationInstructionResult{}, nil
	}
	result, err := NewAutomationInstructionExecutionApplicationService(nil).Execute(t.Context(), simulation)
	if err != nil || result.Status != "simulated" || simulationCalls != 0 || result.Data["dry_run"] != true {
		t.Fatalf("simulation=%#v calls=%d err=%v", result, simulationCalls, err)
	}
	normal := simulation
	normal.Simulation = false
	normal.Execute = func(context.Context) (automationmodel.AutomationInstructionResult, error) {
		return automationmodel.AutomationInstructionResult{Status: "success"}, nil
	}
	if result, err := NewAutomationInstructionExecutionApplicationService(nil).Execute(t.Context(), normal); err != nil || result.Status != "success" {
		t.Fatalf("normal=%#v err=%v", result, err)
	}
	nonSideEffect := simulation
	nonSideEffect.Instruction.Type = "set_field"
	nonSideEffect.Execute = func(context.Context) (automationmodel.AutomationInstructionResult, error) {
		return automationmodel.AutomationInstructionResult{Status: "success"}, nil
	}
	if result, err := NewAutomationInstructionExecutionApplicationService(nil).Execute(t.Context(), nonSideEffect); err != nil || result.Status != "success" {
		t.Fatalf("non-side-effect simulation=%#v err=%v", result, err)
	}
	afterSimulation := simulation
	afterSimulation.Phase = "after"
	if result, err := NewAutomationInstructionExecutionApplicationService(nil).Execute(t.Context(), afterSimulation); err != nil || result.Status != "simulated" {
		t.Fatalf("after simulation=%#v err=%v", result, err)
	}
	completedStatus := ""
	successRepository := instructionRepositoryStub{
		claim: func(_ context.Context, execution automationmodel.AutomationInstructionExecution, _, _ string) (automationmodel.AutomationInstructionExecution, bool, error) {
			execution.LeaseOwner, execution.FencingToken = "worker", 3
			return execution, true, nil
		},
		complete: func(_ context.Context, _, _, _ string, _ int64, status string, _ map[string]any, _ string) (automationmodel.AutomationInstructionExecution, error) {
			completedStatus = status
			return automationmodel.AutomationInstructionExecution{}, nil
		},
	}
	success := request
	success.Record = nil
	success.Execute = func(context.Context) (automationmodel.AutomationInstructionResult, error) {
		return automationmodel.AutomationInstructionResult{Status: "success"}, nil
	}
	if result, err := NewAutomationInstructionExecutionApplicationService(successRepository).Execute(t.Context(), success); err != nil || result.Status != "success" || completedStatus != "succeeded" {
		t.Fatalf("success=%#v status=%q err=%v", result, completedStatus, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result, err := NewAutomationInstructionExecutionApplicationService(successRepository).Execute(ctx, success); apperror.CodeOf(err) != "backend.automation.rule_timeout" || result.Status != "failed" {
		t.Fatalf("cancelled=%#v err=%v", result, err)
	}
}

func TestAutomationInstructionExecutionPropagatesHeartbeatFailure(t *testing.T) {
	repository := instructionRepositoryStub{
		claim: func(_ context.Context, execution automationmodel.AutomationInstructionExecution, _, _ string) (automationmodel.AutomationInstructionExecution, bool, error) {
			execution.LeaseOwner, execution.FencingToken = "worker", 1
			return execution, true, nil
		},
		heartbeat: func(context.Context, string, string, string, int64, string) (automationmodel.AutomationInstructionExecution, error) {
			return automationmodel.AutomationInstructionExecution{}, errAutomationFacadeProbe
		},
		complete: func(context.Context, string, string, string, int64, string, map[string]any, string) (automationmodel.AutomationInstructionExecution, error) {
			return automationmodel.AutomationInstructionExecution{}, nil
		},
	}
	request := afterInstructionRequest(func(ctx context.Context) (automationmodel.AutomationInstructionResult, error) {
		<-ctx.Done()
		return automationmodel.AutomationInstructionResult{Status: "success"}, nil
	})
	if result, err := NewAutomationInstructionExecutionApplicationService(repository).Execute(t.Context(), request); !errors.Is(err, errAutomationFacadeProbe) || result.Status != "success" {
		t.Fatalf("heartbeat result=%+v err=%v", result, err)
	}
}

func TestAutomationRuleExecutionValidationConditionErrorTargetsAndAudit(t *testing.T) {
	service := NewAutomationRuleApplicationService(nil, nil, nil)
	base := RuleExecutionRequest{Rule: automationmodel.AutomationRuleSchema{Key: "rule", ObjectKey: "order", Trigger: automationmodel.AutomationTriggerSchema{Operation: "create"}}, Phase: "before", WorkspaceID: "workspace-1", Principal: automationFacadePrincipal(), Candidate: map[string]any{}}
	if _, err := service.Execute(t.Context(), RuleExecutionRequest{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization err=%v", err)
	}
	mismatch := base
	mismatch.WorkspaceID = "other"
	if _, err := service.Execute(t.Context(), mismatch); apperror.CodeOf(err) != "backend.workspace_scope_mismatch" {
		t.Fatalf("mismatch err=%v", err)
	}
	if _, err := service.Execute(nil, base); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("nil context err=%v", err)
	}
	condition := base
	condition.Rule.Conditions = automationmodel.AutomationConditionGroup{Clauses: []automationmodel.AutomationConditionClause{{Reference: "$payload.active"}}}
	condition.MatchCondition = func(automationmodel.AutomationConditionClause) bool { return false }
	if trace, err := service.Execute(WithSimulation(t.Context(), "condition"), condition); err != nil || trace.Status != "skipped" || trace.Matched {
		t.Fatalf("condition trace=%#v err=%v", trace, err)
	}
	if trace, err := service.Execute(t.Context(), condition); err != nil || trace.Status != "skipped" || trace.Matched {
		t.Fatalf("skipped trace=%#v err=%v", trace, err)
	}
	instruction := base
	instruction.Rule.Instructions = []automationmodel.AutomationInstructionSchema{{Key: "send", Type: "emit_event"}}
	instruction.MatchCondition = func(automationmodel.AutomationConditionClause) bool { return true }
	instruction.ExecuteInstruction = func(context.Context, automationmodel.AutomationInstructionSchema) (automationmodel.AutomationInstructionResult, error) {
		return automationmodel.AutomationInstructionResult{Key: "send", Status: "failed"}, errAutomationFacadeProbe
	}
	if trace, err := service.Execute(t.Context(), instruction); !errors.Is(err, errAutomationFacadeProbe) || trace.Status != "blocked" || trace.ErrorCode != "backend.internal" {
		t.Fatalf("blocked trace=%#v err=%v", trace, err)
	}
	instruction.ExecuteInstruction = func(context.Context, automationmodel.AutomationInstructionSchema) (automationmodel.AutomationInstructionResult, error) {
		return automationmodel.AutomationInstructionResult{Key: "send", Status: "success"}, nil
	}
	if trace, err := service.Execute(WithSimulation(t.Context(), "send"), instruction); err != nil || len(trace.InstructionTraces) != 1 {
		t.Fatalf("target trace=%#v err=%v", trace, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if trace, err := service.Execute(ctx, instruction); apperror.CodeOf(err) != "backend.automation.rule_timeout" || trace.Status != "blocked" {
		t.Fatalf("cancelled trace=%#v err=%v", trace, err)
	}
	audits := 0
	service = NewAutomationRuleApplicationService(nil, nil, func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
		audits++
	})
	after := base
	after.Phase = "after"
	after.Record = &recordmodel.Record{ID: "record-1", UpdatedAt: "v1"}
	if trace, err := service.Execute(t.Context(), after); err != nil || trace.NodeTraces[len(trace.NodeTraces)-1].NodeID != "outbox" || audits < 2 {
		t.Fatalf("after trace=%#v audits=%d err=%v", trace, audits, err)
	}
}
