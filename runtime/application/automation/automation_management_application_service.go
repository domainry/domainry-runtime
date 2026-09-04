package automation

import (
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"fmt"

	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	automationrepository "github.com/domainry/domainry-runtime/runtime/domain/automation/repository"
	automationvalidation "github.com/domainry/domainry-runtime/runtime/domain/automation/validation"
	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"

	"sort"
	"strings"

	apperror "github.com/domainry/domainry-foundation/apperror"
	capability "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

type AutomationRuleExecutor func(context.Context, automationmodel.AutomationRuleSchema, string, map[string]any, map[string]any, map[string]any, *recordmodel.Record, principalmodel.Principal) (automationprojection.AutomationRuleTrace, error)

type AutomationManagementDependencies struct {
	Rules              automationcontract.AutomationRuleRegistry
	Executions         automationrepository.AutomationExecutionRepository
	ListInvocations    func(context.Context, string, automationmodel.AutomationExecutionFilter) ([]integrationsdk.Invocation, error)
	ListOutbox         func(context.Context, string) ([]publicationmodel.Message, error)
	ListConnections    func(context.Context, string) ([]integrationsdk.Connection, error)
	Connectors         func(context.Context, principalmodel.Principal) []connectormodel.ConnectorSchema
	ValidateDefinition func(context.Context, automationmodel.AutomationRuleSchema) error
	ExecuteRule        AutomationRuleExecutor
}

// AutomationManagementApplicationService coordinates automation administration across Automation, Integration and Capability owners.
type AutomationManagementApplicationService struct {
	dependencies AutomationManagementDependencies
}

func NewAutomationManagementApplicationService(dependencies AutomationManagementDependencies) *AutomationManagementApplicationService {
	return &AutomationManagementApplicationService{dependencies: dependencies}
}

func (s *AutomationManagementApplicationService) ExecutionCatalog(ctx context.Context, principal principalmodel.Principal) (capability.CapabilityAutomationCatalog, error) {
	if err := automationAuthorizeEndpoint(principal, "GET /automation/execution-catalog"); err != nil {
		return capability.CapabilityAutomationCatalog{}, err
	}
	workspaceID := automationWorkspaceID(principal)
	connections := []integrationsdk.Connection{}
	if s.dependencies.ListConnections != nil {
		var err error
		connections, err = s.dependencies.ListConnections(ctx, workspaceID)
		if err != nil {
			return capability.CapabilityAutomationCatalog{}, err
		}
	}
	catalog := capability.RuntimeAutomationExecutionCatalog()
	if s.dependencies.Connectors != nil {
		catalog.Connectors = s.dependencies.Connectors(ctx, principal)
	}
	catalog.Connections = connections
	return catalog, nil
}

func (s *AutomationManagementApplicationService) Rules(ctx context.Context, principal principalmodel.Principal) ([]automationmodel.AutomationRuleSchema, error) {
	if err := automationAuthorizeEndpoint(principal, "GET /automation/rules"); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rules := s.dependencies.Rules.List()
	sort.Slice(rules, func(i, j int) bool { return rules[i].Key < rules[j].Key })
	return rules, nil
}

func (s *AutomationManagementApplicationService) Rule(ctx context.Context, ruleKey string, principal principalmodel.Principal) (automationmodel.AutomationRuleSchema, error) {
	if err := automationAuthorizeEndpoint(principal, "GET /automation/rules/{ruleKey}"); err != nil {
		return automationmodel.AutomationRuleSchema{}, err
	}
	if err := ctx.Err(); err != nil {
		return automationmodel.AutomationRuleSchema{}, err
	}
	rule, ok := s.dependencies.Rules.Get(strings.TrimSpace(ruleKey))
	if !ok {
		return automationmodel.AutomationRuleSchema{}, managementError(apperror.KindNotFound, "backend.automation.not_found", nil)
	}
	return rule, nil
}

func (s *AutomationManagementApplicationService) ExecutionHistory(ctx context.Context, filter automationmodel.AutomationExecutionFilter, principal principalmodel.Principal) (automationprojection.AutomationExecutionHistory, error) {
	if err := automationAuthorizeEndpoint(principal, "GET /automation/executions"); err != nil {
		return automationprojection.AutomationExecutionHistory{}, err
	}
	workspaceID := automationWorkspaceID(principal)
	items := []automationmodel.AutomationRuleExecution{}
	if s.dependencies.Executions != nil {
		var err error
		items, err = s.dependencies.Executions.ListExecutions(ctx, workspaceID, filter)
		if err != nil {
			return automationprojection.AutomationExecutionHistory{}, managementError(apperror.KindInternal, "backend.automation.history_failed", fmt.Errorf("list automation executions: %w", err))
		}
	}
	invocations := []integrationsdk.Invocation{}
	if s.dependencies.ListInvocations != nil {
		var err error
		invocations, err = s.dependencies.ListInvocations(ctx, workspaceID, filter)
		if err != nil {
			return automationprojection.AutomationExecutionHistory{}, managementError(apperror.KindInternal, "backend.automation.history_failed", fmt.Errorf("list automation connector invocations: %w", err))
		}
	}
	outbox := []publicationmodel.Message{}
	if s.dependencies.ListOutbox != nil {
		var err error
		outbox, err = s.dependencies.ListOutbox(ctx, workspaceID)
		if err != nil {
			return automationprojection.AutomationExecutionHistory{}, managementError(apperror.KindInternal, "backend.automation.history_failed", fmt.Errorf("list automation outbox metrics: %w", err))
		}
	}
	return automationprojection.AutomationExecutionHistory{Items: items, Count: len(items), Metrics: automationprojection.BuildAutomationExecutionMetrics(items, invocations, outbox)}, nil
}

func (s *AutomationManagementApplicationService) ValidateRule(ctx context.Context, rule automationmodel.AutomationRuleSchema, principal principalmodel.Principal) (automationvalidation.AutomationValidationResult, error) {
	if err := automationAuthorizeEndpoint(principal, "POST /automation/validate"); err != nil {
		return automationvalidation.AutomationValidationResult{}, err
	}
	return automationvalidation.AutomationValidateRuleForAuthoring(ctx, rule, s.dependencies.ValidateDefinition)
}

func (s *AutomationManagementApplicationService) SimulateRule(ctx context.Context, rule automationmodel.AutomationRuleSchema, request automationcontract.AutomationSimulationRequest, principal principalmodel.Principal) (automationprojection.AutomationSimulationResult, error) {
	endpoint := "POST /automation/rules/{ruleKey}/simulate"
	if request.Rule != nil {
		endpoint = "POST /automation/simulate"
	}
	if err := automationAuthorizeEndpoint(principal, endpoint); err != nil {
		return automationprojection.AutomationSimulationResult{}, err
	}
	if err := s.dependencies.ValidateDefinition(ctx, rule); err != nil {
		return automationprojection.AutomationSimulationResult{}, err
	}
	candidate := recordcontract.RecordCloneData(request.Input)
	trace, err := s.dependencies.ExecuteRule(WithSimulation(ctx, request.TargetNodeID), rule, rule.Trigger.Phase, request.Input, request.Before, candidate, nil, principal)
	testedNodeID := strings.TrimSpace(request.TargetNodeID)
	if testedNodeID == "" {
		if rule.Trigger.Phase == "before" {
			testedNodeID = "save"
		} else {
			testedNodeID = "outbox"
		}
	}
	nodePassed := false
	for _, node := range trace.NodeTraces {
		if node.NodeID == testedNodeID {
			nodePassed = node.Status == "success" || node.Status == "succeeded" || node.Status == "simulated" || node.Status == "idempotent_replay"
			break
		}
	}
	if testedNodeID == "save" && rule.Trigger.Phase == "before" {
		nodePassed = err == nil && trace.Matched
	}
	return automationprojection.AutomationSimulationResult{
		RuleKey: rule.Key, Status: trace.Status, WouldSave: err == nil && trace.Matched && testedNodeID == "save",
		TestedNodeID: testedNodeID, NodePassed: nodePassed, Candidate: candidate, Trace: trace,
	}, err
}

func automationWorkspaceID(principal principalmodel.Principal) string {
	return strings.TrimSpace(principal.WorkspaceID)
}

func automationAuthorizeQuery(principal principalmodel.Principal) error {
	if _, err := principalmodel.NewWorkspaceQueryScope(principal.WorkspaceID); !principal.Known || err != nil {
		return managementError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	return nil
}

func automationAuthorizeCommand(principal principalmodel.Principal) error {
	if _, err := principalmodel.NewWorkspaceCommandScope(principal.WorkspaceID); !principal.Known || err != nil {
		return managementError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	return nil
}

func automationAuthorizeEndpoint(principal principalmodel.Principal, endpointIdentity string) error {
	if err := automationAuthorizeQuery(principal); err != nil {
		return err
	}
	contract, found := endpointmodel.EndpointContracts[strings.TrimSpace(endpointIdentity)]
	if !found || strings.TrimSpace(contract.ActionKey) == "" || !principal.HasExactPermission(contract.ActionKey) {
		return managementError(apperror.KindForbidden, "auth.permission_denied", nil)
	}
	return nil
}

func managementError(kind apperror.ErrorKind, code string, err error) error {
	return &apperror.AppError{Kind: kind, Code: code, Err: err}
}
