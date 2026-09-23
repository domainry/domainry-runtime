package operations

import (
	"testing"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func TestOperationsLifecycleSpecsAreClosedByRegisteredKindRetentionAndScope(t *testing.T) {
	seen := map[string]bool{}
	for _, retentionClass := range []operationsmodel.OperationsRetentionClass{
		operationsmodel.OperationsRetentionTechnical,
		operationsmodel.OperationsRetentionLegalAudit,
	} {
		for _, scope := range []operationsmodel.OperationsExecutionScope{
			operationsmodel.OperationsExecutionWorkspace,
			operationsmodel.OperationsExecutionSystem,
		} {
			for _, definition := range operationsLifecycleDefinitions(retentionClass, scope) {
				if seen[definition.Kind] {
					t.Fatalf("operation kind %s appears in more than one retention group", definition.Kind)
				}
				seen[definition.Kind] = true
			}
		}
	}
	if len(seen) != 30 {
		t.Fatalf("registered lifecycle kinds=%d", len(seen))
	}
	specs := operationsLifecycleSpecs()
	if len(specs) != 8 {
		t.Fatalf("lifecycle specs=%d", len(specs))
	}
	policies := map[string]int{}
	for _, spec := range specs {
		if spec.Table != "_operations" || spec.TimeColumn != "finished_at" || len(spec.EligibleStatuses) != 1 || spec.AdditionalPredicate == nil {
			t.Fatalf("incomplete lifecycle spec=%#v", spec)
		}
		if len(spec.ReferenceChecks) != 2 {
			t.Fatalf("missing artifact/child reference guards: %#v", spec.ReferenceChecks)
		}
		policies[spec.PolicyKey]++
	}
	if policies["operations.technical_receipt.v1"] != 4 || policies["operations.receipt.v1"] != 4 {
		t.Fatalf("policy specs=%#v", policies)
	}
}
