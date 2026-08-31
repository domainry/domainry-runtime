package transport

import (
	"context"
	"fmt"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	recordtimerapplication "github.com/domainry/domainry-runtime/runtime/application/recordtimer"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

func registerOperationsDeadLetterOwners(service *operationsapplication.OperationsApplicationService, publications runtimePublicationDeadLetterService, workflows *workflowapplication.WorkflowApplicationService, scheduler schedulerDeadLetterService, recordTimers recordTimerDeadLetterService) {
	if service == nil {
		return
	}
	if publications != nil {
		_ = service.RegisterDeadLetterOwner("runtime_publication_outbox", publicationHandoffDeadLetterOwner{service: publications})
	}
	if workflows != nil {
		_ = service.RegisterDeadLetterOwner("workflow_execution", workflowDeadLetterOwner{service: workflows})
	}
	if scheduler != nil {
		_ = service.RegisterDeadLetterOwner("scheduler", schedulerDeadLetterOwner{service: scheduler})
	}
	if recordTimers != nil {
		_ = service.RegisterDeadLetterOwner("record_timer", recordTimerDeadLetterOwner{service: recordTimers})
	}
}

type recordTimerDeadLetterService interface {
	InspectFailure(context.Context, string, principalmodel.Principal) (recordmodel.Record, error)
	RetryFailure(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)
	ResolveFailure(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)
}

type recordTimerDeadLetterOwner struct{ service recordTimerDeadLetterService }

func (o recordTimerDeadLetterOwner) Inspect(ctx context.Context, id string, principal principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	record, err := o.service.InspectFailure(ctx, id, principal)
	return recordTimerDeadLetterItem(record), err
}

func (o recordTimerDeadLetterOwner) Act(ctx context.Context, id, action, reason, _ string, principal principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	var record recordmodel.Record
	var err error
	switch action {
	case operationsapplication.OperationsDeadLetterRetry:
		record, err = o.service.RetryFailure(ctx, id, reason, principal)
	case operationsapplication.OperationsDeadLetterResolve, operationsapplication.OperationsDeadLetterAck:
		record, err = o.service.ResolveFailure(ctx, id, reason, principal)
	default:
		err = deadLetterActionUnsupported()
	}
	return recordTimerDeadLetterItem(record), err
}

func recordTimerDeadLetterItem(record recordmodel.Record) operationsapplication.OperationsDeadLetterItem {
	data := record.Data
	return operationsapplication.OperationsDeadLetterItem{
		Owner: "record_timer", ID: record.ID, ResourceType: "record_timer", Status: strings.TrimSpace(valueString(data, "status")),
		FailureCode: valueString(data, "last_error"), BusinessKey: strings.Trim(strings.Join([]string{valueString(data, "object_key"), valueString(data, "record_id"), valueString(data, "purpose")}, ":"), ":"),
		EvidenceRef: "record_timer_event:" + record.ID, AllowedActions: []string{"resolve", "retry", "ack"},
		Details: map[string]any{"target_type": valueString(data, "target_type"), "target_key": valueString(data, "target_key"), "attempt": data["attempt"], "max_attempts": data["max_attempts"], "fencing_token": data["fencing_token"]}, UpdatedAt: record.UpdatedAt,
	}
}

func valueString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(data[key]))
}

var _ recordTimerDeadLetterService = (*recordtimerapplication.RecordTimerApplicationService)(nil)

type publicationHandoffDeadLetterOwner struct {
	service runtimePublicationDeadLetterService
}

type runtimePublicationDeadLetterService interface {
	InspectPublicationMessage(context.Context, string, principalmodel.Principal) (publicationmodel.Message, error)
	SchedulePublicationRetry(context.Context, string, publicationmodel.RetryRequest, principalmodel.Principal) (publicationmodel.Message, error)
	UpdatePublicationStatus(context.Context, string, publicationmodel.StatusRequest, principalmodel.Principal) (publicationmodel.Message, error)
}

func (o publicationHandoffDeadLetterOwner) Inspect(ctx context.Context, id string, principal principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	message, err := o.service.InspectPublicationMessage(ctx, id, principal)
	if err != nil {
		return operationsapplication.OperationsDeadLetterItem{}, err
	}
	return publicationHandoffDeadLetterItem(message), nil
}
func (o publicationHandoffDeadLetterOwner) Act(ctx context.Context, id, action, reason, _ string, principal principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	var message publicationmodel.Message
	var err error
	switch action {
	case operationsapplication.OperationsDeadLetterRetry:
		message, err = o.service.SchedulePublicationRetry(ctx, id, publicationmodel.RetryRequest{Error: reason}, principal)
	case operationsapplication.OperationsDeadLetterResolve, operationsapplication.OperationsDeadLetterAck:
		message, err = o.service.UpdatePublicationStatus(ctx, id, publicationmodel.StatusRequest{Status: "cancelled", Error: reason}, principal)
	default:
		err = deadLetterActionUnsupported()
	}
	return publicationHandoffDeadLetterItem(message), err
}
func publicationHandoffDeadLetterItem(message publicationmodel.Message) operationsapplication.OperationsDeadLetterItem {
	businessKey := message.RequestRef
	if businessKey == "" {
		businessKey = message.DedupKey
	}
	return operationsapplication.OperationsDeadLetterItem{Owner: "runtime_publication_outbox", ID: message.ID, ResourceType: "runtime_publication_outbox", Status: message.Status, FailureCode: message.Error, CorrelationID: message.EventID, BusinessKey: businessKey, EvidenceRef: valueOr(message.ResponseRef, "publication_handoff:"+message.ID), AllowedActions: []string{"resolve", "retry", "ack"}, Details: map[string]any{"connector_key": message.ConnectorKey, "operation": message.Operation, "attempt_count": message.AttemptCount}, UpdatedAt: message.UpdatedAt}
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
	DeadLetter(context.Context, string) (schedulersdk.DeadLetter, error)
	ResolveDeadLetter(context.Context, string, string) (schedulersdk.DeadLetter, error)
	RequeueDeadLetter(context.Context, string, string) (schedulersdk.Run, error)
}

func (o schedulerDeadLetterOwner) Inspect(ctx context.Context, id string, principal principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	record, err := o.service.DeadLetter(ctx, id)
	if err != nil {
		return operationsapplication.OperationsDeadLetterItem{}, err
	}
	return operationsapplication.OperationsDeadLetterItem{Owner: "scheduler", ID: record.RunID, ResourceType: "scheduler_dead_letter", Status: record.Status, FailureCode: record.Reason, CorrelationID: record.RunID, BusinessKey: record.DefinitionKey, EvidenceRef: "scheduler_dead_letter:" + record.RunID, AllowedActions: []string{"resolve", "retry", "ack"}, Details: map[string]any{"reason": record.Reason}, UpdatedAt: record.FailedAt.UTC().Format(time.RFC3339Nano)}, nil
}
func (o schedulerDeadLetterOwner) Act(ctx context.Context, id, action, reason, key string, principal principalmodel.Principal) (operationsapplication.OperationsDeadLetterItem, error) {
	switch action {
	case operationsapplication.OperationsDeadLetterRetry:
		if _, err := o.service.RequeueDeadLetter(ctx, id, reason); err != nil {
			return operationsapplication.OperationsDeadLetterItem{}, err
		}
	case operationsapplication.OperationsDeadLetterResolve, operationsapplication.OperationsDeadLetterAck:
		if _, err := o.service.ResolveDeadLetter(ctx, id, reason); err != nil {
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
