package validation

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

var providerResourceHealthCode = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$`)

func ValidateIntegrationProviderResourceHealth(report integrationmodel.IntegrationProviderResourceHealth) error {
	if !providerResourceHealthCode.MatchString(strings.TrimSpace(report.ObservationID)) {
		return fmt.Errorf("backend.integration.resource_health.observation_id_invalid")
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(report.ObservedAt)); err != nil {
		return fmt.Errorf("backend.integration.resource_health.observed_at_invalid")
	}
	if report.EvidenceSource != integrationmodel.IntegrationResourceEvidenceProviderAPI && report.EvidenceSource != integrationmodel.IntegrationResourceEvidenceProviderError {
		return fmt.Errorf("backend.integration.resource_health.evidence_source_invalid")
	}
	if report.QuotaUsedPercent != nil && (*report.QuotaUsedPercent < 0 || *report.QuotaUsedPercent > 100) {
		return fmt.Errorf("backend.integration.resource_health.quota_percent_invalid")
	}
	if report.BalanceBand != "" && report.BalanceBand != "positive" && report.BalanceBand != "low" && report.BalanceBand != "zero" && report.BalanceBand != "negative" {
		return fmt.Errorf("backend.integration.resource_health.balance_band_invalid")
	}
	if report.EvidenceSource == integrationmodel.IntegrationResourceEvidenceProviderError {
		if !providerResourceHealthCode.MatchString(strings.TrimSpace(report.ErrorCode)) {
			return fmt.Errorf("backend.integration.resource_health.error_code_required")
		}
		if report.QuotaUsedPercent != nil || report.BalanceBand != "" {
			return fmt.Errorf("backend.integration.resource_health.provider_error_metrics_forbidden")
		}
	}
	switch report.Kind {
	case integrationmodel.IntegrationResourceKindQuota:
		if report.BalanceBand != "" || (report.State != integrationmodel.IntegrationResourceStateHealthy && report.State != integrationmodel.IntegrationResourceStateWarning && report.State != integrationmodel.IntegrationResourceStateExhausted) {
			return fmt.Errorf("backend.integration.resource_health.quota_state_invalid")
		}
	case integrationmodel.IntegrationResourceKindBilling:
		if report.QuotaUsedPercent != nil || (report.State != integrationmodel.IntegrationResourceStateHealthy && report.State != integrationmodel.IntegrationResourceStatePaymentRequired) {
			return fmt.Errorf("backend.integration.resource_health.billing_state_invalid")
		}
	default:
		return fmt.Errorf("backend.integration.resource_health.kind_invalid")
	}
	if report.State == integrationmodel.IntegrationResourceStateHealthy {
		if report.EvidenceSource != integrationmodel.IntegrationResourceEvidenceProviderAPI || !integrationResourceAlertState(report.Kind, report.PreviousState) {
			return fmt.Errorf("backend.integration.resource_health.recovery_evidence_invalid")
		}
	} else if report.PreviousState == report.State {
		return fmt.Errorf("backend.integration.resource_health.state_unchanged")
	}
	return nil
}

func integrationResourceAlertState(kind, state string) bool {
	if kind == integrationmodel.IntegrationResourceKindQuota {
		return state == integrationmodel.IntegrationResourceStateWarning || state == integrationmodel.IntegrationResourceStateExhausted
	}
	return kind == integrationmodel.IntegrationResourceKindBilling && state == integrationmodel.IntegrationResourceStatePaymentRequired
}
