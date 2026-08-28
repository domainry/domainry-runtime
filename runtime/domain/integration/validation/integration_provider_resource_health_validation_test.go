package validation

import (
	"strings"
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func resourceHealthReport() integrationmodel.IntegrationProviderResourceHealth {
	percent := 85
	return integrationmodel.IntegrationProviderResourceHealth{ObservationID: "quota:2026-07-28T12", Kind: "quota", State: "warning", EvidenceSource: "provider_api", QuotaUsedPercent: &percent, CapabilityBlocked: false, ObservedAt: "2026-07-28T12:00:00Z"}
}

func TestValidateIntegrationProviderResourceHealth(t *testing.T) {
	if err := ValidateIntegrationProviderResourceHealth(resourceHealthReport()); err != nil {
		t.Fatal(err)
	}
	valid := []integrationmodel.IntegrationProviderResourceHealth{
		{ObservationID: "quota-error", Kind: "quota", State: "exhausted", EvidenceSource: "provider_error", CapabilityBlocked: true, ObservedAt: "2026-07-28T12:00:00Z", ErrorCode: "provider.quota_exhausted"},
		{ObservationID: "billing", Kind: "billing", State: "payment_required", EvidenceSource: "provider_api", BalanceBand: "zero", CapabilityBlocked: true, ObservedAt: "2026-07-28T12:00:00Z"},
		{ObservationID: "billing-low", Kind: "billing", State: "payment_required", EvidenceSource: "provider_api", BalanceBand: "low", CapabilityBlocked: true, ObservedAt: "2026-07-28T12:00:00Z"},
		{ObservationID: "billing-negative", Kind: "billing", State: "payment_required", EvidenceSource: "provider_api", BalanceBand: "negative", CapabilityBlocked: true, ObservedAt: "2026-07-28T12:00:00Z"},
		{ObservationID: "quota-recovered", Kind: "quota", State: "healthy", PreviousState: "warning", EvidenceSource: "provider_api", ObservedAt: "2026-07-28T12:00:00Z"},
		{ObservationID: "billing-recovered", Kind: "billing", State: "healthy", PreviousState: "payment_required", EvidenceSource: "provider_api", BalanceBand: "positive", ObservedAt: "2026-07-28T12:00:00Z"},
	}
	for _, report := range valid {
		if err := ValidateIntegrationProviderResourceHealth(report); err != nil {
			t.Fatalf("valid report=%+v err=%v", report, err)
		}
	}
}

func TestValidateIntegrationProviderResourceHealthRejectsUnsafeOrInferredReports(t *testing.T) {
	negative, tooHigh := -1, 101
	tests := []struct {
		name   string
		mutate func(*integrationmodel.IntegrationProviderResourceHealth)
		code   string
	}{
		{name: "observation", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) { v.ObservationID = "bad observation" }, code: "observation_id_invalid"},
		{name: "observed at", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) { v.ObservedAt = "bad" }, code: "observed_at_invalid"},
		{name: "evidence", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) { v.EvidenceSource = "guess" }, code: "evidence_source_invalid"},
		{name: "negative percent", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) { v.QuotaUsedPercent = &negative }, code: "quota_percent_invalid"},
		{name: "high percent", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) { v.QuotaUsedPercent = &tooHigh }, code: "quota_percent_invalid"},
		{name: "balance band", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) { v.BalanceBand = "exact:12.34" }, code: "balance_band_invalid"},
		{name: "error code missing", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) { v.EvidenceSource = "provider_error" }, code: "error_code_required"},
		{name: "error metrics", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) {
			v.EvidenceSource, v.ErrorCode = "provider_error", "provider.quota"
		}, code: "provider_error_metrics_forbidden"},
		{name: "error balance", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) {
			v.EvidenceSource, v.ErrorCode, v.QuotaUsedPercent, v.BalanceBand = "provider_error", "provider.balance", nil, "low"
		}, code: "provider_error_metrics_forbidden"},
		{name: "quota balance", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) {
			v.QuotaUsedPercent, v.BalanceBand = nil, "low"
		}, code: "quota_state_invalid"},
		{name: "quota state", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) { v.State = "payment_required" }, code: "quota_state_invalid"},
		{name: "billing percent", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) { v.Kind = "billing" }, code: "billing_state_invalid"},
		{name: "billing state", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) {
			v.Kind, v.QuotaUsedPercent, v.State = "billing", nil, "warning"
		}, code: "billing_state_invalid"},
		{name: "kind", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) { v.Kind = "money" }, code: "kind_invalid"},
		{name: "recovery source", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) {
			v.State, v.PreviousState, v.EvidenceSource, v.QuotaUsedPercent, v.ErrorCode = "healthy", "warning", "provider_error", nil, "provider.recovered"
		}, code: "recovery_evidence_invalid"},
		{name: "recovery prior", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) {
			v.State, v.PreviousState, v.QuotaUsedPercent = "healthy", "healthy", nil
		}, code: "recovery_evidence_invalid"},
		{name: "unchanged", mutate: func(v *integrationmodel.IntegrationProviderResourceHealth) { v.PreviousState = "warning" }, code: "state_unchanged"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := resourceHealthReport()
			test.mutate(&report)
			if err := ValidateIntegrationProviderResourceHealth(report); err == nil || !strings.Contains(err.Error(), test.code) {
				t.Fatalf("report=%+v err=%v", report, err)
			}
		})
	}
}

func TestIntegrationResourceAlertState(t *testing.T) {
	for _, test := range []struct {
		kind, state string
		want        bool
	}{
		{"quota", "warning", true}, {"quota", "exhausted", true}, {"quota", "healthy", false},
		{"billing", "payment_required", true}, {"billing", "warning", false}, {"unknown", "payment_required", false},
	} {
		if got := integrationResourceAlertState(test.kind, test.state); got != test.want {
			t.Fatalf("kind=%q state=%q got=%v want=%v", test.kind, test.state, got, test.want)
		}
	}
}
