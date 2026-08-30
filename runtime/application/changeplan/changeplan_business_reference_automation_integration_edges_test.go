package changeplan

import (
	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func TestReportIntegrationReferencesIncludeRangePrivacyAndAnalysisFields(t *testing.T) {
	field := func(key string) *reportmodel.ReportDatasetField {
		return &reportmodel.ReportDatasetField{SourceAlias: "orders", FieldKey: key}
	}
	builder := NewReferenceGraphBuilder()
	AddReportIntegrationReferences(builder, ReferenceSchema{
		Reports: []reportmodel.ReportSchema{{
			Key: "order-cycle-time",
			Dataset: reportmodel.ReportDatasetSchema{
				Source:  reportmodel.ReportDatasetSource{ObjectKey: "order", Alias: "orders"},
				Filters: []reportmodel.ReportDatasetFilter{{Field: *field("status")}},
				Measures: []reportmodel.ReportDatasetMeasure{
					{
						Key:        "cycle-time",
						StartField: field("started_at"),
						EndField:   field("completed_at"),
					},
					{
						Key:   "total",
						Field: field("amount"),
					},
				},
				Privacy: &reportmodel.ReportDatasetPrivacy{
					EntityField: *field("customer_id"),
				},
				Analyses: []reportmodel.ReportDatasetAnalysis{{
					Key:         "conversion",
					EntityField: *field("customer_id"),
					TimeField:   *field("created_at"),
					EventField:  field("status"),
				}, {
					Key:         "retention",
					EntityField: *field("customer_id"),
					TimeField:   *field("created_at"),
				}},
			},
		}},
	})

	want := map[string]bool{
		"measures_start_field:order.started_at":   false,
		"measures_end_field:order.completed_at":   false,
		"measures_field:order.amount":             false,
		"filters_field:order.status":              false,
		"privacy_entity_field:order.customer_id":  false,
		"analyzes_entity_field:order.customer_id": false,
		"analyzes_time_field:order.created_at":    false,
		"analyzes_event_field:order.status":       false,
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
