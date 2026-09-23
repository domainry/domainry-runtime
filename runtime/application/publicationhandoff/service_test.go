package publicationhandoff

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	runtimeext "github.com/domainry/domainry-runtime/pkg/runtimeext"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
)

type publicationRepositoryProbe struct {
	inserted publicationmodel.Message
}

func (p *publicationRepositoryProbe) ListOutbox(context.Context, string, string, string, int) ([]publicationmodel.Message, error) {
	return []publicationmodel.Message{p.inserted}, nil
}
func (p *publicationRepositoryProbe) InsertOutbox(_ context.Context, _ string, message publicationmodel.Message) (publicationmodel.Message, error) {
	p.inserted = message
	return message, nil
}
func (*publicationRepositoryProbe) UpdateOutboxStatus(context.Context, string, string, string, string, string) (publicationmodel.Message, error) {
	return publicationmodel.Message{}, nil
}
func (*publicationRepositoryProbe) ScheduleOutboxRetry(context.Context, string, string, int, string) (publicationmodel.Message, error) {
	return publicationmodel.Message{}, nil
}
func (p *publicationRepositoryProbe) GetOutbox(context.Context, string, string) (publicationmodel.Message, bool, error) {
	return p.inserted, p.inserted.ID != "", nil
}

type publicationWorkerProbe struct {
	publicationRepositoryProbe
	status, responseRef string
	due                 []publicationmodel.Message
	listErr             error
}

func (p *publicationWorkerProbe) ListDueOutbox(context.Context, principalmodel.SystemScope, int, string) ([]publicationmodel.Message, error) {
	return append([]publicationmodel.Message(nil), p.due...), p.listErr
}
func (p *publicationWorkerProbe) ClaimOutbox(_ context.Context, _, _, owner, _ string) (publicationmodel.Message, bool, error) {
	value := p.inserted
	value.LeaseOwner, value.FencingToken = owner, 1
	return value, true, nil
}
func (p *publicationWorkerProbe) HeartbeatOutbox(context.Context, string, string, string, int64, string) (publicationmodel.Message, error) {
	return p.inserted, nil
}
func (p *publicationWorkerProbe) UpdateOutboxStatus(_ context.Context, _, _, _ string, _ int64, status, responseRef, _, _ string) (publicationmodel.Message, error) {
	p.status, p.responseRef = status, responseRef
	value := p.inserted
	value.Status, value.ResponseRef = status, responseRef
	return value, nil
}
func (p *publicationWorkerProbe) ScheduleOutboxRetry(context.Context, string, string, string, int64, int, string, string) (publicationmodel.Message, error) {
	return p.inserted, nil
}

type deliveryProbe struct {
	request integrationsdk.DeliveryRequest
	err     error
}

func (p *deliveryProbe) Accept(_ context.Context, request integrationsdk.DeliveryRequest) (integrationsdk.DeliveryReceipt, error) {
	p.request = request
	if p.err != nil {
		return integrationsdk.DeliveryReceipt{}, p.err
	}
	return integrationsdk.DeliveryReceipt{MessageID: request.MessageID, InvocationID: "invocation-1", Status: integrationsdk.DeliveryStatusAccepted}, nil
}
func (*deliveryProbe) Query(context.Context, string) (integrationsdk.DeliveryReceipt, error) {
	return integrationsdk.DeliveryReceipt{}, nil
}

func TestAcceptPersistsRuntimePublicationWithoutCallingOwner(t *testing.T) {
	repository := &publicationRepositoryProbe{}
	service := NewPublicationHandoffApplicationService(Dependencies{Repository: repository})
	receipt, err := service.Accept(t.Context(), integrationsdk.DeliveryRequest{MessageID: "message-1", DeduplicationKey: "dedup-1", WorkspaceID: "workspace-primary", ConnectorKey: "email", ConnectionKey: "primary", Operation: "send", Payload: json.RawMessage(`{"subject":"hello"}`)}, "scheduler")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.MessageID != "message-1" || receipt.Status != integrationsdk.DeliveryStatusAccepted {
		t.Fatalf("receipt=%#v", receipt)
	}
	if repository.inserted.ConnectorKey != "email" || repository.inserted.CreatedBy != "scheduler" || repository.inserted.Status != "queued" {
		t.Fatalf("message=%#v", repository.inserted)
	}
}

func TestDurableIntentValidationStopsAtRuntimeEnvelope(t *testing.T) {
	service := NewPublicationHandoffApplicationService(Dependencies{})
	valid := runtimeext.DurableIntent{ConsumerKey: "unknown-owner-connector", ConnectionKey: "owner-connection", OperationKey: "owner-operation", ContractSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Payload: map[string]any{}}
	if err := service.ValidateActionDurableIntent(t.Context(), valid, principalmodel.Principal{}); err != nil {
		t.Fatalf("owner facts must be validated by Integration: %v", err)
	}
	if err := service.ValidateActionDurableIntent(t.Context(), runtimeext.DurableIntent{}, principalmodel.Principal{}); err == nil {
		t.Fatal("invalid Runtime envelope was accepted")
	}
}

func TestWorkerHandsStableIdentityToIntegrationOwner(t *testing.T) {
	repository := &publicationWorkerProbe{publicationRepositoryProbe: publicationRepositoryProbe{inserted: publicationmodel.Message{ID: "message-1", DedupKey: "dedup-1", WorkspaceID: "workspace-primary", ConnectorKey: "email", ConnectionKey: "primary", Operation: "send", Payload: map[string]any{"subject": "hello"}, Status: "queued"}}}
	delivery := &deliveryProbe{}
	service := NewPublicationHandoffApplicationService(Dependencies{Repository: &repository.publicationRepositoryProbe, WorkerRepository: repository, Delivery: delivery})
	if _, err := service.process(t.Context(), Locator{WorkspaceID: "workspace-primary", MessageID: "message-1"}); err != nil {
		t.Fatal(err)
	}
	if delivery.request.MessageID != "message-1" || delivery.request.DeduplicationKey != "dedup-1" {
		t.Fatalf("delivery=%#v", delivery.request)
	}
	if repository.status != "accepted" || repository.responseRef != "invocation-1" {
		t.Fatalf("status=%q ref=%q", repository.status, repository.responseRef)
	}
}

type publicationClock struct{ now time.Time }

func (c publicationClock) Now() time.Time { return c.now }

func TestPublicationWorkerMetricsUseOwnerIdentity(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	repository := &publicationWorkerProbe{
		publicationRepositoryProbe: publicationRepositoryProbe{inserted: publicationmodel.Message{ID: "message-metrics", DedupKey: "dedup-metrics", WorkspaceID: "workspace-primary", ConnectorKey: "email", Operation: "send", Payload: map[string]any{"subject": "hello"}, Status: "queued"}},
		due: []publicationmodel.Message{
			{ID: "message-metrics", WorkspaceID: "workspace-primary", CreatedAt: now.Add(-30 * time.Second).Format(time.RFC3339Nano)},
			{ID: "message-newer", WorkspaceID: "workspace-primary", CreatedAt: now.Add(-5 * time.Second).Format(time.RFC3339Nano)},
		},
	}
	service := NewPublicationHandoffApplicationService(Dependencies{
		Repository: &repository.publicationRepositoryProbe, WorkerRepository: repository, Delivery: &deliveryProbe{},
		Worker: workerplatform.Dependencies{Clock: publicationClock{now: now}},
	})
	locators, err := service.recoverLocators(t.Context(), 25)
	if err != nil || len(locators) != 2 {
		t.Fatalf("recover locators=%+v err=%v", locators, err)
	}
	if _, err = service.process(t.Context(), Locator{WorkspaceID: "workspace-primary", MessageID: "message-metrics"}); err != nil {
		t.Fatal(err)
	}
	metrics := workerplatform.OpenMetrics(t.Context())
	for _, expected := range []string{
		`domainry_runtime_worker_owner_queue_depth{owner="runtime_publication_outbox"} 2`,
		`domainry_runtime_worker_owner_queue_lag_seconds{owner="runtime_publication_outbox"} 30.000000000`,
		`domainry_runtime_worker_owner_outcomes_total{owner="runtime_publication_outbox",outcome="completed"}`,
	} {
		if !strings.Contains(metrics, expected) {
			t.Fatalf("publication owner metrics missing %q:\n%s", expected, metrics)
		}
	}
	if strings.Contains(metrics, `table="_publication_outbox"`) {
		t.Fatalf("physical table leaked into worker metrics: %s", metrics)
	}
}

func TestPublicationWorkerRecordsRetryAttemptByOwner(t *testing.T) {
	repository := &publicationWorkerProbe{publicationRepositoryProbe: publicationRepositoryProbe{inserted: publicationmodel.Message{ID: "message-retry-metrics", WorkspaceID: "workspace-primary", ConnectorKey: "email", Operation: "send", Payload: map[string]any{}, Status: "queued"}}}
	service := NewPublicationHandoffApplicationService(Dependencies{Repository: &repository.publicationRepositoryProbe, WorkerRepository: repository, Delivery: &deliveryProbe{err: errors.New("provider unavailable")}})
	if _, err := service.process(t.Context(), Locator{WorkspaceID: "workspace-primary", MessageID: "message-retry-metrics"}); err == nil {
		t.Fatal("retry attempt unexpectedly succeeded")
	}
	metrics := workerplatform.OpenMetrics(t.Context())
	if !strings.Contains(metrics, `domainry_runtime_worker_owner_outcomes_total{owner="runtime_publication_outbox",outcome="retry"}`) {
		t.Fatalf("publication retry metric missing:\n%s", metrics)
	}
}
