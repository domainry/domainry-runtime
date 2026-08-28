package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type reconciliationErrorRepository struct {
	*independentDeliveryRepository
	called chan struct{}
}

func (r *reconciliationErrorRepository) ListPreparedInvocationsForReconciliation(context.Context, principalmodel.SystemScope, int, string) ([]integrationmodel.IntegrationInvocation, error) {
	select {
	case r.called <- struct{}{}:
	default:
	}
	return nil, errIntegrationManagementTest
}

func TestStartInvocationReconciliationWorkerEdges(t *testing.T) {
	service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: &integrationManagementDeliveryRepo{}})
	if done := service.StartInvocationReconciliationWorker(t.Context(), 0, 0, 0, principalmodel.SystemScope{}); !channelClosed(done) {
		t.Fatal("invalid system scope worker remained active")
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "reconcile integration invocations")
	if done := service.StartInvocationReconciliationWorker(t.Context(), 0, 0, 0, scope); !channelClosed(done) {
		t.Fatal("unsupported reconciliation worker remained active")
	}

	repository := &independentDeliveryRepository{invocations: []integrationmodel.IntegrationInvocation{{
		ID: "missing", WorkspaceID: "workspace", Status: "prepared", CreatedAt: "2020-01-01T00:00:00Z",
	}}}
	audited := make(chan struct{}, 1)
	service = NewIntegrationApplicationService(ApplicationDependencies{
		DeliveryRepository: repository,
		Audit: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
			audited <- struct{}{}
		},
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := service.StartInvocationReconciliationWorker(ctx, 0, 0, 0, scope)
	select {
	case <-audited:
	case <-time.After(2 * time.Second):
		t.Fatal("reconciliation worker did not run")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconciliation worker did not stop")
	}

	called := make(chan struct{}, 1)
	errorRepository := &reconciliationErrorRepository{independentDeliveryRepository: &independentDeliveryRepository{}, called: called}
	service = NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: errorRepository})
	ctx, cancel = context.WithCancel(t.Context())
	done = service.StartInvocationReconciliationWorker(ctx, time.Hour, time.Minute, 1, scope)
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("failing reconciliation worker did not run")
	}
	cancel()
	<-done
}

func channelClosed(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}

func TestReconciliationErrorRepositoryContract(t *testing.T) {
	repository := &reconciliationErrorRepository{independentDeliveryRepository: &independentDeliveryRepository{}, called: make(chan struct{}, 1)}
	_, err := repository.ListPreparedInvocationsForReconciliation(t.Context(), principalmodel.SystemScope{}, 1, "")
	if !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("error=%v", err)
	}
}
