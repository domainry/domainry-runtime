package query

import (
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

func TestMeasureOnlyBoundedDatasetGetsDeterministicKeysetOrder(t *testing.T) {
	plan := reportmodel.ReportObjectSQLPlan{Projections: []reportmodel.ReportObjectSQLProjection{{Alias: "customer_count"}}}
	ensureReportDatasetSQLStableOrder(&plan)
	if len(plan.OrderBy) != 1 || plan.OrderBy[0].Expression.Kind != "result" || plan.OrderBy[0].Expression.Alias != "customer_count" || plan.OrderBy[0].Direction != "asc" {
		t.Fatalf("measure-only order=%#v", plan.OrderBy)
	}

	explicit := reportmodel.ReportObjectSQLPlan{Projections: plan.Projections, OrderBy: []reportmodel.ReportObjectSQLOrder{{Direction: "desc"}}}
	ensureReportDatasetSQLStableOrder(&explicit)
	if len(explicit.OrderBy) != 1 || explicit.OrderBy[0].Direction != "desc" {
		t.Fatalf("explicit order changed=%#v", explicit.OrderBy)
	}
}
