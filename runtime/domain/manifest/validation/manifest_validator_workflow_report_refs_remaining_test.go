package validation

import (
	"strings"
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestValidateReportsCoversObjectSQLSourceAndFieldEdges(t *testing.T) {
	state := newValidationState(manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "customer_id"}}}},
		Reports: []reportmodel.ReportSchema{{Key: "missing-source", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
			SQL: "SELECT orders.missing AS missing FROM `order` orders LIMIT 10", SourceObjects: []string{"order"}, ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "missing", Type: "text", Kind: "dimension"}},
		}}},
	}, nil)
	state.validateReports()
	if diagnostics := state.errs.Error(); !strings.Contains(diagnostics, "backend.report.field_not_found") {
		t.Fatalf("missing ObjectSQL field diagnostic: %s", diagnostics)
	}
}
