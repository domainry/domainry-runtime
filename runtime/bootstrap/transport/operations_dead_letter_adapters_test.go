package transport

import (
	"context"
	"errors"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

type deadLetterDeliveryRepository struct {
	message   publicationmodel.Message
	found     bool
	err       error
	status    string
	errorText string
	delay     int
}

func (*deadLetterDeliveryRepository) ListOutbox(context.Context, string, string, string, int) ([]publicationmodel.Message, error) {
	return nil, nil
}
func (*deadLetterDeliveryRepository) InsertOutbox(context.Context, string, publicationmodel.Message) (publicationmodel.Message, error) {
	return publicationmodel.Message{}, nil
}
func (r *deadLetterDeliveryRepository) UpdateOutboxStatus(_ context.Context, _ string, _ string, status, _ string, errorText string) (publicationmodel.Message, error) {
	r.status, r.errorText = status, errorText
	if r.err != nil {
		return publicationmodel.Message{}, r.err
	}
	r.message.Status, r.message.Error = status, errorText
	return r.message, nil
}
func (r *deadLetterDeliveryRepository) ScheduleOutboxRetry(_ context.Context, _ string, _ string, delay int, errorText string) (publicationmodel.Message, error) {
	r.delay, r.errorText = delay, errorText
	if r.err != nil {
		return publicationmodel.Message{}, r.err
	}
	r.message.Status, r.message.Error, r.message.NextAttemptAt = "retrying", errorText, "later"
	return r.message, nil
}
func (r *deadLetterDeliveryRepository) GetOutbox(context.Context, string, string) (publicationmodel.Message, bool, error) {
	return r.message, r.found, r.err
}

func deadLetterPrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "operator"}}, accessfixture.Bundle{})
}

func TestPublicationHandoffDeadLetterOwnerInspectRetryResolveAndProjectionFallbacks(t *testing.T) {
	repository := &deadLetterDeliveryRepository{found: true, message: publicationmodel.Message{
		ID: "message", WorkspaceID: "workspace", ConnectorKey: "__automation__", Operation: "run", Status: "dead_letter", Error: "failed", EventID: "event", DedupKey: "dedup", AttemptCount: 2, UpdatedAt: "now",
	}}
	service := publicationhandoff.NewPublicationHandoffApplicationService(publicationhandoff.Dependencies{Repository: repository})
	owner := publicationHandoffDeadLetterOwner{service: service}
	principal := deadLetterPrincipal()

	item, err := owner.Inspect(t.Context(), " message ", principal)
	if err != nil || item.BusinessKey != "dedup" || item.EvidenceRef != "publication_handoff:message" || item.Details["connector_key"] != "__automation__" {
		t.Fatalf("item=%#v err=%v", item, err)
	}
	repository.message.RequestRef, repository.message.ResponseRef = "request", " receipt "
	item = publicationHandoffDeadLetterItem(repository.message)
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
	deadLetter schedulersdk.DeadLetter
	inspectErr error
	resolveErr error
	requeueErr error
}

func (s *schedulerDeadLetterServiceStub) DeadLetter(context.Context, string) (schedulersdk.DeadLetter, error) {
	return s.deadLetter, s.inspectErr
}
func (s *schedulerDeadLetterServiceStub) ResolveDeadLetter(context.Context, string, string) (schedulersdk.DeadLetter, error) {
	return schedulersdk.DeadLetter{}, s.resolveErr
}
func (s *schedulerDeadLetterServiceStub) RequeueDeadLetter(context.Context, string, string) (schedulersdk.Run, error) {
	return schedulersdk.Run{}, s.requeueErr
}

func TestSchedulerDeadLetterOwnerAllActionsAndFailures(t *testing.T) {
	service := &schedulerDeadLetterServiceStub{deadLetter: schedulersdk.DeadLetter{RunID: "run", DefinitionKey: "definition", Status: "dead_letter", Reason: "failed"}}
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
