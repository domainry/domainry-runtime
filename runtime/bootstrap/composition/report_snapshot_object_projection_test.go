package composition

import (
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

func TestReportSnapshotObjectKeySetIsAClosedRuntimeProjection(t *testing.T) {
	reports := []reportmodel.ReportSchema{
		{Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "snapshot_order", SourceType: "snapshot"}}},
		{Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: " ", SourceType: "snapshot"}}},
	}
	keys := reportSnapshotObjectKeySet(reports)
	if len(keys) != 1 {
		t.Fatalf("snapshot object keys=%#v", keys)
	}
	if _, ok := keys["snapshot_order"]; !ok {
		t.Fatalf("snapshot object keys=%#v", keys)
	}
}
