package automation

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"

	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"

	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	capability "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

type managementExecutionRepository struct {
	items []automationmodel.AutomationRuleExecution
}

func TestAutomationApplicationAuthorizesWorkspaceBeforeRepositoryAccess(t *testing.T) {
	calls := 0
	service := NewAutomationManagementApplicationService(AutomationManagementDependencies{
		Rules: managementRuleRegistry{},
		ListConnections: func(context.Context, string) ([]integrationsdk.Connection, error) {
			calls++
			return nil, nil
		},
		ListInvocations: func(context.Context, string, automationmodel.AutomationExecutionFilter) ([]integrationsdk.Invocation, error) {
			calls++
			return nil, nil
		},
		ListOutbox: func(context.Context, string) ([]publicationmodel.Message, error) {
			calls++
			return nil, nil
		},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{
		"runtime.automation.automation_capabilities",
		"runtime.automation.list_automation_executions",
	}})
	if _, err := service.Capabilities(t.Context(), principal); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("capabilities error=%v", err)
	}
	if _, err := service.ExecutionHistory(t.Context(), automationmodel.AutomationExecutionFilter{}, principal); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("history error=%v", err)
	}
	if _, err := NewAutomationInstructionExecutionApplicationService(nil).Execute(t.Context(), AutomationInstructionExecutionRequest{Phase: "after"}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("instruction error=%v", err)
	}
	if calls != 0 {
		t.Fatalf("automation ports called before workspace authorization: %d", calls)
	}
}

type managementRuleRegistry struct {
	rules []automationmodel.AutomationRuleSchema
}

func (r managementRuleRegistry) List() []automationmodel.AutomationRuleSchema {
	return append([]automationmodel.AutomationRuleSchema(nil), r.rules...)
}

func (r managementRuleRegistry) Get(key string) (automationmodel.AutomationRuleSchema, bool) {
	for _, rule := range r.rules {
		if rule.Key == key {
			return rule, true
		}
	}
	return automationmodel.AutomationRuleSchema{}, false
}

func (r managementRuleRegistry) Set(string, automationmodel.AutomationRuleSchema) {}

func (r managementExecutionRepository) InsertExecution(_ context.Context, _ string, value automationmodel.AutomationRuleExecution) (automationmodel.AutomationRuleExecution, error) {
	return value, nil
}
func (r managementExecutionRepository) ListExecutions(context.Context, string, automationmodel.AutomationExecutionFilter) ([]automationmodel.AutomationRuleExecution, error) {
	return append([]automationmodel.AutomationRuleExecution(nil), r.items...), nil
}

func TestManagementServiceOwnsSortedRulesCapabilitiesAndHistory(t *testing.T) {
	rules := managementRuleRegistry{rules: []automationmodel.AutomationRuleSchema{{Key: "z"}, {Key: "a"}}}
	service := NewAutomationManagementApplicationService(AutomationManagementDependencies{
		Rules: rules, Executions: managementExecutionRepository{items: []automationmodel.AutomationRuleExecution{{ID: "execution-1"}}},
		ListInvocations: func(context.Context, string, automationmodel.AutomationExecutionFilter) ([]integrationsdk.Invocation, error) {
			return []integrationsdk.Invocation{}, nil
		},
		ListOutbox: func(context.Context, string) ([]publicationmodel.Message, error) {
			return []publicationmodel.Message{}, nil
		},
		ListConnections: func(context.Context, string) ([]integrationsdk.Connection, error) {
			return []integrationsdk.Connection{{Key: "primary"}}, nil
		},
		Connectors: func(context.Context, principalmodel.Principal) []connectormodel.ConnectorSchema {
			return []connectormodel.ConnectorSchema{{Key: "crm"}}
		},
		AuthoringProjection: func() capability.CapabilityAuthoringProjection {
			return capability.CapabilityAuthoringProjection{
				Mode:            "compatibility_projection",
				Successor:       "/capabilities",
				ContractVersion: capability.RuntimeAuthoringContractVersion,
				Domains:         []string{"automation"},
			}
		},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1"}}, accessfixture.Bundle{Permissions: []string{
		"runtime.automation.list_automation_rules",
		"runtime.automation.automation_capabilities",
		"runtime.automation.list_automation_executions",
	}})
	listed, err := service.Rules(t.Context(), principal)
	if err != nil || len(listed) != 2 || listed[0].Key != "a" {
		t.Fatalf("rules=%#v err=%v", listed, err)
	}
	catalog, err := service.Capabilities(t.Context(), principal)
	if err != nil || len(catalog.Connectors) != 1 || len(catalog.Connections) != 1 || catalog.AuthoringProjection == nil {
		t.Fatalf("catalog=%#v err=%v", catalog, err)
	}
	history, err := service.ExecutionHistory(t.Context(), automationmodel.AutomationExecutionFilter{}, principal)
	if err != nil || history.Count != 1 {
		t.Fatalf("history=%#v err=%v", history, err)
	}
}

func TestManagementServiceOwnsSimulationResultProjection(t *testing.T) {
	rule := automationmodel.AutomationRuleSchema{Key: "before", Trigger: automationmodel.AutomationTriggerSchema{Phase: "before"}}
	service := NewAutomationManagementApplicationService(AutomationManagementDependencies{
		Rules:              managementRuleRegistry{rules: []automationmodel.AutomationRuleSchema{rule}},
		ValidateDefinition: func(context.Context, automationmodel.AutomationRuleSchema) error { return nil },
		ExecuteRule: func(_ context.Context, _ automationmodel.AutomationRuleSchema, _ string, _, _, candidate map[string]any, _ *recordmodel.Record, _ principalmodel.Principal) (automationprojection.AutomationRuleTrace, error) {
			candidate["normalized"] = true
			return automationprojection.AutomationRuleTrace{Status: "succeeded", Matched: true, NodeTraces: []automationprojection.AutomationNodeTrace{{NodeID: "save", Status: "success"}}}, nil
		},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1"}}, accessfixture.Bundle{Permissions: []string{"runtime.automation.simulate_rule"}})
	result, err := service.SimulateRule(t.Context(), rule, automationcontract.AutomationSimulationRequest{Input: map[string]any{"name": "test"}}, principal)
	if err != nil || !result.WouldSave || !result.NodePassed || result.Candidate["normalized"] != true {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
