package composition

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	recordtimerapplication "github.com/domainry/domainry-runtime/runtime/application/recordtimer"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func newRecordQueryPolicyService(services *runtimeAssembly) *recordservice.RecordQueryPolicyDomainService {
	views := func() []definitionmodel.ViewSchema {
		services.mu.RLock()
		defer services.mu.RUnlock()
		return append([]definitionmodel.ViewSchema(nil), services.views...)
	}
	return recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{
		Objects: func() []definitionmodel.ObjectSchema {
			return services.Schema().Objects
		},
		Reports: func() []reportmodel.ReportSchema {
			return services.Schema().Reports
		},
		Views: views,
		CandidateScopeMatches: func(ctx context.Context, workspaceID string, candidate recordmodel.Record, expression recordmodel.RecordScopeExpression) (bool, error) {
			evaluator, ok := services.recordRepo.(interface {
				CandidateScopeMatches(context.Context, string, recordmodel.Record, recordmodel.RecordScopeExpression) (bool, error)
			})
			if !ok {
				return false, fmt.Errorf("record candidate scope evaluator is unavailable")
			}
			return evaluator.CandidateScopeMatches(ctx, workspaceID, candidate, expression)
		},
	})
}

type schedulerWorkflowTimerRuntime interface {
	ResumeTimerNode(context.Context, string, string, string, principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error)
	ProcessApprovalDeadlineTimer(context.Context, string, string, string, principalmodel.Principal) error
}

type schedulerActionTimerRuntime interface {
	Invoke(context.Context, actionmodel.ActionSource, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error)
}

func executeSchedulerRecordTimer(
	ctx context.Context,
	execution schedulerapplication.RecordTimerExecution,
	principal principalmodel.Principal,
	workflows schedulerWorkflowTimerRuntime,
	actionService schedulerActionTimerRuntime,
) error {
	switch execution.TargetType {
	case "workflow":
		switch execution.TargetKey {
		case "resume_node":
			processID, _ := execution.Payload["process_id"].(string)
			nodeID, _ := execution.Payload["node_id"].(string)
			_, err := workflows.ResumeTimerNode(ctx, execution.WorkspaceID, processID, nodeID, principal)
			return err
		case "approval_deadline":
			taskID, _ := execution.Payload["task_id"].(string)
			phase, _ := execution.Payload["phase"].(string)
			return workflows.ProcessApprovalDeadlineTimer(ctx, execution.WorkspaceID, taskID, phase, principal)
		default:
			return fmt.Errorf("unsupported workflow timer target %q", execution.TargetKey)
		}
	case "action":
		_, err := actionService.Invoke(ctx, actionmodel.ActionSourceScheduler, actionmodel.ActionInvocation{ActionKey: execution.TargetKey, ObjectKey: execution.ObjectKey, RecordID: execution.RecordID, Input: execution.Payload, Principal: principal, Actor: principal, IdempotencyKey: execution.IdempotencyKey})
		return err
	default:
		return fmt.Errorf("unsupported record timer target type %q", execution.TargetType)
	}
}

func newSchedulerOperationRuntimeAdapter(s *runtimeAssembly) schedulerOperationRuntimeAdapter {
	return schedulerOperationRuntimeAdapter{
		processExecutions: func(ctx context.Context, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
			return s.workflowApplicationService.ProcessDueWorkflowExecutions(ctx, limit, principal)
		},
		processTargetedExecutions: func(ctx context.Context, targetKey string, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
			return s.workflowApplicationService.ProcessDueWorkflowExecutionsForTarget(ctx, targetKey, limit, principal)
		},
		processTargetedExecutionsForWindow: func(ctx context.Context, targetKey string, scheduledFor time.Time, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
			return s.workflowApplicationService.ProcessDueWorkflowExecutionsForScheduledWindow(ctx, targetKey, scheduledFor, limit, principal)
		},
		insertRecord: func(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record, reason string) error {
			return s.internalMutations.Insert(ctx, workspaceID, recordapplication.RecordInternalMutationSchedulerRuntime, object, record, reason)
		},
		updateRecord: func(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record, reason string) error {
			return s.internalMutations.Update(ctx, workspaceID, recordapplication.RecordInternalMutationSchedulerRuntime, object, record, reason)
		},
		executeTimer: func(ctx context.Context, execution schedulerapplication.RecordTimerExecution, _ principalmodel.Principal) error {
			principal := principalmodel.NewSystemPrincipal(
				"runtime-scheduler",
				principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "scheduler record timer dispatch"),
				"*",
			)
			principal.WorkspaceID = execution.WorkspaceID
			switch execution.TargetType {
			case "workflow":
				if s.workflowApplicationService == nil {
					return fmt.Errorf("unsupported workflow timer target %q", execution.TargetKey)
				}
			case "action":
				if s.actionService == nil {
					return fmt.Errorf("action timer runtime is not configured")
				}
			}
			return executeSchedulerRecordTimer(ctx, execution, principal, s.workflowApplicationService, s.actionService)
		},
	}
}

func activeMetadataDefinitionKeys(
	ctx context.Context,
	repository metadatarepository.MetadataRepository,
	resourceType string,
	reason string,
) ([]string, error) {
	definitions, err := repository.ListDefinitions(
		ctx,
		principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, reason),
		resourceType,
	)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		if strings.TrimSpace(definition.DisabledAt) == "" {
			keys = append(keys, strings.TrimSpace(definition.ResourceKey))
		}
	}
	return keys, nil
}

func metadataDefinitionReferenceSource(
	repository metadatarepository.MetadataRepository,
	resourceType string,
	reason string,
) func(context.Context, principalmodel.Principal) ([]string, error) {
	return func(ctx context.Context, _ principalmodel.Principal) ([]string, error) {
		return activeMetadataDefinitionKeys(ctx, repository, resourceType, reason)
	}
}

func initializeWorkflowAutomationAndGovernance(s *runtimeAssembly, deps RuntimeServicesDependencies) {
	schedulerRuntime := newSchedulerOperationRuntimeAdapter(s)
	s.schedulerService = newSchedulerApplicationService(s, schedulerRuntime, s.recordRepo, s.auditApplicationService, s.workerDependencies)
	s.recordTimerService = recordtimerapplication.NewRecordTimerApplicationService(s.schedulerService)
	s.schedulerService.UseNotificationCompiler(s.workflowNotificationCompiler)
	if s.metadataRepo != nil {
		s.schedulerService.UseDefinitionSource(schedulerMetadataDefinitionSource{repository: s.metadataRepo})
	}
	s.workflowApplicationService = assembleWorkflowApplication(s)
	if s.agentInteractiveRunService != nil && deps.AgentInteractiveRunner != nil {
		s.newAgentInteractiveExecution = func(tools *agentapplication.AgentToolGateway) *agentapplication.AgentInteractiveExecutionApplicationService {
			return agentapplication.NewAgentInteractiveExecutionApplicationService(agentapplication.AgentInteractiveExecutionDependencies{
				Runs: s.agentInteractiveRunService, Authorize: s.agentAuthorizationService, Runner: deps.AgentInteractiveRunner, Dispatch: s.agentTaskDispatchService,
				Workflows: runtimeInteractiveWorkflowStarter{records: s}, Tools: tools,
			})
		}
	}
	s.authoringCapabilities = newCapabilityAuthoringApplicationService(s)
	if s.metadataRepo != nil {
		s.authoringCapabilities.UsePreferenceReferenceSource(metadataDefinitionReferenceSource(s.metadataRepo, "preference", "discover preference references"))
		s.authoringCapabilities.UseRuleSetReferenceSource(metadataDefinitionReferenceSource(s.metadataRepo, "rule_set", "discover rule set references"))
	}
	s.businessReferences = assembleChangePlanReferenceApplication(s, businessReferenceRuntimeAdapter{records: s, workflows: s.workflowApplicationService}, s.businessEvidenceRepo, s.frontendCapabilities)
	s.applicationSchemaService = assembleApplicationSchema(s)
	s.automationApplicationService = assembleAutomationApplication(s)
}
