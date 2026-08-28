package integration

import (
	"context"
	"errors"
	"fmt"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"

	"strings"
	"time"

	resilience "github.com/domainry/domainry-runtime/runtime/platform/resilience"
)

const (
	defaultSyncCallTimeout      = 15 * time.Second
	defaultSyncCircuitThreshold = 5
	defaultSyncCircuitCooldown  = time.Minute
	defaultSyncRateLimitWindow  = time.Minute
)

// normalizeSyncCallGovernance makes reliability policy a Runtime-owned
// property. Callers identify the operation and Connection; they cannot weaken
// timeout, circuit or rate-limit policy by choosing request fields.
func normalizeSyncCallGovernance(req SyncCallRequest, connection integrationmodel.IntegrationConnection, operation integrationmodel.ConnectorOperationSchema) SyncCallRequest {
	defaultTimeout := time.Duration(operation.TimeoutDefaultSeconds) * time.Second
	if defaultTimeout <= 0 {
		defaultTimeout = defaultSyncCallTimeout
	}
	maximumTimeout := time.Duration(operation.TimeoutMaxSeconds) * time.Second
	if maximumTimeout <= 0 {
		maximumTimeout = defaultTimeout
	}
	configuredTimeout := time.Duration(integrationpolicy.IntegrationConfigInt(connection.Config, int(defaultTimeout/time.Second), "sync_timeout_seconds", "timeout_seconds")) * time.Second
	if configuredTimeout <= 0 {
		configuredTimeout = defaultTimeout
	}
	if configuredTimeout > maximumTimeout {
		configuredTimeout = maximumTimeout
	}
	req.Timeout = configuredTimeout

	req.CircuitThreshold = integrationpolicy.IntegrationConfigInt(connection.Config, defaultSyncCircuitThreshold, "sync_circuit_failure_threshold", "circuit_failure_threshold")
	if req.CircuitThreshold <= 0 {
		req.CircuitThreshold = defaultSyncCircuitThreshold
	}
	req.CircuitCooldown = time.Duration(integrationpolicy.IntegrationConfigInt(connection.Config, int(defaultSyncCircuitCooldown/time.Second), "sync_circuit_cooldown_seconds", "circuit_cooldown_seconds")) * time.Second
	if req.CircuitCooldown <= 0 {
		req.CircuitCooldown = defaultSyncCircuitCooldown
	}
	req.RateLimitCount = integrationpolicy.IntegrationConfigInt(connection.Config, 0, "sync_rate_limit_count")
	if req.RateLimitCount < 0 {
		req.RateLimitCount = 0
	}
	req.RateLimitWindow = time.Duration(integrationpolicy.IntegrationConfigInt(connection.Config, int(defaultSyncRateLimitWindow/time.Second), "sync_rate_limit_window_seconds")) * time.Second
	if req.RateLimitWindow <= 0 {
		req.RateLimitWindow = defaultSyncRateLimitWindow
	}
	return req
}

func (s *IntegrationApplicationService) beforeSyncPolicy(ctx context.Context, req SyncCallRequest, connection integrationmodel.IntegrationConnection, principal principalmodel.Principal, now time.Time) error {
	key := SyncPolicyKey(req, connection, principal)
	if s.policyStore == nil || (req.CircuitThreshold <= 0 && req.RateLimitCount <= 0) {
		return nil
	}
	err := s.policyStore.Before(ctx, "sync:"+key, resilience.Config{FailureThreshold: req.CircuitThreshold, Cooldown: req.CircuitCooldown, RateLimit: req.RateLimitCount, RateWindow: req.RateLimitWindow}, now)
	if errors.Is(err, resilience.ErrCircuitOpen) {
		return fmt.Errorf("backend.integration.sync_call.circuit_open")
	}
	if errors.Is(err, resilience.ErrRateLimited) {
		return fmt.Errorf("backend.integration.sync_call.rate_limited")
	}
	return err
}

func (s *IntegrationApplicationService) afterSyncPolicy(ctx context.Context, req SyncCallRequest, connection integrationmodel.IntegrationConnection, principal principalmodel.Principal, success bool, now time.Time) error {
	key := SyncPolicyKey(req, connection, principal)
	if s.policyStore == nil || req.CircuitThreshold <= 0 {
		return nil
	}
	cooldown := req.CircuitCooldown
	if cooldown <= 0 {
		cooldown = time.Minute
	}
	return s.policyStore.Record(ctx, "sync:"+key, resilience.Config{FailureThreshold: req.CircuitThreshold, Cooldown: cooldown}, success, now)
}

func (s *IntegrationApplicationService) BeforeGenericWebhookSend(ctx context.Context, connection integrationmodel.IntegrationConnection, now time.Time) error {
	if err := integrationAuthorizeWorkspaceCommand(connection.WorkspaceID); err != nil {
		return err
	}
	if s.policyStore == nil {
		return nil
	}
	key := connectionCircuitKey(connection)
	minInterval := time.Duration(integrationpolicy.IntegrationConfigInt(connection.Config, 0, "min_interval_seconds")) * time.Second
	err := s.policyStore.Before(ctx, "outbox:"+key, resilience.Config{MinInterval: minInterval}, now)
	if errors.Is(err, resilience.ErrCircuitOpen) {
		return fmt.Errorf("backend.integration.outbox.circuit_open")
	}
	if errors.Is(err, resilience.ErrRateLimited) {
		return fmt.Errorf("backend.integration.outbox.rate_limited")
	}
	return err
}

func (s *IntegrationApplicationService) AfterGenericWebhookSend(ctx context.Context, connection integrationmodel.IntegrationConnection, success bool, now time.Time) error {
	if err := integrationAuthorizeWorkspaceCommand(connection.WorkspaceID); err != nil {
		return err
	}
	if s.policyStore == nil {
		return nil
	}
	threshold := integrationpolicy.IntegrationConfigInt(connection.Config, 5, "circuit_failure_threshold")
	cooldown := time.Duration(integrationpolicy.IntegrationConfigInt(connection.Config, 60, "circuit_cooldown_seconds")) * time.Second
	if threshold <= 0 {
		threshold = 5
	}
	if cooldown <= 0 {
		cooldown = time.Minute
	}
	return s.policyStore.Record(ctx, "outbox:"+connectionCircuitKey(connection), resilience.Config{FailureThreshold: threshold, Cooldown: cooldown}, success, now)
}

func connectionCircuitKey(connection integrationmodel.IntegrationConnection) string {
	workspaceID := strings.TrimSpace(connection.WorkspaceID)
	return workspaceID + ":" + strings.TrimSpace(connection.Key)
}
