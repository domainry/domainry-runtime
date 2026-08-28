package validation

import (
	"testing"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestCRMCustomer360ProofManifestCoversCoreBusinessLoop(t *testing.T) {
	manifest := loadFixtureManifest(t, "crm-customer-360.json")
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("ValidateManifest() error = %v", err)
	}
	for _, objectKey := range []string{"customer", "contact", "lead", "opportunity", "activity", "contract", "payment"} {
		if objectMap(manifest)[objectKey].Key == "" {
			t.Fatalf("CRM proof manifest missing object %s", objectKey)
		}
	}
	for _, workflowKey := range []string{"customer.risk_follow_up", "lead.auto_route", "payment.overdue_escalation"} {
		if !workflowExists(manifest, workflowKey) {
			t.Fatalf("CRM proof manifest missing workflow %s", workflowKey)
		}
	}
	for _, reportKey := range []string{"crm_pipeline_health", "crm_activity_coverage", "crm_revenue_collection"} {
		if !reportExists(manifest, reportKey) {
			t.Fatalf("CRM proof manifest missing report %s", reportKey)
		}
	}
	for _, actionKey := range []string{"customer.mark_risk", "lead.qualify", "opportunity.advance_stage", "activity.complete", "payment.mark_collected"} {
		if !actionExists(manifest, actionKey) {
			t.Fatalf("CRM proof manifest missing action %s", actionKey)
		}
	}
}

func workflowExists(manifest manifestmodel.ManifestSchema, key string) bool {
	for _, workflow := range manifest.Workflows {
		if workflow.Key == key {
			return true
		}
	}
	return false
}

func reportExists(manifest manifestmodel.ManifestSchema, key string) bool {
	for _, report := range manifest.Reports {
		if report.Key == key {
			return true
		}
	}
	return false
}

func actionExists(manifest manifestmodel.ManifestSchema, key string) bool {
	for _, action := range manifest.Actions {
		if action.Key == key {
			return true
		}
	}
	return false
}
