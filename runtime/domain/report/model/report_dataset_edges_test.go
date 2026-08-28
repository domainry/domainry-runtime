package reportmodel

import "testing"

func TestReportDatasetProjectionHelpersAndPlanError(t *testing.T) {
	dataset := ReportDatasetSchema{
		Source: ReportDatasetSource{ObjectKey: "", Alias: ""},
		Joins: []ReportDatasetJoin{
			{ObjectKey: "", Alias: ""},
			{ObjectKey: "order", Alias: "orders", SourceType: "records"},
			{ObjectKey: "snapshot_order", Alias: "snapshot_orders", SourceType: "snapshot"},
		},
	}
	if keys := ReportDatasetObjectKeys(dataset); len(keys) != 2 {
		t.Fatalf("object keys=%v", keys)
	}
	if keys := ReportDatasetSnapshotObjectKeys(dataset); len(keys) != 1 || keys[0] != "snapshot_order" {
		t.Fatalf("snapshot keys=%v", keys)
	}
	aliases := ReportDatasetAliasObjects(dataset)
	if len(aliases) != 2 || aliases["orders"] != "order" || ReportDatasetFieldObjectKey(dataset, ReportDatasetField{SourceAlias: "orders"}) != "order" {
		t.Fatalf("aliases=%v", aliases)
	}

	dataset.Source = ReportDatasetSource{ObjectKey: "source_snapshot", Alias: "source", SourceType: "snapshot"}
	dataset.Joins = append(dataset.Joins, ReportDatasetJoin{ObjectKey: "", Alias: "blank_snapshot", SourceType: "snapshot"})
	if keys := ReportDatasetSnapshotObjectKeys(dataset); len(keys) != 2 {
		t.Fatalf("source snapshot keys=%v", keys)
	}
	if keys := ReportDatasetSnapshotObjectKeys(ReportDatasetSchema{Source: ReportDatasetSource{SourceType: "snapshot"}}); len(keys) != 0 {
		t.Fatalf("blank source snapshot keys=%v", keys)
	}

	var nilPlanErr *ReportDatasetPlanError
	if nilPlanErr.Error() != "" || nilPlanErr.ErrorCode() != "" {
		t.Fatal("nil plan error should have an empty contract")
	}
	planErr := &ReportDatasetPlanError{Code: "backend.report.invalid", Path: "dataset"}
	if planErr.Error() != "backend.report.invalid at dataset" || planErr.ErrorCode() != "backend.report.invalid" {
		t.Fatalf("plan error=%v code=%s", planErr, planErr.ErrorCode())
	}
}
