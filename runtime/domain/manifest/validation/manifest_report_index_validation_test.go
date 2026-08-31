package validation

import (
	"strings"
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestReportPublicationBlocksMissingDatasetIndex(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "event", Fields: []definitionmodel.FieldSchema{{Key: "occurred_at", Type: "datetime"}}}},
		Reports: []reportmodel.ReportSchema{{Key: "event.timeline", Dataset: reportmodel.ReportDatasetSchema{
			Source:     reportmodel.ReportDatasetSource{ObjectKey: "event", Alias: "events"},
			Dimensions: []reportmodel.ReportDatasetDimension{{Key: "day", Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "occurred_at"}, TimeGrain: "day"}},
		}}},
	}
	state := newValidationState(manifest, nil)
	state.validateReports()
	message := ValidationErrors(state.errs).Error()
	if !strings.Contains(message, "backend.report.required_index_missing") || !strings.Contains(message, "recommended_index=workspace_id,occurred_at") {
		t.Fatalf("diagnostics=%s", message)
	}
	manifest.Objects[0].Fields[0].Config = map[string]any{"indexed": true}
	valid := newValidationState(manifest, nil)
	valid.validateReports()
	if len(valid.errs) != 0 {
		t.Fatalf("indexed report rejected: %v", valid.errs)
	}
}
