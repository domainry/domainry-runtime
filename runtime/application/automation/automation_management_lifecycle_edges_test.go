package automation

import (
	"context"
	"errors"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"github.com/domainry/domainry-foundation/apperror"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type automationManagementExecutionErrorProbe struct{ err error }

func (p automationManagementExecutionErrorProbe) InsertExecution(_ context.Context, _ string, execution automationmodel.AutomationRuleExecution) (automationmodel.AutomationRuleExecution, error) {
	return execution, p.err
}

func (p automationManagementExecutionErrorProbe) ListExecutions(context.Context, string, automationmodel.AutomationExecutionFilter) ([]automationmodel.AutomationRuleExecution, error) {
	return nil, p.err
}

func TestAutomationManagementAuthorizationContextAndDependencyFailures(t *testing.T) {
	principal := automationFacadePrincipal()
	denied := principal
	denied = accessfixture.With(denied, accessfixture.Bundle{})
	registry := managementRuleRegistry{rules: []automationmodel.AutomationRuleSchema{{Key: "rule"}}}
	service := NewAutomationManagementApplicationService(AutomationManagementDependencies{Rules: registry})
	if catalog, err := service.ExecutionCatalog(t.Context(), principal); err != nil || len(catalog.Connections) != 0 || len(catalog.Connectors) != 0 {
		t.Fatalf("empty catalog=%+v err=%v", catalog, err)
	}
	if history, err := service.ExecutionHistory(t.Context(), automationmodel.AutomationExecutionFilter{}, principal); err != nil || history.Count != 0 {
		t.Fatalf("empty history=%+v err=%v", history, err)
	}
	if _, err := service.ExecutionCatalog(t.Context(), denied); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("capabilities denied err=%v", err)
	}
	if _, err := service.Rules(t.Context(), denied); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("rules denied err=%v", err)
	}
	if _, err := service.Rule(t.Context(), "rule", denied); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("rule denied err=%v", err)
	}
	if _, err := service.ExecutionHistory(t.Context(), automationmodel.AutomationExecutionFilter{}, denied); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("history denied err=%v", err)
	}
	if _, err := service.ValidateRule(t.Context(), automationmodel.AutomationRuleSchema{}, denied); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("validate denied err=%v", err)
	}
	if _, err := service.SimulateRule(t.Context(), automationmodel.AutomationRuleSchema{}, automationcontract.AutomationSimulationRequest{}, denied); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("simulate denied err=%v", err)
	}
	unknown := principalmodel.Principal{}
	if _, err := service.Rules(t.Context(), unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("rules unknown err=%v", err)
	}
	if _, err := service.Rule(t.Context(), "rule", unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("rule unknown err=%v", err)
	}
	if _, err := service.ValidateRule(t.Context(), automationmodel.AutomationRuleSchema{}, unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("validate unknown err=%v", err)
	}
	if _, err := service.SimulateRule(t.Context(), automationmodel.AutomationRuleSchema{}, automationcontract.AutomationSimulationRequest{}, unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("simulate unknown err=%v", err)
	}
	knownWithoutWorkspace := principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}
	if err := automationAuthorizeCommand(knownWithoutWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("known missing workspace err=%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Rules(ctx, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("rules context err=%v", err)
	}
	if _, err := service.Rule(ctx, "rule", principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("rule context err=%v", err)
	}
	if _, err := service.Rule(t.Context(), "missing", principal); apperror.CodeOf(err) != "backend.automation.not_found" {
		t.Fatalf("missing rule err=%v", err)
	}

	service = NewAutomationManagementApplicationService(AutomationManagementDependencies{Rules: registry, ListConnections: func(context.Context, string) ([]integrationsdk.Connection, error) {
		return nil, errAutomationFacadeProbe
	}})
	if _, err := service.ExecutionCatalog(t.Context(), principal); !errors.Is(err, errAutomationFacadeProbe) {
		t.Fatalf("connections err=%v", err)
	}
	for _, test := range []struct {
		name string
		deps AutomationManagementDependencies
	}{
		{"executions", AutomationManagementDependencies{Rules: registry, Executions: automationManagementExecutionErrorProbe{err: errAutomationFacadeProbe}}},
		{"invocations", AutomationManagementDependencies{Rules: registry, ListInvocations: func(context.Context, string, automationmodel.AutomationExecutionFilter) ([]integrationsdk.Invocation, error) {
			return nil, errAutomationFacadeProbe
		}}},
		{"outbox", AutomationManagementDependencies{Rules: registry, ListOutbox: func(context.Context, string) ([]publicationmodel.Message, error) {
			return nil, errAutomationFacadeProbe
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewAutomationManagementApplicationService(test.deps).ExecutionHistory(t.Context(), automationmodel.AutomationExecutionFilter{}, principal); apperror.CodeOf(err) != "backend.automation.history_failed" || !errors.Is(err, errAutomationFacadeProbe) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestAutomationManagementValidationAndSimulationFailureOutcomes(t *testing.T) {
	principal := automationFacadePrincipal()
	rule := automationmodel.AutomationRuleSchema{Key: "rule", Trigger: automationmodel.AutomationTriggerSchema{Phase: "after"}}
	service := NewAutomationManagementApplicationService(AutomationManagementDependencies{
		Rules: managementRuleRegistry{rules: []automationmodel.AutomationRuleSchema{rule}},
		ValidateDefinition: func(context.Context, automationmodel.AutomationRuleSchema) error {
			return errAutomationFacadeProbe
		},
	})
	if result, err := service.ValidateRule(t.Context(), rule, principal); !errors.Is(err, errAutomationFacadeProbe) || result.Valid {
		t.Fatalf("validation=%#v err=%v", result, err)
	}
	if _, err := service.SimulateRule(t.Context(), rule, automationcontract.AutomationSimulationRequest{}, principal); !errors.Is(err, errAutomationFacadeProbe) {
		t.Fatalf("simulate validation err=%v", err)
	}
	service = NewAutomationManagementApplicationService(AutomationManagementDependencies{
		Rules:              managementRuleRegistry{rules: []automationmodel.AutomationRuleSchema{rule}},
		ValidateDefinition: func(context.Context, automationmodel.AutomationRuleSchema) error { return nil },
		ExecuteRule: func(context.Context, automationmodel.AutomationRuleSchema, string, map[string]any, map[string]any, map[string]any, *recordmodel.Record, principalmodel.Principal) (automationprojection.AutomationRuleTrace, error) {
			return automationprojection.AutomationRuleTrace{Status: "blocked", NodeTraces: []automationprojection.AutomationNodeTrace{{NodeID: "custom", Status: "failed"}}}, errAutomationFacadeProbe
		},
	})
	result, err := service.SimulateRule(t.Context(), rule, automationcontract.AutomationSimulationRequest{TargetNodeID: " custom ", Input: map[string]any{"value": true}}, principal)
	if !errors.Is(err, errAutomationFacadeProbe) || result.TestedNodeID != "custom" || result.NodePassed || result.WouldSave {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if automationWorkspaceID(principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: " workspace "}}) != "workspace" {
		t.Fatal("workspace not trimmed")
	}
	validUnknown := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-1"}}
	if apperror.CodeOf(automationAuthorizeQuery(validUnknown)) != "backend.workspace_scope_required" || apperror.CodeOf(automationAuthorizeCommand(validUnknown)) != "backend.workspace_scope_required" {
		t.Fatal("unknown principal accepted")
	}
}

type automationReplayRecordProbe struct {
	page    recordmodel.RecordPageResult
	err     error
	observe func(recordmodel.RecordListQuery)
}

func (p automationReplayRecordProbe) ListRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	if p.observe != nil {
		p.observe(query)
	}
	return p.page, p.err
}

func TestAutomationBeforeCreateReplayCoversIncompleteMissingAmbiguousAndAccess(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "order"}
	principal := automationFacadePrincipal()
	resolveScope := func(principalmodel.Principal, definitionmodel.ObjectSchema, string) (*recordmodel.RecordScopeExpression, error) {
		return nil, nil
	}
	rule := automationmodel.AutomationRuleSchema{Key: "rule", ObjectKey: "order", Enabled: true, Trigger: automationmodel.AutomationTriggerSchema{Phase: "before", Operation: "create"}, Execution: automationmodel.AutomationExecutionPolicy{IdempotencyKeys: []string{"external_id"}}}
	if record, found, err := AutomationFindBeforeCreateReplay(t.Context(), []automationmodel.AutomationRuleSchema{{Key: "no-keys", ObjectKey: "order", Enabled: true, Trigger: rule.Trigger}, rule}, automationReplayRecordProbe{}, object, map[string]any{}, principal, nil, nil); err != nil || found || record.ID != "" {
		t.Fatalf("incomplete record=%#v found=%v err=%v", record, found, err)
	}
	input := map[string]any{"external_id": "A"}
	if _, _, err := AutomationFindBeforeCreateReplay(t.Context(), []automationmodel.AutomationRuleSchema{rule}, automationReplayRecordProbe{}, object, input, principal, nil, nil); apperror.CodeOf(err) != "backend.automation.record_scope_unavailable" {
		t.Fatalf("missing scope resolver err=%v", err)
	}
	compiledScope := &recordmodel.RecordScopeExpression{Operator: "eq", FieldKey: "owner_user_id", Values: []string{"user"}}
	var scopedQuery recordmodel.RecordListQuery
	if _, _, err := AutomationFindBeforeCreateReplay(t.Context(), []automationmodel.AutomationRuleSchema{rule}, automationReplayRecordProbe{observe: func(query recordmodel.RecordListQuery) { scopedQuery = query }}, object, input, principal, func(principalmodel.Principal, definitionmodel.ObjectSchema, string) (*recordmodel.RecordScopeExpression, error) {
		return compiledScope, nil
	}, nil); err != nil {
		t.Fatalf("scoped replay lookup: %v", err)
	}
	if scopedQuery.AuthorizationMode != recordmodel.RecordQueryAuthorizationPredicate || scopedQuery.RootObjectKey != object.Key || scopedQuery.ScopeExpression != compiledScope {
		t.Fatalf("scoped replay query=%#v", scopedQuery)
	}
	if _, _, err := AutomationFindBeforeCreateReplay(t.Context(), []automationmodel.AutomationRuleSchema{rule}, automationReplayRecordProbe{err: errAutomationFacadeProbe}, object, input, principal, resolveScope, nil); apperror.CodeOf(err) != "backend.internal" || !errors.Is(err, errAutomationFacadeProbe) {
		t.Fatalf("list err=%v", err)
	}
	if _, found, err := AutomationFindBeforeCreateReplay(t.Context(), []automationmodel.AutomationRuleSchema{rule}, automationReplayRecordProbe{}, object, input, principal, resolveScope, nil); err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}
	page := recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "a"}, {ID: "b"}}}
	if _, _, err := AutomationFindBeforeCreateReplay(t.Context(), []automationmodel.AutomationRuleSchema{rule}, automationReplayRecordProbe{page: page}, object, input, principal, resolveScope, nil); apperror.CodeOf(err) != "backend.automation.idempotency_ambiguous" {
		t.Fatalf("ambiguous err=%v", err)
	}
	page.Items = page.Items[:1]
	if _, _, err := AutomationFindBeforeCreateReplay(t.Context(), []automationmodel.AutomationRuleSchema{rule}, automationReplayRecordProbe{page: page}, object, input, principal, resolveScope, func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return false }); apperror.CodeOf(err) != "backend.automation.idempotency_conflict" {
		t.Fatalf("access err=%v", err)
	}
	record, found, err := AutomationFindBeforeCreateReplay(t.Context(), []automationmodel.AutomationRuleSchema{rule}, automationReplayRecordProbe{page: page}, object, input, principal, resolveScope, func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true })
	if err != nil || !found || record.ID != "a" {
		t.Fatalf("record=%#v found=%v err=%v", record, found, err)
	}
	blankKeyRule := rule
	blankKeyRule.Execution.IdempotencyKeys = []string{""}
	if _, found, err := AutomationFindBeforeCreateReplay(t.Context(), []automationmodel.AutomationRuleSchema{blankKeyRule}, automationReplayRecordProbe{}, object, input, principal, nil, nil); err != nil || found {
		t.Fatalf("blank key found=%v err=%v", found, err)
	}
	if record, found, err := AutomationFindBeforeCreateReplay(t.Context(), []automationmodel.AutomationRuleSchema{rule}, automationReplayRecordProbe{page: page}, object, input, principal, resolveScope, nil); err != nil || !found || record.ID != "a" {
		t.Fatalf("nil access record=%+v found=%v err=%v", record, found, err)
	}
}

func TestAutomationInstructionDispatchWorkflowEventAndFailureEdges(t *testing.T) {
	renderErr := func(map[string]any) AutomationInstructionRenderContext {
		return AutomationInstructionRenderContext{Payload: map[string]any{}, RenderString: func(value any) string {
			if value == nil {
				return ""
			}
			return value.(string)
		}, RenderOptionalData: func(any) (map[string]any, error) { return nil, errAutomationFacadeProbe }}
	}
	dispatcher := NewAutomationInstructionDispatchApplicationService(AutomationInstructionDispatchDependencies{})
	for _, instruction := range []automationmodel.AutomationInstructionSchema{{Key: "workflow", Type: "start_workflow", Config: map[string]any{}}, {Key: "unknown", Type: "unknown"}} {
		if result, err := dispatcher.Execute(t.Context(), automationmodel.AutomationRuleSchema{}, instruction, renderErr(nil), nil, automationFacadePrincipal()); err == nil || result.Status != "failed" || result.ErrorCode == "" {
			t.Fatalf("instruction=%#v result=%#v err=%v", instruction, result, err)
		}
	}
	workflowInstruction := automationmodel.AutomationInstructionSchema{Key: "workflow", Type: "start_workflow", Config: map[string]any{"workflow_key": "flow", "input": map[string]any{"id": "1"}}}
	if _, err := dispatcher.Execute(t.Context(), automationmodel.AutomationRuleSchema{}, workflowInstruction, renderErr(nil), nil, automationFacadePrincipal()); !errors.Is(err, errAutomationFacadeProbe) {
		t.Fatalf("render err=%v", err)
	}
	dispatcher = NewAutomationInstructionDispatchApplicationService(AutomationInstructionDispatchDependencies{RunWorkflow: func(context.Context, string, map[string]any, principalmodel.Principal) (map[string]any, error) {
		return nil, errAutomationFacadeProbe
	}})
	render := renderContext(map[string]any{})
	if _, err := dispatcher.Execute(t.Context(), automationmodel.AutomationRuleSchema{}, workflowInstruction, render, nil, automationFacadePrincipal()); !errors.Is(err, errAutomationFacadeProbe) {
		t.Fatalf("workflow err=%v", err)
	}
	dispatcher = NewAutomationInstructionDispatchApplicationService(AutomationInstructionDispatchDependencies{RunWorkflow: func(_ context.Context, key string, payload map[string]any, _ principalmodel.Principal) (map[string]any, error) {
		return map[string]any{"key": key, "id": payload["id"]}, nil
	}, EmitEvent: func(context.Context, automationmodel.AutomationRuleSchema, automationmodel.AutomationInstructionSchema) (map[string]any, error) {
		return map[string]any{"event": true}, nil
	}})
	if result, err := dispatcher.Execute(t.Context(), automationmodel.AutomationRuleSchema{}, workflowInstruction, render, nil, automationFacadePrincipal()); err != nil || result.Data["key"] != "flow" {
		t.Fatalf("workflow result=%#v err=%v", result, err)
	}
	if result, err := dispatcher.Execute(t.Context(), automationmodel.AutomationRuleSchema{}, automationmodel.AutomationInstructionSchema{Key: "event", Type: "emit_event"}, render, nil, automationFacadePrincipal()); err != nil || result.Data["event"] != true {
		t.Fatalf("event result=%#v err=%v", result, err)
	}
	dispatcher = NewAutomationInstructionDispatchApplicationService(AutomationInstructionDispatchDependencies{EmitEvent: func(context.Context, automationmodel.AutomationRuleSchema, automationmodel.AutomationInstructionSchema) (map[string]any, error) {
		return nil, errAutomationFacadeProbe
	}})
	if _, err := dispatcher.Execute(t.Context(), automationmodel.AutomationRuleSchema{}, automationmodel.AutomationInstructionSchema{Key: "event", Type: "emit_event"}, render, nil, automationFacadePrincipal()); !errors.Is(err, errAutomationFacadeProbe) {
		t.Fatalf("event err=%v", err)
	}
	if mapped := automationInstructionMap("not-map"); len(mapped) != 0 {
		t.Fatalf("mapped=%v", mapped)
	}
	if result, err := failedInstruction(automationmodel.AutomationInstructionResult{Key: "x"}, errors.New("plain")); err == nil || result.ErrorCode != "backend.internal" || result.Status != "failed" {
		t.Fatalf("failed result=%#v err=%v", result, err)
	}
}
