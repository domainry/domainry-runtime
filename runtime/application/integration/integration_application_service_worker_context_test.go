// Integration application service worker-context tests.
package integration

import (
	"context"
	"errors"
	integrationruntime "github.com/domainry/domainry-runtime/runtime/domain/integration/runtime"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
)

type cancellingIntegrationWorkerRepository struct {
	integrationrepository.IntegrationWorkerRepository
	updates          int
	retries          int
	updateContextErr error
	updateStatus     string
}

func (r *cancellingIntegrationWorkerRepository) ListDueOutbox(context.Context, principalmodel.SystemScope, int, string) ([]integrationmodel.IntegrationOutboxMessage, error) {
	return []integrationmodel.IntegrationOutboxMessage{{ID: "message_1", WorkspaceID: "workspace-primary", ConnectorKey: "cancel_sender", Status: "queued"}}, nil
}

func (r *cancellingIntegrationWorkerRepository) ClaimOutbox(_ context.Context, _, _, _, _ string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	return integrationmodel.IntegrationOutboxMessage{ID: "message_1", WorkspaceID: "workspace-primary", ConnectorKey: "cancel_sender", Status: "sending"}, true, nil
}
func (r *cancellingIntegrationWorkerRepository) UpdateOutboxStatus(ctx context.Context, workspaceID, messageID, owner string, token int64, status, responseRef, errorText, ackDeadlineAt, now string) (integrationmodel.IntegrationOutboxMessage, error) {
	r.updates++
	r.updateContextErr = ctx.Err()
	r.updateStatus = status
	return integrationmodel.IntegrationOutboxMessage{ID: messageID, WorkspaceID: workspaceID, Status: status}, nil
}
func (r *cancellingIntegrationWorkerRepository) ScheduleOutboxRetry(context.Context, string, string, string, int64, int, string, string) (integrationmodel.IntegrationOutboxMessage, error) {
	r.retries++
	return integrationmodel.IntegrationOutboxMessage{}, nil
}

type cancellingOutboxSender struct{ cancel context.CancelFunc }

func (s cancellingOutboxSender) SendIntegrationOutboxMessage(ctx context.Context, _ integrationmodel.IntegrationOutboxMessage, _ principalmodel.Principal) (OutboxSendResult, error) {
	s.cancel()
	return OutboxSendResult{}, ctx.Err()
}

func TestIntegrationOutboxCancellationReleasesClaimWithoutRecordingFailureOrRetry(t *testing.T) {
	repository := &cancellingIntegrationWorkerRepository{}
	service := NewIntegrationApplicationService(ApplicationDependencies{WorkerRepository: repository, Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{})})
	ctx, cancel := context.WithCancel(context.Background())
	service.RegisterIntegrationOutboxSender("cancel_sender", cancellingOutboxSender{cancel: cancel})
	result, err := service.ProcessDueIntegrationOutbox(ctx, 1, integrationruntime.IntegrationWorkerPrincipal("workspace-primary"))
	if err != nil {
		t.Fatalf("cancelled send error=%v", err)
	}
	if result.Skipped != 1 {
		t.Fatalf("cancelled send result=%+v, want one skipped", result)
	}
	if repository.updates != 1 || repository.retries != 0 || repository.updateStatus != "queued" || !errors.Is(ctx.Err(), context.Canceled) || repository.updateContextErr != nil {
		t.Fatalf("cancelled send release: updates=%d retries=%d status=%q worker_ctx=%v cleanup_ctx=%v", repository.updates, repository.retries, repository.updateStatus, ctx.Err(), repository.updateContextErr)
	}
}
