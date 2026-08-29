package automation

import integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"

import actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"

import automationruntime "github.com/domainry/domainry-runtime/runtime/domain/automation/runtime"

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"fmt"

	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	automationrepository "github.com/domainry/domainry-runtime/runtime/domain/automation/repository"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	"strings"
	"time"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	apperror "github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	automationbusiness "github.com/domainry/domainry-runtime/runtime/domain/automation/service"
	automationvalidation "github.com/domainry/domainry-runtime/runtime/domain/automation/validation"
	capability "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

type AutomationConnectorCatalog interface {
	Schema() integrationmodel.IntegrationSchema
}

type AutomationWorkflowRunner interface {
	RunAutomationWorkflow(context.Context, string, map[string]any, principalmodel.Principal) (workflowmodel.WorkflowRunResult, error)
}

type AutomationMetadataDefinitionPort interface {
	ListMetadataDefinitionVersions(context.Context, string, string, principalmodel.Principal) ([]metadatamodel.MetadataDefinitionVersion, error)
}

type AutomationApplicationDependencies struct {
	Rules                 automationcontract.AutomationRuleRegistry
	Connectors            AutomationConnectorCatalog
	RecordRepository      recordrepository.RecordRepository
	WorkerStore           automationcontract.AutomationWorkerStore
	ExecutionRepository   automationrepository.AutomationExecutionRepository
	DeliveryRepository    integrationrepository.IntegrationDeliveryRepository
	ConfigRepository      integrationrepository.IntegrationConfigRepository
	Audit                 func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
	Principal             func(context.Context, string, string, string) principalmodel.Principal
	Schema                func(context.Context, principalmodel.Principal) metadatamodel.ApplicationSchemaSnapshot
	InvokeAction          func(context.Context, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error)
	Workflows             AutomationWorkflowRunner
	Metadata              AutomationMetadataDefinitionPort
	CanAccess             func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
	ValidateRule          func(context.Context, automationmodel.AutomationRuleSchema) error
	AuthoringProjection   func() capability.CapabilityAuthoringProjection
	Worker                workerplatform.Dependencies
	NotificationCompiler  func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	NotificationCommitter AutomationExecutionNotificationCommitter
}

type AutomationExecutionNotificationCommitter interface {
	CommitAutomationExecution(context.Context, automationmodel.AutomationRuleExecution) error
	CommitAutomationExecutionNotification(context.Context, automationmodel.AutomationRuleExecution, notificationmodel.NotificationEvent) error
}

// AutomationApplicationService coordinates Automation use cases with explicit
// cross-owner Runtime ports and Metadata persistence seams. Automation rules,
// history, capabilities, validation, simulation and dispatch policy remain in
// domain/automation.
type AutomationApplicationService struct {
	rules               automationcontract.AutomationRuleRegistry
	connectors          AutomationConnectorCatalog
	recordRepo          recordrepository.RecordRepository
	workerRepo          automationcontract.AutomationWorkerStore
	executionRepo       automationrepository.AutomationExecutionRepository
	deliveryRepo        integrationrepository.IntegrationDeliveryRepository
	configRepo          integrationrepository.IntegrationConfigRepository
	audit               func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
	principal           func(context.Context, string, string, string) principalmodel.Principal
	schema              func(context.Context, principalmodel.Principal) metadatamodel.ApplicationSchemaSnapshot
	invokeAction        func(context.Context, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error)
	workflows           AutomationWorkflowRunner
	metadata            AutomationMetadataDefinitionPort
	canAccess           func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
	validateRule        func(context.Context, automationmodel.AutomationRuleSchema) error
	management          *AutomationManagementApplicationService
	worker              workerplatform.Dependencies
	compileNotification func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	commitNotification  AutomationExecutionNotificationCommitter
}

func NewAutomationApplicationService(dependencies AutomationApplicationDependencies) *AutomationApplicationService {
	dependencies.Worker = workerplatform.NormalizeDependencies(dependencies.Worker)
	service := &AutomationApplicationService{
		rules: dependencies.Rules, connectors: dependencies.Connectors,
		recordRepo: dependencies.RecordRepository, workerRepo: dependencies.WorkerStore, executionRepo: dependencies.ExecutionRepository,
		deliveryRepo: dependencies.DeliveryRepository, configRepo: dependencies.ConfigRepository,
		audit: dependencies.Audit, principal: dependencies.Principal, schema: dependencies.Schema,
		invokeAction: dependencies.InvokeAction, workflows: dependencies.Workflows, metadata: dependencies.Metadata,
		canAccess: dependencies.CanAccess, validateRule: dependencies.ValidateRule,
		worker: dependencies.Worker, compileNotification: dependencies.NotificationCompiler, commitNotification: dependencies.NotificationCommitter,
	}
	service.management = NewAutomationManagementApplicationService(AutomationManagementDependencies{
		Rules: service.rules, Executions: service.executionRepo,
		ListInvocations: func(ctx context.Context, workspaceID string, filter automationmodel.AutomationExecutionFilter) ([]integrationmodel.IntegrationInvocation, error) {
			if service.deliveryRepo == nil {
				return []integrationmodel.IntegrationInvocation{}, nil
			}
			return service.deliveryRepo.ListInvocations(ctx, workspaceID, filter.ConnectorKey, "", "", "", 500)
		},
		ListOutbox: func(ctx context.Context, workspaceID string) ([]integrationmodel.IntegrationOutboxMessage, error) {
			if service.deliveryRepo == nil {
				return []integrationmodel.IntegrationOutboxMessage{}, nil
			}
			return service.deliveryRepo.ListOutbox(ctx, workspaceID, "__automation__", "", 500)
		},
		ListConnections: func(ctx context.Context, workspaceID string) ([]integrationmodel.IntegrationConnection, error) {
			if service.configRepo == nil {
				return []integrationmodel.IntegrationConnection{}, nil
			}
			return service.configRepo.ListConnections(ctx, workspaceID)
		},
		Connectors: func(ctx context.Context, principal principalmodel.Principal) []integrationmodel.ConnectorSchema {
			return service.schema(ctx, principal).Integrations.Connectors
		},
		AuthoringProjection: dependencies.AuthoringProjection,
		ValidateDefinition:  service.validateRule,
		ExecuteRule:         service.executeRule,
	})
	return service
}

func (s *AutomationApplicationService) ValidateIntegrationOutput(action automationmodel.AutomationInstructionSchema, output map[string]any) error {
	return automationbusiness.ValidateIntegrationOutput(s.connectors.Schema().Connectors, action, output)
}

func (s *AutomationApplicationService) RunBefore(ctx context.Context, objectKey, operation, recordID string, input, before, candidate map[string]any, principal principalmodel.Principal) ([]automationprojection.AutomationRuleTrace, error) {
	if err := automationAuthorizeCommand(principal); err != nil {
		return nil, err
	}
	traces, err := NewAutomationBeforeApplicationService(BeforeServiceDependencies{
		Rules: s.rules,
		ExecuteRule: func(execCtx context.Context, rule automationmodel.AutomationRuleSchema, input, before, candidate map[string]any, recordID string, principal principalmodel.Principal) (automationprojection.AutomationRuleTrace, error) {
			started := time.Now()
			trace, executeErr := executeBeforeRule(execCtx, rule, input, before, candidate, principal)
			trace.ExecutionID = fmt.Sprintf("automation_execution_%d", time.Now().UnixNano())
			trace.DurationMS = time.Since(started).Milliseconds()
			if s.executionRepo != nil {
				_, _ = s.executionRepo.InsertExecution(execCtx, automationWorkspaceID(principal), automationmodel.AutomationRuleExecution{
					ID:            trace.ExecutionID,
					WorkspaceID:   automationWorkspaceID(principal),
					RuleKey:       rule.Key,
					ObjectKey:     rule.ObjectKey,
					RecordID:      recordID,
					Phase:         "before",
					Operation:     rule.Trigger.Operation,
					Status:        trace.Status,
					ActorID:       principal.UserID,
					RoleKey:       principal.RoleKey,
					RequestID:     principal.RequestID,
					CorrelationID: valueOrDefault(principal.RequestID, trace.ExecutionID),
					DurationMS:    trace.DurationMS,
					ErrorCode:     trace.ErrorCode,
					Candidate:     recordvalidation.RecordCloneData(candidate),
					Trace:         automationbusiness.TraceMap(trace),
				})
			}
			return trace, executeErr
		},
	}).Run(ctx, objectKey, operation, recordID, input, before, candidate, principal)
	return traces, err
}

func (s *AutomationApplicationService) matchingRules(objectKey, phase, operation string, before, candidate map[string]any) []automationmodel.AutomationRuleSchema {
	return automationbusiness.MatchingRules(s.rules.List(), objectKey, phase, operation, before, candidate)
}

func (s *AutomationApplicationService) FindBeforeCreateReplay(ctx context.Context, object definitionmodel.ObjectSchema, input map[string]any, principal principalmodel.Principal) (recordmodel.Record, bool, error) {
	if err := automationAuthorizeQuery(principal); err != nil {
		return recordmodel.Record{}, false, err
	}
	return AutomationFindBeforeCreateReplay(ctx, s.rules.List(), s.recordRepo, object, input, principal, s.canAccess)
}

func (s *AutomationApplicationService) AfterOutbox(objectKey, operation string, before map[string]any, record recordmodel.Record, principal principalmodel.Principal) []integrationmodel.IntegrationOutboxMessage {
	if automationAuthorizeCommand(principal) != nil {
		return nil
	}
	return AutomationAfterOutbox(s.rules.List(), objectKey, operation, before, record, principal, automationWorkspaceID(principal))
}

func (s *AutomationApplicationService) ExecuteBeforeRule(execCtx context.Context, rule automationmodel.AutomationRuleSchema, input, before, candidate map[string]any, principal principalmodel.Principal) (automationprojection.AutomationRuleTrace, error) {
	if err := automationAuthorizeCommand(principal); err != nil {
		return automationprojection.AutomationRuleTrace{}, err
	}
	return executeBeforeRule(execCtx, rule, input, before, candidate, principal)
}

func (s *AutomationApplicationService) executeRule(execCtx context.Context, rule automationmodel.AutomationRuleSchema, phase string, input, before, candidate map[string]any, record *recordmodel.Record, principal principalmodel.Principal) (automationprojection.AutomationRuleTrace, error) {
	return s.executeRuleWithPersistence(execCtx, rule, phase, input, before, candidate, record, principal, nil)
}

func (s *AutomationApplicationService) executeRuleWithPersistence(execCtx context.Context, rule automationmodel.AutomationRuleSchema, phase string, input, before, candidate map[string]any, record *recordmodel.Record, principal principalmodel.Principal, persist func(context.Context, automationmodel.AutomationRuleExecution) error) (automationprojection.AutomationRuleTrace, error) {
	if strings.TrimSpace(phase) == "before" {
		return executeBeforeRule(execCtx, rule, input, before, candidate, principal)
	}
	actionContext := automationmodel.AutomationRenderContext{}
	return NewAutomationRuleApplicationServiceWithWorker(s.executionRepo, s.workerRepo, s.audit, s.worker).Execute(execCtx, RuleExecutionRequest{
		Rule: rule, Phase: phase, Input: input, Before: before, Candidate: candidate, Record: record,
		Principal: principal, WorkspaceID: automationWorkspaceID(principal),
		Initialize: func() {
			actionContext = automationmodel.AutomationRenderContext{
				Payload: candidate,
				Input:   recordvalidation.RecordCloneData(input),
				Before:  recordvalidation.RecordCloneData(before),
				Actor:   map[string]any{"user_id": principal.UserID, "role": principal.RoleKey},
				Event: map[string]any{
					"id":         principal.RequestID,
					"phase":      phase,
					"operation":  rule.Trigger.Operation,
					"object_key": rule.ObjectKey,
					"timestamp":  time.Now().UTC().Format(time.RFC3339),
				},
				Results: map[string]automationmodel.AutomationInstructionResult{},
			}
			if record != nil {
				actionContext.Record = recordvalidation.RecordCloneData(record.Data)
				actionContext.Record["id"] = record.ID
			}
		},
		MatchCondition: func(clause automationmodel.AutomationConditionClause) bool {
			step := map[string]any{"source": clause.Reference, "operator": clause.Operator, "value": clause.Value}
			return automationruntime.AutomationAssert(step, &actionContext) == nil
		},
		ExecuteInstruction: func(instructionCtx context.Context, instruction automationmodel.AutomationInstructionSchema) (automationmodel.AutomationInstructionResult, error) {
			return s.executeInstruction(instructionCtx, rule, instruction, &actionContext, record, principal)
		},
		StoreResult: func(alias string, result automationmodel.AutomationInstructionResult) {
			actionContext.Results[alias] = result
		},
		PersistExecution: persist,
	})
}

func (s *AutomationApplicationService) AutomationCapabilities(ctx context.Context, principal principalmodel.Principal) (capability.CapabilityAutomationCatalog, error) {
	return s.management.Capabilities(ctx, principal)
}

func (s *AutomationApplicationService) AutomationRules(ctx context.Context, principal principalmodel.Principal) ([]automationmodel.AutomationRuleSchema, error) {
	return s.management.Rules(ctx, principal)
}

func (s *AutomationApplicationService) AutomationExecutions(ctx context.Context, filter automationmodel.AutomationExecutionFilter, principal principalmodel.Principal) (automationprojection.AutomationExecutionHistory, error) {
	return s.management.ExecutionHistory(ctx, filter, principal)
}

func (s *AutomationApplicationService) AutomationRule(ctx context.Context, ruleKey string, principal principalmodel.Principal) (automationmodel.AutomationRuleSchema, error) {
	return s.management.Rule(ctx, ruleKey, principal)
}

func (s *AutomationApplicationService) ValidateAutomationRule(ctx context.Context, rule automationmodel.AutomationRuleSchema, principal principalmodel.Principal) (automationvalidation.AutomationValidationResult, error) {
	return s.management.ValidateRule(ctx, rule, principal)
}

// ValidateAutomationAuthoringFragment validates one Automation leaf payload
// without creating or persisting a complete rule.
func (s *AutomationApplicationService) ValidateAutomationAuthoringFragment(_ context.Context, capabilityKey string, fragment map[string]any, principal principalmodel.Principal) (automationvalidation.AutomationFragmentValidationResult, error) {
	if err := automationAuthorizeQuery(principal); err != nil {
		return automationvalidation.AutomationFragmentValidationResult{}, err
	}
	if !automationvalidation.AutomationHasPermission(principal, "manage") {
		return automationvalidation.AutomationFragmentValidationResult{}, managementError(apperror.KindForbidden, "auth.permission_denied", nil)
	}
	capabilityKey = strings.TrimSpace(capabilityKey)
	result := automationvalidation.AutomationFragmentValidationResult{Valid: true, CapabilityKey: capabilityKey, Fragment: fragment}
	if err := automationvalidation.AutomationValidateAuthoringFragment(capabilityKey, fragment); err != nil {
		issue := automationvalidation.AutomationValidationIssueFromError(automationmodel.AutomationRuleSchema{}, err)
		issue.CapabilityKey = capabilityKey
		result.Valid = false
		result.Errors = []automationvalidation.AutomationValidationIssue{issue}
	}
	return result, nil
}

func (s *AutomationApplicationService) AutomationRuleVersions(ctx context.Context, ruleKey string, principal principalmodel.Principal) ([]metadatamodel.MetadataDefinitionVersion, error) {
	return s.metadata.ListMetadataDefinitionVersions(ctx, "automation_rule", strings.TrimSpace(ruleKey), principal)
}

func (s *AutomationApplicationService) SimulateAutomationRule(ctx context.Context, ruleKey string, request automationcontract.AutomationSimulationRequest, principal principalmodel.Principal) (automationprojection.AutomationSimulationResult, error) {
	rule := automationmodel.AutomationRuleSchema{}
	if request.Rule != nil {
		rule = *request.Rule
	} else {
		var err error
		rule, err = s.AutomationRule(ctx, ruleKey, principal)
		if err != nil {
			return automationprojection.AutomationSimulationResult{}, err
		}
	}
	return s.management.SimulateRule(ctx, rule, request, principal)
}

func (s *AutomationApplicationService) executeInstruction(execCtx context.Context, rule automationmodel.AutomationRuleSchema, instruction automationmodel.AutomationInstructionSchema, actionContext *automationmodel.AutomationRenderContext, record *recordmodel.Record, principal principalmodel.Principal) (automationmodel.AutomationInstructionResult, error) {
	return NewAutomationInstructionDispatchApplicationService(AutomationInstructionDispatchDependencies{
		InvokeAction: s.invokeAction,
		RunWorkflow: func(ctx context.Context, workflowKey string, payload map[string]any, principal principalmodel.Principal) (map[string]any, error) {
			run, err := s.workflows.RunAutomationWorkflow(ctx, workflowKey, payload, principal)
			return AutomationWorkflowInstructionResult(workflowKey, run, err)
		},
		EmitEvent: func(ctx context.Context, rule automationmodel.AutomationRuleSchema, instruction automationmodel.AutomationInstructionSchema) (map[string]any, error) {
			return s.emitInstructionEvent(ctx, rule, instruction, actionContext, principal)
		},
	}).Execute(execCtx, rule, instruction, AutomationInstructionRenderContext{
		Payload: actionContext.Payload,
		RenderString: func(value any) string {
			return automationruntime.AutomationRenderString(value, actionContext)
		},
		RenderOptionalData: func(value any) (map[string]any, error) {
			return automationruntime.AutomationRenderOptionalData(value, actionContext)
		},
	}, record, principal)
}

func (s *AutomationApplicationService) emitInstructionEvent(ctx context.Context, rule automationmodel.AutomationRuleSchema, instruction automationmodel.AutomationInstructionSchema, render *automationmodel.AutomationRenderContext, principal principalmodel.Principal) (map[string]any, error) {
	eventType := automationruntime.AutomationRenderString(automationruntime.AutomationFirstNonNil(instruction.Config["event_type"], instruction.Config["event"]), render)
	if eventType == "" {
		eventType = strings.TrimSpace(rule.AuditEvent)
	}
	if eventType == "" {
		return nil, automationError(apperror.KindBadRequest, "backend.automation.event_type_required", nil, "instruction", instruction.Key)
	}
	recordID := automationruntime.AutomationRenderString(instruction.Config["record_id"], render)
	if recordID == "" && render != nil && render.Record != nil {
		recordID = automationruntime.AutomationRenderString("$record.id", render)
	}
	metadata, err := automationruntime.AutomationRenderOptionalData(instruction.Config["metadata"], render)
	if err != nil {
		return nil, err
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["automation_rule_key"] = rule.Key
	metadata["instruction_key"] = instruction.Key
	if s.audit != nil {
		s.audit(ctx, eventType, rule.ObjectKey, recordID, principal, "Automation emitted "+eventType, nil, nil, metadata)
	}
	return map[string]any{"event_type": eventType, "object_key": rule.ObjectKey, "record_id": recordID, "metadata": metadata}, nil
}
