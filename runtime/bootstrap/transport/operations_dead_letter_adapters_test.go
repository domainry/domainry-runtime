package transport

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type deadLetterEventRepository struct {
	event     integrationmodel.IntegrationEvent
	found     bool
	err       error
	status    string
	errorText string
	delay     int
}

func (*deadLetterEventRepository) ListEvents(context.Context, string, string, string, int) ([]integrationmodel.IntegrationEvent, error) {
	return nil, nil
}
func (r *deadLetterEventRepository) GetEvent(context.Context, string, string) (integrationmodel.IntegrationEvent, bool, error) {
	return r.event, r.found, r.err
}
func (*deadLetterEventRepository) UpsertEvent(context.Context, string, integrationmodel.IntegrationEvent) (integrationmodel.IntegrationEvent, bool, error) {
	return integrationmodel.IntegrationEvent{}, false, nil
}
func (*deadLetterEventRepository) AcceptEvent(context.Context, string, integrationmodel.IntegrationEvent, integrationmodel.IntegrationEventMappingIntent) (integrationmodel.IntegrationEvent, bool, error) {
	return integrationmodel.IntegrationEvent{}, false, nil
}
func (r *deadLetterEventRepository) UpdateEventStatus(_ context.Context, _ string, _ string, status, errorText string) (integrationmodel.IntegrationEvent, error) {
	r.status, r.errorText = status, errorText
	if r.err != nil {
		return integrationmodel.IntegrationEvent{}, r.err
	}
	r.event.Status, r.event.Error = status, errorText
	return r.event, nil
}
func (r *deadLetterEventRepository) ScheduleEventRetry(_ context.Context, _ string, _ string, delay int, errorText string) (integrationmodel.IntegrationEvent, error) {
	r.delay, r.errorText = delay, errorText
	if r.err != nil {
		return integrationmodel.IntegrationEvent{}, r.err
	}
	r.event.Status, r.event.Error, r.event.NextRetryAt = "retrying", errorText, "later"
	return r.event, nil
}
func (*deadLetterEventRepository) RecordWebhookNonce(context.Context, string, string, string, string, string) (bool, error) {
	return true, nil
}

type deadLetterDeliveryRepository struct {
	message   integrationmodel.IntegrationOutboxMessage
	found     bool
	err       error
	status    string
	errorText string
	delay     int
}

func (*deadLetterDeliveryRepository) ListInvocations(context.Context, string, string, string, string, string, int) ([]integrationmodel.IntegrationInvocation, error) {
	return nil, nil
}
func (*deadLetterDeliveryRepository) InsertInvocation(context.Context, string, integrationmodel.IntegrationInvocation) (integrationmodel.IntegrationInvocation, error) {
	return integrationmodel.IntegrationInvocation{}, nil
}
func (*deadLetterDeliveryRepository) UpdateInvocationStatus(context.Context, string, string, string, int64, string, string) (integrationmodel.IntegrationInvocation, error) {
	return integrationmodel.IntegrationInvocation{}, nil
}
func (*deadLetterDeliveryRepository) ListOutbox(context.Context, string, string, string, int) ([]integrationmodel.IntegrationOutboxMessage, error) {
	return nil, nil
}
func (*deadLetterDeliveryRepository) InsertOutbox(context.Context, string, integrationmodel.IntegrationOutboxMessage) (integrationmodel.IntegrationOutboxMessage, error) {
	return integrationmodel.IntegrationOutboxMessage{}, nil
}
func (r *deadLetterDeliveryRepository) UpdateOutboxStatus(_ context.Context, _ string, _ string, status, _ string, errorText string) (integrationmodel.IntegrationOutboxMessage, error) {
	r.status, r.errorText = status, errorText
	if r.err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, r.err
	}
	r.message.Status, r.message.Error = status, errorText
	return r.message, nil
}
func (*deadLetterDeliveryRepository) UpdateOutboxStatusByResponseRef(context.Context, string, string, string, string, string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	return integrationmodel.IntegrationOutboxMessage{}, false, nil
}
func (r *deadLetterDeliveryRepository) ScheduleOutboxRetry(_ context.Context, _ string, _ string, delay int, errorText string) (integrationmodel.IntegrationOutboxMessage, error) {
	r.delay, r.errorText = delay, errorText
	if r.err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, r.err
	}
	r.message.Status, r.message.Error, r.message.NextAttemptAt = "retrying", errorText, "later"
	return r.message, nil
}
func (r *deadLetterDeliveryRepository) GetOutbox(context.Context, string, string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	return r.message, r.found, r.err
}

func deadLetterPrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{
		integrationapplication.PermissionRetry,
		integrationapplication.PermissionInvoke,
	}})
}

func TestIntegrationEventDeadLetterOwnerInspectRetryResolveAndErrors(t *testing.T) {
	repository := &deadLetterEventRepository{found: true, event: integrationmodel.IntegrationEvent{
		ID: "event", Provider: "crm", EventType: "customer.updated", ExternalID: "external", Status: "dead_letter", Error: "failed", AttemptCount: 3, UpdatedAt: "now",
	}}
	service := integrationapplication.NewIntegrationApplicationService(integrationapplication.ApplicationDependencies{EventRepository: repository})
	owner := integrationEventDeadLetterOwner{service: service}
	principal := deadLetterPrincipal()

	item, err := owner.Inspect(t.Context(), " event ", principal)
	if err != nil || item.Owner != "integration_event" || item.BusinessKey != "crm:customer.updated" || item.CorrelationID != "external" || item.Details["attempt_count"] != 3 {
		t.Fatalf("item=%#v err=%v", item, err)
	}
	item, err = owner.Act(t.Context(), "event", operationsapplication.OperationsDeadLetterRetry, " retry reason ", "ignored", principal)
	if err != nil || item.Status != "retrying" || repository.errorText != "retry reason" {
		t.Fatalf("retry item=%#v repo=%#v err=%v", item, repository, err)
	}
	repository.event.Status, repository.event.NextRetryAt = "dead_letter", ""
	item, err = owner.Act(t.Context(), "event", operationsapplication.OperationsDeadLetterResolve, " resolved ", "ignored", principal)
	if err != nil || item.Status != "ignored" || repository.status != "ignored" || repository.errorText != "resolved" {
		t.Fatalf("resolve item=%#v repo=%#v err=%v", item, repository, err)
	}
	if _, err := owner.Act(t.Context(), "event", "invalid", "", "", principal); apperror.CodeOf(err) != "backend.operations.dead_letter_action_unsupported" {
		t.Fatalf("unsupported err=%v", err)
	}
	repository.err = errors.New("event unavailable")
	if _, err := owner.Inspect(t.Context(), "event", principal); !errors.Is(err, repository.err) {
		t.Fatalf("inspect error=%v", err)
	}
}

func TestIntegrationOutboxDeadLetterOwnerInspectRetryResolveAndProjectionFallbacks(t *testing.T) {
	repository := &deadLetterDeliveryRepository{found: true, message: integrationmodel.IntegrationOutboxMessage{
		ID: "message", WorkspaceID: "workspace", ConnectorKey: "__automation__", Operation: "run", Status: "dead_letter", Error: "failed", EventID: "event", DedupKey: "dedup", AttemptCount: 2, UpdatedAt: "now",
	}}
	service := integrationapplication.NewIntegrationApplicationService(integrationapplication.ApplicationDependencies{DeliveryRepository: repository})
	owner := integrationOutboxDeadLetterOwner{service: service}
	principal := deadLetterPrincipal()

	item, err := owner.Inspect(t.Context(), " message ", principal)
	if err != nil || item.BusinessKey != "dedup" || item.EvidenceRef != "integration_outbox:message" || item.Details["connector_key"] != "__automation__" {
		t.Fatalf("item=%#v err=%v", item, err)
	}
	repository.message.RequestRef, repository.message.ResponseRef = "request", " receipt "
	item = integrationOutboxDeadLetterItem(repository.message)
	if item.BusinessKey != "request" || item.EvidenceRef != "receipt" {
		t.Fatalf("explicit projection=%#v", item)
	}
	repository.message.RequestRef, repository.message.ResponseRef = "", ""
	item, err = owner.Act(t.Context(), "message", operationsapplication.OperationsDeadLetterRetry, " retry ", "", principal)
	if err != nil || item.Status != "retrying" || repository.errorText != "retry" {
		t.Fatalf("retry item=%#v repo=%#v err=%v", item, repository, err)
	}
	repository.message.Status, repository.message.NextAttemptAt = "dead_letter", ""
	item, err = owner.Act(t.Context(), "message", operationsapplication.OperationsDeadLetterAck, " acknowledged ", "", principal)
	if err != nil || item.Status != "cancelled" || repository.status != "cancelled" || repository.errorText != "acknowledged" {
		t.Fatalf("ack item=%#v repo=%#v err=%v", item, repository, err)
	}
	if _, err := owner.Act(t.Context(), "message", "invalid", "", "", principal); apperror.CodeOf(err) != "backend.operations.dead_letter_action_unsupported" {
		t.Fatalf("unsupported err=%v", err)
	}
	repository.err = errors.New("outbox unavailable")
	if _, err := owner.Inspect(t.Context(), "message", principal); !errors.Is(err, repository.err) {
		t.Fatalf("inspect error=%v", err)
	}
}

func TestDeadLetterAdapterHelpersAndNilRegistration(t *testing.T) {
	registerOperationsDeadLetterOwners(nil, nil, nil, nil, nil)
	service := operationsapplication.NewOperationsApplicationService(nil, nil, nil, nil)
	registerOperationsDeadLetterOwners(service, nil, nil, nil, nil)
	if valueOr(" value ", "fallback") != "value" || valueOr(" ", "fallback") != "fallback" {
		t.Fatal("value fallback mismatch")
	}
	if apperror.CodeOf(deadLetterActionUnsupported()) != "backend.operations.dead_letter_action_unsupported" {
		t.Fatal("unsupported action code mismatch")
	}
}

type recordBatchDeadLetterServiceStub struct {
	job        recordmodel.RecordBatchJob
	inspectErr error
	actionErr  error
	getErr     error
	action     string
}

func (s *recordBatchDeadLetterServiceStub) InspectBatchJobDeadLetter(context.Context, string, principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	return s.job, s.inspectErr
}
func (s *recordBatchDeadLetterServiceStub) RetryBatchJobDeadLetter(context.Context, string, principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	s.action = "retry"
	return s.job, s.actionErr
}
func (s *recordBatchDeadLetterServiceStub) ResolveBatchJobDeadLetter(context.Context, string, principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	s.action = "resolve"
	return s.job, s.actionErr
}
func (s *recordBatchDeadLetterServiceStub) GetBatchJob(context.Context, string, principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	return s.job, s.getErr
}

func TestRecordBatchDeadLetterOwnerAllActionsAndFailures(t *testing.T) {
	service := &recordBatchDeadLetterServiceStub{job: recordmodel.RecordBatchJob{ID: "job", Kind: "import", ObjectKey: "customer", Status: "dead_letter", ErrorCode: "failed", IdempotencyKey: "business", AttemptCount: 2, FencingToken: 3, UpdatedAt: "now"}}
	owner := recordBatchDeadLetterOwner{service: service}
	principal := deadLetterPrincipal()
	item, err := owner.Inspect(t.Context(), "job", principal)
	if err != nil || item.Owner != "record_batch" || item.BusinessKey != "business" || item.Details["fencing_token"] != int64(3) {
		t.Fatalf("item=%#v err=%v", item, err)
	}
	service.inspectErr = errors.New("inspect failed")
	if _, err := owner.Inspect(t.Context(), "job", principal); !errors.Is(err, service.inspectErr) {
		t.Fatalf("inspect error=%v", err)
	}
	service.inspectErr = nil
	for _, action := range []string{operationsapplication.OperationsDeadLetterRetry, operationsapplication.OperationsDeadLetterResolve, operationsapplication.OperationsDeadLetterAck} {
		item, err = owner.Act(t.Context(), "job", action, "reason", "key", principal)
		if err != nil || item.ID != "job" {
			t.Fatalf("action=%s item=%#v err=%v", action, item, err)
		}
	}
	if _, err := owner.Act(t.Context(), "job", "invalid", "", "", principal); apperror.CodeOf(err) != "backend.operations.dead_letter_action_unsupported" {
		t.Fatalf("unsupported error=%v", err)
	}
	service.actionErr = errors.New("action failed")
	if _, err := owner.Act(t.Context(), "job", operationsapplication.OperationsDeadLetterRetry, "", "", principal); !errors.Is(err, service.actionErr) {
		t.Fatalf("action error=%v", err)
	}
	service.actionErr, service.getErr = nil, errors.New("get failed")
	if _, err := owner.Act(t.Context(), "job", operationsapplication.OperationsDeadLetterResolve, "", "", principal); !errors.Is(err, service.getErr) {
		t.Fatalf("get error=%v", err)
	}
}

type workflowDeadLetterServiceStub struct {
	execution  workflowmodel.WorkflowExecution
	inspectErr error
	retryErr   error
	resolveErr error
}

func (s *workflowDeadLetterServiceStub) InspectWorkflowExecution(context.Context, string, principalmodel.Principal) (workflowmodel.WorkflowExecution, error) {
	return s.execution, s.inspectErr
}
func (s *workflowDeadLetterServiceStub) RetryWorkflowExecutionWithKey(context.Context, string, string, principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	return workflowmodel.WorkflowRunResult{}, s.retryErr
}
func (s *workflowDeadLetterServiceStub) ResolveWorkflowExecution(context.Context, string, string, principalmodel.Principal) (workflowmodel.WorkflowResolveResult, error) {
	return workflowmodel.WorkflowResolveResult{}, s.resolveErr
}

func TestWorkflowDeadLetterOwnerAllActionsAndFailures(t *testing.T) {
	service := &workflowDeadLetterServiceStub{execution: workflowmodel.WorkflowExecution{ID: "execution", WorkflowKey: "approval", Status: "dead_letter", LastError: "failed", ProcessID: "process", ObjectKey: "order", RecordID: "record", Attempt: 2, MaxAttempts: 3, UpdatedAt: "now"}}
	owner := workflowDeadLetterOwner{service: service}
	principal := deadLetterPrincipal()
	item, err := owner.Inspect(t.Context(), "execution", principal)
	if err != nil || item.BusinessKey != "order:record" || item.CorrelationID != "process" {
		t.Fatalf("item=%#v err=%v", item, err)
	}
	service.execution.ObjectKey, service.execution.RecordID = "", ""
	item, err = owner.Inspect(t.Context(), "execution", principal)
	if err != nil || item.BusinessKey != "" {
		t.Fatalf("empty business key item=%#v err=%v", item, err)
	}
	for _, action := range []string{operationsapplication.OperationsDeadLetterRetry, operationsapplication.OperationsDeadLetterResolve, operationsapplication.OperationsDeadLetterAck} {
		if _, err := owner.Act(t.Context(), "execution", action, "reason", "key", principal); err != nil {
			t.Fatalf("action=%s err=%v", action, err)
		}
	}
	if _, err := owner.Act(t.Context(), "execution", "invalid", "", "", principal); apperror.CodeOf(err) != "backend.operations.dead_letter_action_unsupported" {
		t.Fatalf("unsupported error=%v", err)
	}
	service.retryErr = errors.New("retry failed")
	if _, err := owner.Act(t.Context(), "execution", operationsapplication.OperationsDeadLetterRetry, "", "", principal); !errors.Is(err, service.retryErr) {
		t.Fatalf("retry error=%v", err)
	}
	service.retryErr, service.resolveErr = nil, errors.New("resolve failed")
	if _, err := owner.Act(t.Context(), "execution", operationsapplication.OperationsDeadLetterResolve, "", "", principal); !errors.Is(err, service.resolveErr) {
		t.Fatalf("resolve error=%v", err)
	}
	service.resolveErr, service.inspectErr = nil, errors.New("inspect failed")
	if _, err := owner.Act(t.Context(), "execution", operationsapplication.OperationsDeadLetterAck, "", "", principal); !errors.Is(err, service.inspectErr) {
		t.Fatalf("final inspect error=%v", err)
	}
}

type schedulerDeadLetterServiceStub struct {
	record     recordmodel.Record
	inspectErr error
	resolveErr error
	requeueErr error
}

func (s *schedulerDeadLetterServiceStub) InspectDeadLetter(context.Context, string, principalmodel.Principal) (recordmodel.Record, error) {
	return s.record, s.inspectErr
}
func (s *schedulerDeadLetterServiceStub) ResolveDeadLetter(context.Context, string, string, string, principalmodel.Principal) (schedulerapplication.SchedulerOperationResult, error) {
	return schedulerapplication.SchedulerOperationResult{}, s.resolveErr
}
func (s *schedulerDeadLetterServiceStub) RequeueDeadLetter(context.Context, string, string, string, principalmodel.Principal) (schedulerapplication.SchedulerOperationResult, error) {
	return schedulerapplication.SchedulerOperationResult{}, s.requeueErr
}

func TestSchedulerDeadLetterOwnerAllActionsAndFailures(t *testing.T) {
	service := &schedulerDeadLetterServiceStub{record: recordmodel.Record{ID: "dead", UpdatedAt: "now", Data: map[string]any{"status": "dead_letter", "last_error": "failed", "job_run_id": "run", "scheduler_definition_key": "definition", "reason": "provider"}}}
	owner := schedulerDeadLetterOwner{service: service}
	principal := deadLetterPrincipal()
	item, err := owner.Inspect(t.Context(), "dead", principal)
	if err != nil || item.BusinessKey != "definition" || item.CorrelationID != "run" {
		t.Fatalf("item=%#v err=%v", item, err)
	}
	for _, action := range []string{operationsapplication.OperationsDeadLetterResolve, operationsapplication.OperationsDeadLetterRetry, operationsapplication.OperationsDeadLetterAck} {
		if _, err := owner.Act(t.Context(), "dead", action, "reason", "key", principal); err != nil {
			t.Fatalf("action=%s err=%v", action, err)
		}
	}
	if _, err := owner.Act(t.Context(), "dead", "invalid", "", "", principal); apperror.CodeOf(err) != "backend.operations.dead_letter_action_unsupported" {
		t.Fatalf("unsupported error=%v", err)
	}
	service.resolveErr = errors.New("resolve failed")
	if _, err := owner.Act(t.Context(), "dead", operationsapplication.OperationsDeadLetterResolve, "", "", principal); !errors.Is(err, service.resolveErr) {
		t.Fatalf("resolve error=%v", err)
	}
	service.resolveErr, service.requeueErr = nil, errors.New("requeue failed")
	if _, err := owner.Act(t.Context(), "dead", operationsapplication.OperationsDeadLetterRetry, "", "", principal); !errors.Is(err, service.requeueErr) {
		t.Fatalf("requeue error=%v", err)
	}
	service.requeueErr, service.inspectErr = nil, errors.New("inspect failed")
	if _, err := owner.Act(t.Context(), "dead", operationsapplication.OperationsDeadLetterAck, "", "", principal); !errors.Is(err, service.inspectErr) {
		t.Fatalf("inspect error=%v", err)
	}
}
