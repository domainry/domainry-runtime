package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	connector "github.com/domainry/domainry-connector-sdk"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	integrationruntime "github.com/domainry/domainry-runtime/runtime/domain/integration/runtime"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

const connectorBackgroundLeaseTTL = 2 * time.Minute

type connectorBackgroundRegistry interface {
	ConnectorBackgroundProvider(string, string) (connector.Adapter, connector.ProviderDescriptor, connector.BackgroundProcessor, bool)
}

func (s *IntegrationApplicationService) upsertConnectionAndSyncBackground(ctx context.Context, connection integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
	saved, err := s.configRepo.UpsertConnection(ctx, connection.WorkspaceID, connection)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	if err := s.syncConnectorBackgroundTasks(ctx, saved, s.worker.Clock.Now().UTC()); err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	return saved, nil
}

func (s *IntegrationApplicationService) syncConnectorBackgroundTasks(ctx context.Context, connection integrationmodel.IntegrationConnection, now time.Time) error {
	if s.providerStateRepo == nil {
		return nil
	}
	registry, ok := s.registry.(connectorBackgroundRegistry)
	if !ok {
		return nil
	}
	_, descriptor, processor, ok := registry.ConnectorBackgroundProvider(connection.ConnectorKey, connection.ProviderKey)
	if !ok {
		return s.providerStateRepo.SyncConnectorProviderTasks(ctx, connection, nil, now.UTC().Format(time.RFC3339))
	}
	publicConnection := (&publicProviderAdapter{descriptor: descriptor}).scopedConnection(connection)
	descriptors := processor.BackgroundTasks(publicConnection)
	tasks := make([]integrationrepository.ConnectorProviderTask, 0, len(descriptors))
	seen := map[string]bool{}
	for _, descriptor := range descriptors {
		if err := descriptor.Validate(); err != nil {
			return err
		}
		if seen[descriptor.Key] {
			return fmt.Errorf("duplicate connector background task %s", descriptor.Key)
		}
		seen[descriptor.Key] = true
		tasks = append(tasks, integrationrepository.ConnectorProviderTask{Key: descriptor.Key, StateVersion: descriptor.StateVersion, InitialState: json.RawMessage(`{}`)})
	}
	return s.providerStateRepo.SyncConnectorProviderTasks(ctx, connection, tasks, now.UTC().Format(time.RFC3339))
}

func (s *IntegrationApplicationService) StartConnectorBackgroundWorker(ctx context.Context, interval time.Duration, limit int) <-chan struct{} {
	if s == nil || s.providerStateRepo == nil || s.registry == nil {
		return workerplatform.Stopped()
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	_ = s.ReconcileConnectorBackgroundTasks(ctx)
	return workerplatform.StartNamedLoop(ctx, "connector_provider_background", interval, func() { s.worker.Control.RunIfAccepting(func() { _, _ = s.ProcessDueConnectorBackground(ctx, limit) }) })
}

func (s *IntegrationApplicationService) ReconcileConnectorBackgroundTasks(ctx context.Context) error {
	if s == nil || s.providerStateRepo == nil {
		return nil
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "reconcile Connector Provider background task declarations")
	if err := s.providerStateRepo.MigrateLegacyConnectorProviderStates(ctx, scope); err != nil {
		return err
	}
	connections, err := s.providerStateRepo.ListConnectorProviderConnections(ctx, scope)
	if err != nil {
		return err
	}
	now := s.worker.Clock.Now().UTC()
	for _, connection := range connections {
		if err := s.syncConnectorBackgroundTasks(ctx, connection, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *IntegrationApplicationService) ProcessDueConnectorBackground(ctx context.Context, limit int) (int, error) {
	if s == nil || s.providerStateRepo == nil {
		return 0, nil
	}
	registry, ok := s.registry.(connectorBackgroundRegistry)
	if !ok {
		return 0, nil
	}
	now := s.worker.Clock.Now().UTC()
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "execute selected Connector Provider background tasks")
	candidates, err := s.providerStateRepo.ListDueConnectorProviderStates(ctx, scope, limit, now.Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, candidate := range candidates {
		provider, descriptor, processor, ok := registry.ConnectorBackgroundProvider(candidate.State.ConnectorKey, candidate.State.ProviderKey)
		if !ok {
			continue
		}
		claimed, won, err := s.providerStateRepo.ClaimConnectorProviderState(ctx, candidate.State.WorkspaceID, candidate.State.ConnectionKey, candidate.State.TaskKey, s.worker.WorkerID.String(), now.Format(time.RFC3339), now.Add(connectorBackgroundLeaseTTL).Format(time.RFC3339))
		if err != nil || !won {
			continue
		}
		candidate.State = claimed
		secrets, err := s.ResolveAdapterSecrets(ctx, candidate.Connection)
		if err != nil {
			s.failConnectorBackground(ctx, claimed, err, now)
			continue
		}
		bridge := &publicProviderAdapter{provider: provider, descriptor: descriptor}
		secrets, err = bridge.scopeSecrets(secrets)
		if err != nil {
			s.failConnectorBackground(ctx, claimed, err, now)
			continue
		}
		related := map[string]json.RawMessage{}
		for key, state := range candidate.RelatedStates {
			related[key] = append(json.RawMessage(nil), state.Payload...)
		}
		request := connector.BackgroundRequest{TaskKey: claimed.TaskKey, StateVersion: claimed.StateVersion, Connection: bridge.scopedConnection(candidate.Connection), State: append(json.RawMessage(nil), claimed.Payload...), RelatedStates: related, Secrets: secrets, Now: now, Principal: toConnectorPrincipal(integrationruntime.IntegrationWorkerPrincipal(claimed.WorkspaceID))}
		result, err := processor.ProcessBackground(ctx, request)
		if err != nil {
			s.failConnectorBackground(ctx, claimed, err, now)
			continue
		}
		if err := result.Validate(); err != nil {
			s.failConnectorBackground(ctx, claimed, err, now)
			continue
		}
		principal := integrationruntime.IntegrationWorkerPrincipal(claimed.WorkspaceID)
		accepted := true
		for _, event := range result.Events {
			if strings.TrimSpace(event.ExternalID) == "" || strings.TrimSpace(event.EventType) == "" || !json.Valid(event.Payload) {
				accepted = false
				break
			}
			payload := map[string]any{}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				accepted = false
				break
			}
			status := "received"
			if strings.Contains(event.EventType, "malformed") || strings.Contains(event.EventType, "mismatch") {
				status = "quarantined"
			}
			if _, _, err := s.RecordIntegrationEvent(ctx, integrationmodel.IntegrationEventRecordRequest{Provider: claimed.ProviderKey, EventType: event.EventType, ExternalID: event.ExternalID, Status: status, Payload: payload}, principal); err != nil {
				accepted = false
				break
			}
		}
		if !accepted {
			s.failConnectorBackground(ctx, claimed, fmt.Errorf("connector background event persistence failed"), now)
			continue
		}
		if err := s.PersistAdapterSecretUpdates(ctx, candidate.Connection, secrets, result.SecretUpdates); err != nil {
			s.failConnectorBackground(ctx, claimed, err, now)
			continue
		}
		for key, value := range result.SecretUpdates {
			secrets[key] = value
		}
		dueAt := ""
		if !result.NextDueAt.IsZero() {
			dueAt = result.NextDueAt.UTC().Format(time.RFC3339)
		}
		if _, err := s.providerStateRepo.CompleteConnectorProviderState(ctx, claimed, result.State, dueAt, s.worker.Clock.Now().UTC().Format(time.RFC3339)); err != nil {
			continue
		}
		for _, task := range result.WakeTasks {
			_ = s.providerStateRepo.WakeConnectorProviderState(ctx, claimed.WorkspaceID, claimed.ConnectionKey, task, now.Format(time.RFC3339))
		}
		for _, commit := range result.Commit {
			if !json.Valid(commit.Payload) {
				continue
			}
			_, _ = provider.Call(ctx, connector.CallRequest{ConnectorKey: claimed.ConnectorKey, ProviderKey: claimed.ProviderKey, OperationKey: commit.OperationKey, ContractSHA256: commit.ContractSHA256, Mode: connector.ModeCall, Connection: toConnectorBackgroundConnection(candidate.Connection), Payload: commit.Payload, Secrets: secrets, Principal: request.Principal})
		}
		completed++
	}
	return completed, nil
}

func (s *IntegrationApplicationService) failConnectorBackground(ctx context.Context, state integrationmodel.ConnectorProviderState, cause error, now time.Time) {
	delay := time.Duration(1<<min(state.AttemptCount, 6)) * 15 * time.Second
	code := "connector.background_failed"
	if value, ok := connector.ProviderErrorCodeOf(cause); ok {
		code = value
	}
	_, _ = s.providerStateRepo.FailConnectorProviderState(ctx, state, code, now.Add(delay).Format(time.RFC3339), s.worker.Clock.Now().UTC().Format(time.RFC3339))
}
func toConnectorBackgroundConnection(value integrationmodel.IntegrationConnection) connector.Connection {
	return connector.Connection{Key: value.Key, WorkspaceID: value.WorkspaceID, ConnectorKey: value.ConnectorKey, ProviderKey: value.ProviderKey, Name: value.Name, Status: value.Status, Config: cloneMap(value.Config), SecretRefs: cloneStringMap(value.SecretRefs), CreatedBy: value.CreatedBy, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}
