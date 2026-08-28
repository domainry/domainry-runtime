package integrationtest

import (
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	"net/http"
	"path/filepath"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestCRMAutomationRuleLifecycleThroughPublicAPI(t *testing.T) {
	application := newIntegrationRuntime(t, config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath: filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "crm-customer-360.json"), UploadDir: filepath.Join(t.TempDir(), "uploads"),
	})
	defer application.CloseContext(t.Context())
	handler := application.Routes()

	rules := runtimeFixtureRequest[struct {
		Items []automationmodel.AutomationRuleSchema `json:"items"`
		Count int                                    `json:"count"`
	}](t, handler, "sales_manager", http.MethodGet, "/automation-rules", nil)
	if rules.Count < 1 || len(rules.Items) == 0 {
		t.Fatalf("expected seeded lifecycle automation rule, got %#v", rules)
	}
	validation := runtimeFixtureRequest[map[string]any](t, handler, "sales_manager", http.MethodPost, "/automation-rules/validate", rules.Items[0])
	if valid, _ := validation["valid"].(bool); !valid {
		t.Fatalf("expected draft validation to succeed, got %#v", validation)
	}
	invalidRule := rules.Items[0]
	invalidRule.Trigger.Operation = "unsupported"
	invalidValidation := runtimeFixtureRequest[struct {
		Valid  bool `json:"valid"`
		Errors []struct {
			Section   string `json:"section"`
			FieldPath string `json:"field_path"`
			ErrorCode string `json:"error_code"`
		} `json:"errors"`
	}](t, handler, "sales_manager", http.MethodPost, "/automation-rules/validate", invalidRule)
	if invalidValidation.Valid || len(invalidValidation.Errors) != 1 || invalidValidation.Errors[0].Section != "trigger" || invalidValidation.Errors[0].FieldPath != "trigger.operation" || invalidValidation.Errors[0].ErrorCode != "backend.automation.operation_invalid" {
		t.Fatalf("expected typed trigger validation issue, got %#v", invalidValidation)
	}
	var rule automationmodel.AutomationRuleSchema
	for _, candidate := range rules.Items {
		if candidate.Key == "customer.verify_business_license" {
			rule = candidate
			break
		}
	}
	if rule.Key == "" {
		t.Fatalf("expected customer.verify_business_license in seeded rules, got %#v", rules.Items)
	}
	current := loadMetadataDefinitionFixture(t, handler, "sales_manager", "automation_rule", rule.Key)
	rule.Enabled = false
	disabledDefinition := publishSystemDefinitionUpdateFixture(t, handler, "sales_manager", "automation-author", "automation-approver", "automation-lifecycle-disable", current, rule, "automation.rule")
	disabled := runtimeFixtureRequest[automationmodel.AutomationRuleSchema](t, handler, "sales_manager", http.MethodGet, "/automation-rules/customer.verify_business_license", nil)
	if disabled.Enabled {
		t.Fatalf("expected system draft disable to preserve the rule, got %#v", disabled)
	}
	rule.Enabled = true
	publishSystemDefinitionUpdateFixture(t, handler, "sales_manager", "automation-author", "automation-approver", "automation-lifecycle-enable", disabledDefinition, rule, "automation.rule")
	enabled := runtimeFixtureRequest[automationmodel.AutomationRuleSchema](t, handler, "sales_manager", http.MethodGet, "/automation-rules/customer.verify_business_license", nil)
	if !enabled.Enabled {
		t.Fatalf("expected system draft enable to restore the rule, got %#v", enabled)
	}
}
