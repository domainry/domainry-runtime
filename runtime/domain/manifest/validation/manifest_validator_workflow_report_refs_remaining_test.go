package validation

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func TestValidateReportsCoversSourceObjectAndJoinFieldEdges(t *testing.T) {
	state := newValidationState(manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{
			{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "customer_id"}}},
			{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name"}}},
		},
		Reports: []reportmodel.ReportSchema{
			{
				Key: "missing-source-object",
				Dataset: reportmodel.ReportDatasetSchema{
					Source: reportmodel.ReportDatasetSource{Alias: "orders"},
				},
			},
			{
				Key: "invalid-left-field",
				Dataset: reportmodel.ReportDatasetSchema{
					Source: reportmodel.ReportDatasetSource{ObjectKey: "order", Alias: "orders"},
					Joins: []reportmodel.ReportDatasetJoin{{
						Alias:       "customers",
						ObjectKey:   "customer",
						Type:        "left",
						LeftAlias:   "orders",
						LeftField:   "missing",
						RightField:  "id",
						Cardinality: "many_to_one",
					}},
				},
			},
		},
	}, nil)

	state.validateReports()
	diagnostics := state.errs.Error()
	if !strings.Contains(diagnostics, "reports[0].dataset.source: object_key and alias are required") {
		t.Fatalf("missing source-object diagnostic: %s", diagnostics)
	}
	if !strings.Contains(diagnostics, `reports[1].dataset.joins[0].left_field: unknown field "order".missing`) {
		t.Fatalf("missing join-field diagnostic: %s", diagnostics)
	}

	state.addReportPlanError("reports[fallback]", errors.New("unexpected planner failure"))
	if got := state.errs[len(state.errs)-1]; got.Path != "reports[fallback].dataset" ||
		!strings.Contains(got.Message, "backend.report.plan_invalid: unexpected planner failure") {
		t.Fatalf("fallback plan diagnostic=%+v", got)
	}
}

func TestReportDatasetFieldReferencesIncludesFilterPrivacyAndAnalysisVariants(t *testing.T) {
	field := func(key string) reportmodel.ReportDatasetField {
		return reportmodel.ReportDatasetField{SourceAlias: "orders", FieldKey: key}
	}
	eventField := field("status")
	references := reportDatasetFieldReferences(reportmodel.ReportDatasetSchema{
		Filters: []reportmodel.ReportDatasetFilter{{Field: field("active")}},
		Privacy: &reportmodel.ReportDatasetPrivacy{EntityField: field("customer_id")},
		Analyses: []reportmodel.ReportDatasetAnalysis{
			{EntityField: field("customer_id"), TimeField: field("created_at"), EventField: &eventField},
			{EntityField: field("customer_id"), TimeField: field("updated_at")},
		},
	})
	got := make([]string, 0, len(references))
	for _, reference := range references {
		got = append(got, reference.FieldKey)
	}
	want := []string{"active", "customer_id", "customer_id", "created_at", "status", "customer_id", "updated_at"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("field references=%v want %v", got, want)
	}
}
