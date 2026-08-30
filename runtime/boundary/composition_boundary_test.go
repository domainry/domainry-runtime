package boundary_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var bootstrapCompositionProductionFiles = stringSet(
	"action_application_wiring.go",
	"automation_application_wiring.go",
	"business_system_application_wiring.go",
	"capability_application_wiring.go",
	"changeplan_frontend_reference_application_wiring.go",
	"changeplan_reference_application_wiring.go",
	"deployment_frontend_capability_application_wiring.go",
	"integration_requirements_wiring.go",
	"integration_runtime_application_wiring.go",
	"appschema_application_wiring.go",
	"pipeline_application_wiring.go",
	"appschema_snapshot_wiring.go",
	"record_domain_service_wiring.go",
	"record_query_policy_domain_wiring.go",
	"record_runtime_application_wiring.go",
	"runtime_services.go",
	"runtime_services_application_access.go",
	"runtime_services_initialization.go",
	"runtime_services_appschema_projection.go",
	"runtime_services_record_dependencies.go",
	"runtime_services_record_initialization.go",
	"runtime_services_record_policy.go",
	"runtime_services_schema_initialization.go",
	"runtime_services_state.go",
	"runtime_connector_catalog_wiring.go",
	"scheduler_application_wiring.go",
	"scheduler_sdk_module_host_wiring.go",
	"workflow_application_wiring.go",
	"workflow_dependencies_application_wiring.go",
	"workflow_identity_application_wiring.go",
	"workflow_result_application_wiring.go",
)

var bootstrapCompositionTestFiles = stringSet(
	"composition_remaining_wiring_contract_test.go",
	"composition_final_pipeline_contract_test.go",
	"composition_wiring_contract_assertions_test.go",
	"composition_wiring_contract_test.go",
	"deployment_frontend_capability_test_support_test.go",
	"pipeline_transition_failure_baseline_test.go",
	"runtime_services_record_test.go",
	"scheduler_application_wiring_conditions_test.go",
	"scheduler_sdk_module_host_wiring_test.go",
)

func TestBootstrapCompositionContainsOnlyReviewedFiles(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join(runtimeRoot(t), "bootstrap", "composition"))
	if err != nil {
		t.Fatal(err)
	}
	seenProduction, seenTests := map[string]bool{}, map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			seenTests[name] = true
			if !bootstrapCompositionTestFiles[name] {
				t.Errorf("unreviewed bootstrap/composition test file %q", name)
			}
			continue
		}
		seenProduction[name] = true
		if !bootstrapCompositionProductionFiles[name] {
			t.Errorf("unreviewed bootstrap/composition production file %q", name)
		}
		if !validBootstrapCompositionFileName(name) {
			t.Errorf("bootstrap/composition file must retain a searchable owner and technical suffix: %q", name)
		}
	}
	assertReviewedFilesStillExist(t, "production", bootstrapCompositionProductionFiles, seenProduction)
	assertReviewedFilesStillExist(t, "test", bootstrapCompositionTestFiles, seenTests)
}

func validBootstrapCompositionFileName(name string) bool {
	return strings.HasPrefix(name, "runtime_services_") || name == "runtime_services.go" ||
		strings.HasSuffix(name, "_wiring.go") ||
		strings.HasSuffix(name, "_assembly.go") || strings.HasSuffix(name, "_bindings.go")
}

func assertReviewedFilesStillExist(t *testing.T, kind string, reviewed, seen map[string]bool) {
	t.Helper()
	for name := range reviewed {
		if !seen[name] {
			t.Errorf("reviewed bootstrap/composition %s file is missing; tighten the inventory after deletion: %s", kind, name)
		}
	}
}
