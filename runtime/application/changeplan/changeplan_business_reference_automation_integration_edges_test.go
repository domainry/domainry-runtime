package changeplan

import (
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestReportIntegrationReferencesIncludeObjectSQLFields(t *testing.T) {
	builder := NewReferenceGraphBuilder()
	AddReportIntegrationReferences(builder, ReferenceSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}, {Key: "amount", Type: "decimal"}}}},
		Reports: []reportmodel.ReportSchema{{
			Key: "order-cycle-time",
			ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
				SQL:           "SELECT orders.status AS status, SUM(orders.amount) AS amount FROM `order` orders GROUP BY orders.status LIMIT 100",
				SourceObjects: []string{"order"},
				ResultSchema:  []reportmodel.ReportResultColumnSchema{{Key: "status", Type: "text", Kind: "dimension"}, {Key: "amount", Type: "decimal", Kind: "measure"}},
			},
		}},
	})

	want := map[string]bool{
		"reads_field:order.status": false,
		"reads_field:order.amount": false,
	}
	for _, edge := range builder.Graph().Edges {
		key := edge.Kind + ":" + edge.ToKey
		if _, exists := want[key]; exists {
			want[key] = true
		}
	}
	for edge, found := range want {
		if !found {
			t.Errorf("missing report reference %s", edge)
		}
	}
}

func TestSchedulerReferencesTreatSnapshotRefreshAsReportTarget(t *testing.T) {
	runtime := &changePlanReferenceRuntimeFake{
		records: map[string][]recordmodel.Record{
			"scheduler": {{
				ID: "refresh-orders",
				Data: map[string]any{
					"name":        "Refresh orders",
					"target_type": "report_snapshot_refresh",
					"target_key":  "orders",
				},
			}, {
				ID: "run-approval",
				Data: map[string]any{
					"name":        "Run approval",
					"target_type": "workflow",
					"target_key":  "scheduled:approval",
				},
			}},
		},
		recordErrors: map[string]error{},
	}
	service := NewChangePlanReferenceApplicationService(nil, runtime, nil)
	builder := NewReferenceGraphBuilder()
	if err := service.addSchedulerReferences(t.Context(), builder, changePlanAdmin()); err != nil {
		t.Fatal(err)
	}
	graph := builder.Graph()
	for _, edge := range graph.Edges {
		if edge.FromType == "scheduler" && edge.FromKey == "refresh-orders" &&
			edge.ToType == "report" && edge.ToKey == "orders" && edge.Kind == "schedules_target" {
			return
		}
	}
	t.Fatalf("snapshot refresh report edge missing: %+v", graph.Edges)
}
