package transport

import (
	"context"
	"fmt"
	"strings"

	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func registerOperationsDeadLetterOwners(service *operationsapplication.OperationsApplicationService, integrations *integrationapplication.IntegrationApplicationService, workflows *workflowapplication.WorkflowApplicationService, scheduler *schedulerapplication.SchedulerApplicationService, records *recordapplication.RecordApplicationService) {
	if service == nil {
		return
	}
	if integrations != nil {
		_ = service.RegisterDeadLetterOwner("integration_event", integrationEventDeadLetterOwner{service: integrations})
		_ = service.RegisterDeadLetterOwner("integration_outbox", integrationOutboxDeadLetterOwner{service: integrations})
	}
	if workflows != nil {
		_ = service.RegisterDeadLetterOwner("workflow_execution", workflowDeadLetterOwner{service: workflows})
	}
	if scheduler != nil {
		_ = service.RegisterDeadLetterOwner("scheduler", schedulerDeadLetterOwner{service: scheduler})
	}
	if records != nil {
		_ = service.RegisterDeadLetterOwner("record_batch", recordBatchDeadLetterOwner{service: records})
	}
}

type recordBatchDeadLetterOwner struct {
	service recordBatchDeadLetterService
}

type recordBatchDeadLetterService interface {
	InspectBatchJobDeadLetter(context.Context, string, principalmodel.Principal) (recordmodel.RecordBatchJob, error)
	RetryBatchJobDeadLetter(context.Context, string, principalmodel.Principal) (recordmodel.RecordBatchJob, error)
	ResolveBatchJobDeadLetter(context.Context, string, principalmodel.Principal) (recordmodel.RecordBatchJob, error)
	GetBatchJob(context.Context, string, principalmodel.Principal) (recordmodel.RecordBatchJob, error)
}

func (o recordBatchDeadLetterOwner) Inspect(ctx context.Context, id string, principal principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	job, err := o.service.InspectBatchJobDeadLetter(ctx, id, principal)
	if err != nil {
		return operationsapplication.OperationsDeadLetterItem{}, err
	}
	actions := []string{"resolve", "retry", "ack"}
	return operationsapplication.OperationsDeadLetterItem{Owner: "record_batch", ID: job.ID, ResourceType: "record_batch_job", Status: job.Status, FailureCode: job.ErrorCode, BusinessKey: job.IdempotencyKey, EvidenceRef: "record_batch_job:" + job.ID, AllowedActions: actions, Details: map[string]any{"kind": job.Kind, "object_key": job.ObjectKey, "attempt_count": job.AttemptCount, "fencing_token": job.FencingToken}, UpdatedAt: job.UpdatedAt}, nil
}

func (o recordBatchDeadLetterOwner) Act(ctx context.Context, id, action, _ string, _ string, principal principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	var err error
	switch action {
	case operationsapplication.OperationsDeadLetterRetry:
		_, err = o.service.RetryBatchJobDeadLetter(ctx, id, principal)
	case operationsapplication.OperationsDeadLetterResolve, operationsapplication.OperationsDeadLetterAck:
		_, err = o.service.ResolveBatchJobDeadLetter(ctx, id, principal)
	default:
		err = deadLetterActionUnsupported()
	}
	if err != nil {
		return operationsapplication.OperationsDeadLetterItem{}, err
	}
	job, getErr := o.service.GetBatchJob(ctx, id, principal)
	if getErr != nil {
		return operationsapplication.OperationsDeadLetterItem{}, getErr
	}
	return operationsapplication.OperationsDeadLetterItem{Owner: "record_batch", ID: job.ID, ResourceType: "record_batch_job", Status: job.Status, FailureCode: job.ErrorCode, EvidenceRef: "record_batch_job:" + job.ID, AllowedActions: []string{"resolve", "retry", "ack"}, Details: map[string]any{"kind": job.Kind, "object_key": job.ObjectKey, "attempt_count": job.AttemptCount, "fencing_token": job.FencingToken}, UpdatedAt: job.UpdatedAt}, nil
}

type integrationEventDeadLetterOwner struct {
	service *integrationapplication.IntegrationApplicationService
}

func (o integrationEventDeadLetterOwner) Inspect(ctx context.Context, id string, principal principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	event, err := o.service.InspectIntegrationEvent(ctx, id, principal)
	if err != nil {
		return operationsapplication.OperationsDeadLetterItem{}, err
	}
	return integrationEventDeadLetterItem(event), nil
}
func (o integrationEventDeadLetterOwner) Act(ctx context.Context, id, action, reason, _ string, principal principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	var event integrationmodel.IntegrationEvent
	var err error
	switch action {
	case operationsapplication.OperationsDeadLetterRetry:
		event, err = o.service.ScheduleIntegrationEventRetry(ctx, id, integrationmodel.IntegrationEventRetryRequest{Error: reason}, principal)
	case operationsapplication.OperationsDeadLetterResolve, operationsapplication.OperationsDeadLetterAck:
		event, err = o.service.UpdateIntegrationEventStatus(ctx, id, integrationmodel.IntegrationEventStatusRequest{Status: "ignored", Error: reason}, principal)
	default:
		err = deadLetterActionUnsupported()
	}
	return integrationEventDeadLetterItem(event), err
}
func integrationEventDeadLetterItem(event integrationmodel.IntegrationEvent) operationsapplication.OperationsDeadLetterItem {
	return operationsapplication.OperationsDeadLetterItem{Owner: "integration_event", ID: event.ID, ResourceType: "integration_event", Status: event.Status, FailureCode: event.Error, CorrelationID: event.ExternalID, BusinessKey: event.Provider + ":" + event.EventType, EvidenceRef: "integration_event:" + event.ID, AllowedActions: []string{"resolve", "retry", "ack"}, Details: map[string]any{"provider": event.Provider, "event_type": event.EventType, "attempt_count": event.AttemptCount}, UpdatedAt: event.UpdatedAt}
}

type integrationOutboxDeadLetterOwner struct {
	service *integrationapplication.IntegrationApplicationService
}

func (o integrationOutboxDeadLetterOwner) Inspect(ctx context.Context, id string, principal principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	message, err := o.service.InspectIntegrationOutboxMessage(ctx, id, principal)
	if err != nil {
		return operationsapplication.OperationsDeadLetterItem{}, err
	}
	return integrationOutboxDeadLetterItem(message), nil
}
func (o integrationOutboxDeadLetterOwner) Act(ctx context.Context, id, action, reason, _ string, principal principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	var message integrationmodel.IntegrationOutboxMessage
	var err error
	switch action {
	case operationsapplication.OperationsDeadLetterRetry:
		message, err = o.service.ScheduleIntegrationOutboxRetry(ctx, id, integrationmodel.IntegrationOutboxRetryRequest{Error: reason}, principal)
	case operationsapplication.OperationsDeadLetterResolve, operationsapplication.OperationsDeadLetterAck:
		message, err = o.service.UpdateIntegrationOutboxStatus(ctx, id, integrationmodel.IntegrationOutboxStatusRequest{Status: "cancelled", Error: reason}, principal)
	default:
		err = deadLetterActionUnsupported()
	}
	return integrationOutboxDeadLetterItem(message), err
}
func integrationOutboxDeadLetterItem(message integrationmodel.IntegrationOutboxMessage) operationsapplication.OperationsDeadLetterItem {
	businessKey := message.RequestRef
	if businessKey == "" {
		businessKey = message.DedupKey
	}
	return operationsapplication.OperationsDeadLetterItem{Owner: "integration_outbox", ID: message.ID, ResourceType: "integration_outbox", Status: message.Status, FailureCode: message.Error, CorrelationID: message.EventID, BusinessKey: businessKey, EvidenceRef: valueOr(message.ResponseRef, "integration_outbox:"+message.ID), AllowedActions: []string{"resolve", "retry", "ack"}, Details: map[string]any{"connector_key": message.ConnectorKey, "operation": message.Operation, "attempt_count": message.AttemptCount}, UpdatedAt: message.UpdatedAt}
}

type workflowDeadLetterOwner struct {
	service workflowDeadLetterService
}

type workflowDeadLetterService interface {
	InspectWorkflowExecution(context.Context, string, principalmodel.Principal) (workflowmodel.WorkflowExecution, error)
	RetryWorkflowExecutionWithKey(context.Context, string, string, principalmodel.Principal) (workflowmodel.WorkflowRunResult, error)
	ResolveWorkflowExecution(context.Context, string, string, principalmodel.Principal) (workflowmodel.WorkflowResolveResult, error)
}

func (o workflowDeadLetterOwner) Inspect(ctx context.Context, id string, principal principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	execution, err := o.service.InspectWorkflowExecution(ctx, id, principal)
	if err != nil {
		return operationsapplication.OperationsDeadLetterItem{}, err
	}
	return operationsapplication.OperationsDeadLetterItem{Owner: "workflow_execution", ID: execution.ID, ResourceType: "workflow_execution", Status: execution.Status, FailureCode: execution.LastError, CorrelationID: execution.ProcessID, BusinessKey: strings.Trim(strings.Join([]string{execution.ObjectKey, execution.RecordID}, ":"), ":"), EvidenceRef: "workflow_execution:" + execution.ID, AllowedActions: []string{"resolve", "retry", "ack"}, Details: map[string]any{"workflow_key": execution.WorkflowKey, "attempt": execution.Attempt, "max_attempts": execution.MaxAttempts}, UpdatedAt: execution.UpdatedAt}, nil
}
func (o workflowDeadLetterOwner) Act(ctx context.Context, id, action, reason, key string, principal principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	switch action {
	case operationsapplication.OperationsDeadLetterRetry:
		if _, err := o.service.RetryWorkflowExecutionWithKey(ctx, id, key, principal); err != nil {
			return operationsapplication.OperationsDeadLetterItem{}, err
		}
	case operationsapplication.OperationsDeadLetterResolve, operationsapplication.OperationsDeadLetterAck:
		if _, err := o.service.ResolveWorkflowExecution(ctx, id, reason, principal); err != nil {
			return operationsapplication.OperationsDeadLetterItem{}, err
		}
	default:
		return operationsapplication.OperationsDeadLetterItem{}, deadLetterActionUnsupported()
	}
	return o.Inspect(ctx, id, principal)
}

type schedulerDeadLetterOwner struct {
	service schedulerDeadLetterService
}

type schedulerDeadLetterService interface {
	InspectDeadLetter(context.Context, string, principalmodel.Principal) (recordmodel.Record, error)
	ResolveDeadLetter(context.Context, string, string, string, principalmodel.Principal) (schedulerapplication.SchedulerOperationResult, error)
	RequeueDeadLetter(context.Context, string, string, string, principalmodel.Principal) (schedulerapplication.SchedulerOperationResult, error)
}

func (o schedulerDeadLetterOwner) Inspect(ctx context.Context, id string, principal principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	record, err := o.service.InspectDeadLetter(ctx, id, principal)
	if err != nil {
		return operationsapplication.OperationsDeadLetterItem{}, err
	}
	return operationsapplication.OperationsDeadLetterItem{Owner: "scheduler", ID: record.ID, ResourceType: "job_dead_letter", Status: fmt.Sprint(record.Data["status"]), FailureCode: fmt.Sprint(record.Data["last_error"]), CorrelationID: fmt.Sprint(record.Data["job_run_id"]), BusinessKey: fmt.Sprint(record.Data["scheduler_definition_key"]), EvidenceRef: "job_dead_letter:" + record.ID, AllowedActions: []string{"resolve", "retry", "ack"}, Details: map[string]any{"reason": record.Data["reason"]}, UpdatedAt: record.UpdatedAt}, nil
}
func (o schedulerDeadLetterOwner) Act(ctx context.Context, id, action, reason, key string, principal principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	switch action {
	case operationsapplication.OperationsDeadLetterRetry:
		if _, err := o.service.RequeueDeadLetter(ctx, id, reason, key, principal); err != nil {
			return operationsapplication.OperationsDeadLetterItem{}, err
		}
	case operationsapplication.OperationsDeadLetterResolve, operationsapplication.OperationsDeadLetterAck:
		if _, err := o.service.ResolveDeadLetter(ctx, id, reason, key, principal); err != nil {
			return operationsapplication.OperationsDeadLetterItem{}, err
		}
	default:
		return operationsapplication.OperationsDeadLetterItem{}, deadLetterActionUnsupported()
	}
	return o.Inspect(ctx, id, principal)
}

func deadLetterActionUnsupported() error {
	return apperror.New(apperror.KindBadRequest, "backend.operations.dead_letter_action_unsupported", nil, nil)
}
func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
