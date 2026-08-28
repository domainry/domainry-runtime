package integration

import (
	"context"
	"fmt"
	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationvalidation "github.com/domainry/domainry-runtime/runtime/domain/integration/validation"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/logging"
	"go.uber.org/zap"
)

func ResolveConnectorProvider(connector integrationmodel.ConnectorSchema, requested string) (string, error) {
	return resolveConnectorProvider(connector, requested)
}

func ConnectorProviderKeys(connector integrationmodel.ConnectorSchema) []string {
	return connectorProviderKeys(connector)
}

func (s *IntegrationApplicationService) MigratePersistedConnectionProvider(ctx context.Context, connection integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
	connector, ok := s.ConnectorDefinition(connection.ConnectorKey)
	if !ok {
		return connection, notFound("backend.integration.connector.not_found")
	}
	if _, legacyProvider := connection.Config["provider"]; legacyProvider {
		return connection, badRequest("backend.integration.connection.provider_in_config_forbidden", "field_path", "config.provider")
	}
	requested := strings.TrimSpace(connection.ProviderKey)
	providerKey, err := resolveConnectorProvider(connector, requested)
	if err != nil {
		return connection, err
	}
	status, err := NormalizeConnectionStatus(connection.Status)
	if err != nil {
		return connection, err
	}
	changed := connection.ProviderKey != providerKey || connection.Status != status
	connection.ProviderKey, connection.Status = providerKey, status
	if !changed {
		return connection, nil
	}
	saved, err := s.upsertConnectionAndSyncBackground(ctx, connection)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, fmt.Errorf("backfill integration connection provider: %w", err)
	}
	return saved, nil
}

// PublishProviderResourceHealth accepts only explicit provider-normalized
// evidence. Absence of a report is never interpreted as quota or billing
// trouble, and a missing connection owner never broadens the audience.
func (s *IntegrationApplicationService) PublishProviderResourceHealth(ctx context.Context, connection integrationmodel.IntegrationConnection, report integrationmodel.IntegrationProviderResourceHealth, scope principalmodel.SystemScope) (notificationmodel.NotificationEvent, bool, error) {
	if !scope.Valid() {
		return notificationmodel.NotificationEvent{}, false, forbidden("backend.system_scope_required")
	}
	if err := ctx.Err(); err != nil {
		return notificationmodel.NotificationEvent{}, false, err
	}
	if err := integrationvalidation.ValidateIntegrationProviderResourceHealth(report); err != nil {
		return notificationmodel.NotificationEvent{}, false, badRequest(err.Error())
	}
	if s == nil || s.publishNotification == nil || strings.TrimSpace(connection.Key) == "" || strings.TrimSpace(connection.CreatedBy) == "" {
		return notificationmodel.NotificationEvent{}, false, nil
	}
	if integrationAuthorizeWorkspaceCommand(connection.WorkspaceID) != nil {
		return notificationmodel.NotificationEvent{}, false, nil
	}
	intent := integrationProviderResourceHealthIntent(connection, report)
	return s.publishNotification(ctx, intent, scope)
}

func integrationProviderResourceHealthIntent(connection integrationmodel.IntegrationConnection, report integrationmodel.IntegrationProviderResourceHealth) notificationmodel.NotificationIntent {
	eventType, severity := "", "warning"
	if report.Kind == integrationmodel.IntegrationResourceKindQuota {
		switch report.State {
		case integrationmodel.IntegrationResourceStateWarning:
			eventType = "integration.quota.warning"
		case integrationmodel.IntegrationResourceStateExhausted:
			eventType, severity = "integration.quota.exhausted", "critical"
		default:
			eventType, severity = "integration.quota.recovered", "info"
		}
	} else if report.State == integrationmodel.IntegrationResourceStatePaymentRequired {
		eventType, severity = "integration.billing.payment_required", "critical"
	} else {
		eventType, severity = "integration.billing.recovered", "info"
	}
	variables := map[string]any{
		"connection_name": credentialDisplayName(connection.Name, connection.Key), "provider_key": strings.TrimSpace(connection.ProviderKey),
		"capability_blocked": report.CapabilityBlocked, "observed_at": report.ObservedAt,
	}
	if report.QuotaUsedPercent != nil {
		variables["quota_used_percent"] = *report.QuotaUsedPercent
	}
	if report.BalanceBand != "" {
		variables["balance_band"] = report.BalanceBand
	}
	if report.ErrorCode != "" {
		variables["error_code"] = report.ErrorCode
	}
	intent := notificationmodel.NotificationIntent{
		ID: credentialNotificationID(connection.WorkspaceID, "resource_health", connection.Key, report.ObservationID), WorkspaceID: connection.WorkspaceID,
		SourceEventID: "provider_resource_health:" + connection.Key + ":" + report.ObservationID, EventType: eventType, Severity: severity, Surface: "business_workspace",
		RecipientUserIDs: []string{strings.TrimSpace(connection.CreatedBy)}, SubjectType: "integration_connection", SubjectID: connection.Key, SubjectVersion: connection.UpdatedAt,
		GroupKey: "integration_resource:" + report.Kind + ":" + connection.Key, DedupeKey: "integration.resource_health:" + connection.Key + ":" + report.ObservationID,
		AlertState: notificationmodel.NotificationAlertFiring, OccurredAt: normalizeProviderResourceObservedAt(report.ObservedAt), Variables: variables,
	}
	if report.State == integrationmodel.IntegrationResourceStateHealthy {
		intent.ActionState, intent.AlertState = notificationmodel.NotificationActionCompleted, notificationmodel.NotificationAlertResolved
	}
	return intent
}

func normalizeProviderResourceObservedAt(value string) string {
	parsed, _ := time.Parse(time.RFC3339, strings.TrimSpace(value))
	return parsed.UTC().Format(time.RFC3339Nano)
}

func (s *IntegrationApplicationService) recordProviderResourceHealthFromCall(ctx context.Context, connection integrationmodel.IntegrationConnection, report *integrationmodel.IntegrationProviderResourceHealth) {
	if report == nil {
		return
	}
	_, _, err := s.PublishProviderResourceHealth(ctx, connection, *report, principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "publish provider resource health from connector call"))
	if err != nil && ctx.Err() == nil {
		logging.FromContext(ctx).Error("integration provider resource health notification could not be recorded", append(logging.StableErrorFields(err), zap.String("connection_key", connection.Key))...)
	}
}

func (s *IntegrationApplicationService) recordCredentialRefreshRecovery(ctx context.Context, connection integrationmodel.IntegrationConnection, principal principalmodel.Principal) {
	if err := s.RecordCredentialRefreshRecovery(ctx, connection, principal); err != nil {
		logging.FromContext(ctx).Error("integration credential refresh recovery notification could not be recorded", append(logging.StableErrorFields(err), zap.String("connection_key", connection.Key))...)
	}
}

func (s *IntegrationApplicationService) recordCredentialRefreshFailure(ctx context.Context, connection integrationmodel.IntegrationConnection, principal principalmodel.Principal, callErr error) {
	if err := s.RecordCredentialRefreshFailure(ctx, connection, principal, callErr); err != nil {
		logging.FromContext(ctx).Error("integration credential refresh failure notification could not be recorded", append(logging.StableErrorFields(err), zap.String("connection_key", connection.Key))...)
	}
}
