package record

import (
	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

func TestCandidateScopeMatchesRelationToRecordPlannedInSameAction(t *testing.T) {
	order := recordmodel.Record{
		ID: "repair-1",
		Data: map[string]any{
			"reporter_identity_user_id": "reporter-1",
		},
	}
	ctx := recordservice.RecordWithPlannedRelations(t.Context(), map[string]map[string]recordmodel.Record{
		"repair_order": {order.ID: order},
	})
	history := recordmodel.Record{
		ID: "history-1",
		Data: map[string]any{
			"repair_order_id": order.ID,
		},
	}
	expression := recordmodel.RecordScopeExpression{
		Operator: "eq",
		Path: []recordmodel.RecordScopePathSegment{{
			Direction:        "forward",
			RelationFieldKey: "repair_order_id",
			TargetObjectKey:  "repair_order",
		}},
		FieldKey: "reporter_identity_user_id",
		Values:   []string{"reporter-1"},
	}

	matched, err := (RecordStore{}).CandidateScopeMatches(ctx, "workspace-a", history, expression)
	if err != nil || !matched {
		t.Fatalf("planned relation scope matched=%v err=%v", matched, err)
	}
	expression.Values = []string{"another-reporter"}
	matched, err = (RecordStore{}).CandidateScopeMatches(ctx, "workspace-a", history, expression)
	if err != nil || matched {
		t.Fatalf("mismatched planned relation scope matched=%v err=%v", matched, err)
	}
}
