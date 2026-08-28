package validation

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func relatedAggregateObject(validation definitionmodel.ValidationSchema, extraFields ...definitionmodel.FieldSchema) definitionmodel.ObjectSchema {
	fields := []definitionmodel.FieldSchema{
		{Key: "payment_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "payment"}},
		{Key: "amount", Type: "number"},
		{Key: "paid_amount", Type: "number"},
		{Key: "status", Type: "text"},
	}
	fields = append(fields, extraFields...)
	return definitionmodel.ObjectSchema{Key: "refund", Fields: fields, Validations: []definitionmodel.ValidationSchema{validation}}
}

func relatedAggregateValidation(config map[string]any) definitionmodel.ValidationSchema {
	return definitionmodel.ValidationSchema{Key: "refund_limit", Type: "related_aggregate_invariant", Config: config}
}

func TestRecordRelatedAggregateInvariantsRejectEveryMalformedContractEdge(t *testing.T) {
	validConfig := func() map[string]any {
		return map[string]any{
			"relation_field": "payment_id",
			"aggregate":      "sum",
			"value_field":    "amount",
			"limit_field":    "paid_amount",
			"operator":       "lte",
		}
	}
	run := func(t *testing.T, object definitionmodel.ObjectSchema) {
		t.Helper()
		if _, err := RecordRelatedAggregateInvariants(object); err == nil {
			t.Fatal("expected malformed related aggregate invariant to be rejected")
		}
	}

	t.Run("relation target blank", func(t *testing.T) {
		config := validConfig()
		object := relatedAggregateObject(relatedAggregateValidation(config))
		object.Fields[0].Validation.Target = " "
		run(t, object)
	})
	t.Run("aggregate invalid", func(t *testing.T) {
		config := validConfig()
		config["aggregate"] = "average"
		run(t, relatedAggregateObject(relatedAggregateValidation(config)))
	})
	t.Run("sum value field missing", func(t *testing.T) {
		config := validConfig()
		config["value_field"] = "missing"
		run(t, relatedAggregateObject(relatedAggregateValidation(config)))
	})
	t.Run("sum value field blank", func(t *testing.T) {
		config := validConfig()
		config["value_field"] = ""
		run(t, relatedAggregateObject(relatedAggregateValidation(config), definitionmodel.FieldSchema{Key: ""}))
	})
	t.Run("limit field blank", func(t *testing.T) {
		config := validConfig()
		config["limit_field"] = ""
		run(t, relatedAggregateObject(relatedAggregateValidation(config)))
	})
	t.Run("operator invalid", func(t *testing.T) {
		config := validConfig()
		config["operator"] = "between"
		run(t, relatedAggregateObject(relatedAggregateValidation(config)))
	})
	t.Run("status field missing", func(t *testing.T) {
		config := validConfig()
		config["status_field"] = "missing"
		config["included_statuses"] = []string{"approved"}
		run(t, relatedAggregateObject(relatedAggregateValidation(config)))
	})
	t.Run("status field blank", func(t *testing.T) {
		config := validConfig()
		config["status_field"] = ""
		config["included_statuses"] = []string{"approved"}
		run(t, relatedAggregateObject(relatedAggregateValidation(config), definitionmodel.FieldSchema{Key: ""}))
	})
}

func TestRecordRelatedAggregateInvariantsAcceptCountAndSumContracts(t *testing.T) {
	for _, config := range []map[string]any{
		{"relation_field": "payment_id", "aggregate": "count", "limit_field": "paid_amount", "operator": "lt"},
		{"relation_field": "payment_id", "aggregate": "sum", "value_field": "amount", "limit_field": "paid_amount", "operator": "gte", "status_field": "status", "included_statuses": []string{"approved"}},
	} {
		invariants, err := RecordRelatedAggregateInvariants(relatedAggregateObject(relatedAggregateValidation(config)))
		if err != nil || len(invariants) != 1 {
			t.Fatalf("invariants=%#v err=%v", invariants, err)
		}
	}
	object := relatedAggregateObject(relatedAggregateValidation(map[string]any{
		"relation_field": "payment_id", "aggregate": "count", "limit_field": "paid_amount", "operator": "eq",
	}))
	object.Validations = append([]definitionmodel.ValidationSchema{{Type: "required"}}, object.Validations...)
	if invariants, err := RecordRelatedAggregateInvariants(object); err != nil || len(invariants) != 1 {
		t.Fatalf("unrelated validation was not skipped: invariants=%#v err=%v", invariants, err)
	}
	missingRelation := relatedAggregateValidation(map[string]any{
		"relation_field": "missing", "aggregate": "count", "limit_field": "paid_amount", "operator": "eq",
	})
	if _, err := RecordRelatedAggregateInvariants(relatedAggregateObject(missingRelation)); err == nil {
		t.Fatal("expected missing relation field error")
	}
}

func TestRecordRelatedAggregateCandidatePolicyAndValueEdges(t *testing.T) {
	base := RecordRelatedAggregateInvariant{
		Key: "refund_limit", RelationField: "payment_id", Aggregate: "sum", ValueField: "amount",
		StatusField: "status", IncludedStatuses: []string{"approved"},
		Definition: relatedAggregateValidation(map[string]any{}),
	}
	cases := []struct {
		name      string
		policy    RecordRelatedAggregateInvariant
		data      map[string]any
		operation string
		want      bool
		wantErr   bool
	}{
		{
			name: "non blocking warning",
			policy: func() RecordRelatedAggregateInvariant {
				value := base
				value.Definition.Severity = "warning"
				return value
			}(),
			data: map[string]any{"payment_id": "payment-1", "amount": 1, "status": "approved"}, operation: "create",
		},
		{
			name: "operation excluded",
			policy: func() RecordRelatedAggregateInvariant {
				value := base
				value.Definition.Config = map[string]any{"operations": []string{"create"}}
				return value
			}(),
			data: map[string]any{"payment_id": "payment-1", "amount": 1, "status": "approved"}, operation: "update",
		},
		{
			name: "condition mismatch",
			policy: func() RecordRelatedAggregateInvariant {
				value := base
				value.Definition.Config = map[string]any{"when_field": "kind", "value": "special"}
				return value
			}(),
			data: map[string]any{"payment_id": "payment-1", "amount": 1, "status": "approved", "kind": "ordinary"}, operation: "create",
		},
		{
			name:   "value missing",
			policy: base, data: map[string]any{"payment_id": "payment-1", "status": "approved"}, operation: "create", wantErr: true,
		},
		{
			name:   "included status mismatch",
			policy: base, data: map[string]any{"payment_id": "payment-1", "amount": 1, "status": "rejected"}, operation: "create",
		},
		{
			name:   "relation missing",
			policy: base, data: map[string]any{"amount": 1, "status": "approved"}, operation: "create", wantErr: true,
		},
		{
			name: "count without status filter",
			policy: func() RecordRelatedAggregateInvariant {
				value := base
				value.Aggregate = "count"
				value.IncludedStatuses = nil
				return value
			}(),
			data: map[string]any{"payment_id": "payment-1"}, operation: "create", want: true,
		},
		{
			name:   "candidate",
			policy: base, data: map[string]any{"payment_id": "payment-1", "amount": 1, "status": "approved"}, operation: "create", want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RecordRelatedAggregateCandidate(tc.policy, tc.data, tc.operation)
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("candidate=%t err=%v", got, err)
			}
		})
	}
}
