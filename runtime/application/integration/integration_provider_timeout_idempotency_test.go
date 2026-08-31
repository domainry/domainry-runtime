package integration

import (
	"context"
	"errors"
	"testing"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	workertestkit "github.com/domainry/domainry-foundation/worker/testkit"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	integrationruntime "github.com/domainry/domainry-runtime/runtime/domain/integration/runtime"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type providerTimeoutWorkerRepository struct {
	integrationrepository.IntegrationWorkerRepository
	message integrationmodel.IntegrationOutboxMessage
}

type successfulFaultProbeProvider struct{ calls int }

func (p *successfulFaultProbeProvider) SendIntegrationOutboxMessage(context.Context, integrationmodel.IntegrationOutboxMessage, principalmodel.Principal) (OutboxSendResult, error) {
	p.calls++
	return OutboxSendResult{Status: "sent", ResponseRef: "provider:accepted"}, nil
}

func TestAccountingJournalFaultAfterProviderSuccessQuarantinesWithoutBlindResend(t *testing.T) {
	repository := &providerTimeoutWorkerRepository{message: integrationmodel.IntegrationOutboxMessage{
		ID: "message-fault", WorkspaceID: "workspace-primary", ConnectorKey: "accounting", ConnectionKey: "freee-primary", Operation: "create_journal_entry",
		Status: "queued", RequestRef: "journal:42", DedupKey: "batch:5:batch:entry:5:entry", Payload: map[string]any{"batch_key": "batch", "entry_key": "entry"},
	}}
	provider := &successfulFaultProbeProvider{}
	faults := workertestkit.NewScriptedFaultInjector(workertestkit.ConnectorUncertainSuccess())
	service := NewIntegrationApplicationService(ApplicationDependencies{WorkerRepository: repository, Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{}), Worker: workerplatform.Dependencies{Faults: faults}})
	service.RegisterIntegrationOutboxSender("accounting", provider)
	result, err := service.ProcessDueIntegrationOutbox(t.Context(), 1, integrationruntime.IntegrationWorkerPrincipal("workspace-primary"))
	if err != nil || result.ReconciliationRequired != 1 || repository.message.Status != "quarantined" || provider.calls != 1 {
		t.Fatalf("result=%#v message=%#v calls=%d err=%v", result, repository.message, provider.calls, err)
	}
	second, err := service.ProcessDueIntegrationOutbox(t.Context(), 1, integrationruntime.IntegrationWorkerPrincipal("workspace-primary"))
	if err != nil || provider.calls != 1 || second.Sent != 0 {
		t.Fatalf("second=%#v calls=%d err=%v", second, provider.calls, err)
	}
}

func TestIntegrationConnectorFaultsBeforeSendRetryWithoutProviderSideEffect(t *testing.T) {
	for _, effect := range []workertestkit.FaultEffect{workertestkit.ConnectorTimeout(), workertestkit.Connector429(), workertestkit.Connector5xx()} {
		repository := &providerTimeoutWorkerRepository{message: integrationmodel.IntegrationOutboxMessage{ID: "message-retry", WorkspaceID: "workspace-primary", ConnectorKey: "payment", Operation: "charge", Status: "queued", RequestRef: "charge:retry", Payload: map[string]any{}}}
		provider := &successfulFaultProbeProvider{}
		service := NewIntegrationApplicationService(ApplicationDependencies{WorkerRepository: repository, Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{}), Worker: workerplatform.Dependencies{Faults: workertestkit.NewScriptedFaultInjector(effect)}})
		service.RegisterIntegrationOutboxSender("payment", provider)
		result, err := service.ProcessDueIntegrationOutbox(t.Context(), 1, integrationruntime.IntegrationWorkerPrincipal("workspace-primary"))
		if err != nil || result.Retried != 1 || provider.calls != 0 {
			t.Fatalf("effect=%#v result=%#v calls=%d err=%v", effect, result, provider.calls, err)
		}
	}
}

func (r *providerTimeoutWorkerRepository) ListDueOutbox(context.Context, principalmodel.SystemScope, int, string) ([]integrationmodel.IntegrationOutboxMessage, error) {
	if r.message.Status != "queued" {
		return nil, nil
	}
	return []integrationmodel.IntegrationOutboxMessage{r.message}, nil
}

func (r *providerTimeoutWorkerRepository) ClaimOutbox(context.Context, string, string, string, string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	if r.message.Status != "queued" {
		return r.message, false, nil
	}
	r.message.Status = "sending"
	r.message.LeaseOwner = "runtime-a"
	r.message.FencingToken++
	return r.message, true, nil
}

func (r *providerTimeoutWorkerRepository) HeartbeatOutbox(context.Context, string, string, string, int64, string) (integrationmodel.IntegrationOutboxMessage, error) {
	return r.message, nil
}

func (r *providerTimeoutWorkerRepository) ScheduleOutboxRetry(_ context.Context, _, _, _ string, _ int64, _ int, errorText, _ string) (integrationmodel.IntegrationOutboxMessage, error) {
	r.message.Status = "queued"
	r.message.Error = errorText
	r.message.AttemptCount++
	r.message.LeaseOwner = ""
	return r.message, nil
}

func (r *providerTimeoutWorkerRepository) UpdateOutboxStatus(_ context.Context, _, _, _ string, _ int64, status, responseRef, errorText, ackDeadlineAt, _ string) (integrationmodel.IntegrationOutboxMessage, error) {
	r.message.Status = status
	r.message.ResponseRef = responseRef
	r.message.Error = errorText
	r.message.AckDeadlineAt = ackDeadlineAt
	r.message.LeaseOwner = ""
	return r.message, nil
}

type idempotentProviderAfterTimeout struct {
	seenRequestRefs map[string]bool
	requestRefs     []string
	sideEffects     int
}

func (p *idempotentProviderAfterTimeout) SendIntegrationOutboxMessage(_ context.Context, message integrationmodel.IntegrationOutboxMessage, _ principalmodel.Principal) (OutboxSendResult, error) {
	p.requestRefs = append(p.requestRefs, message.RequestRef)
	if !p.seenRequestRefs[message.RequestRef] {
		p.seenRequestRefs[message.RequestRef] = true
		p.sideEffects++
		return OutboxSendResult{}, errors.New("provider timeout after successful side effect")
	}
	return OutboxSendResult{Status: "sent", ResponseRef: "provider:deduplicated"}, nil
}

func TestIntegrationProviderSuccessWithLocalTimeoutRequiresReconciliationWithoutResend(t *testing.T) {
	const requestRef = "provider-idempotency:payment:one"
	repository := &providerTimeoutWorkerRepository{message: integrationmodel.IntegrationOutboxMessage{
		ID: "message-1", WorkspaceID: "workspace-primary", ConnectorKey: "payment", ConnectionKey: "primary", Operation: "charge",
		Status: "queued", RequestRef: requestRef, DedupKey: "payment:one", Payload: map[string]any{"payment_id": "one"},
	}}
	provider := &idempotentProviderAfterTimeout{seenRequestRefs: map[string]bool{}}
	service := NewIntegrationApplicationService(ApplicationDependencies{WorkerRepository: repository, Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{})})
	service.RegisterIntegrationOutboxSender("payment", provider)
	principal := integrationruntime.IntegrationWorkerPrincipal("workspace-primary")

	first, err := service.ProcessDueIntegrationOutbox(t.Context(), 1, principal)
	if err != nil || first.ReconciliationRequired != 1 || repository.message.Status != "quarantined" {
		t.Fatalf("first attempt=%+v message=%+v err=%v", first, repository.message, err)
	}
	second, err := service.ProcessDueIntegrationOutbox(t.Context(), 1, principal)
	if err != nil || second.Sent != 0 || second.Retried != 0 || repository.message.Status != "quarantined" {
		t.Fatalf("second attempt=%+v message=%+v err=%v", second, repository.message, err)
	}
	if len(provider.requestRefs) != 1 || provider.requestRefs[0] != requestRef {
		t.Fatalf("provider request refs=%v, want one call with %q", provider.requestRefs, requestRef)
	}
	if provider.sideEffects != 1 {
		t.Fatalf("provider side effects=%d, want 1", provider.sideEffects)
	}
}
