package record

import (
	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

func TestCandidateScopeMatchesRelationToRecordPlannedInSameAction(t *testing.T) {
	order := recordmodel.Record{
		RecordID: "repair-1",
		Data: map[string]any{
			"reporter_identity_user_id": "reporter-1",
		},
	}
	ctx := recordservice.RecordWithPlannedRelations(t.Context(), map[string]map[string]recordmodel.Record{
		"repair_order": {order.RecordID: order},
	})
	history := recordmodel.Record{
		RecordID: "history-1",
		Data: map[string]any{
			"repair_order_id": order.RecordID,
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

func TestCandidateScopeMatchesRuntimeOwnershipOfPlannedParent(t *testing.T) {
	parent := recordmodel.Record{RecordID: "case-new", WorkspaceID: "workspace-a", OwnerOrgID: "support-a", OwnerUserID: "ingress-service", Data: map[string]any{"title": "Customer issue", "owner_org_id": "support-b", "owner_user_id": "other-service"}}
	ctx := recordservice.RecordWithPlannedRelations(t.Context(), map[string]map[string]recordmodel.Record{"support_case": {parent.RecordID: parent}})
	child := recordmodel.Record{RecordID: "event-new", WorkspaceID: "workspace-a", Data: map[string]any{"support_case": parent.RecordID}}
	for _, test := range []struct{ field, allowed, denied string }{
		{field: "owner_org_id", allowed: "support-a", denied: "support-b"},
		{field: "owner_user_id", allowed: "ingress-service", denied: "other-service"},
	} {
		t.Run(test.field, func(t *testing.T) {
			expression := recordmodel.RecordScopeExpression{Operator: "eq", Path: []recordmodel.RecordScopePathSegment{{Direction: "forward", RelationFieldKey: "support_case", TargetObjectKey: "support_case"}}, FieldKey: test.field, Values: []string{test.allowed}}
			matched, err := (RecordStore{}).CandidateScopeMatches(ctx, "workspace-a", child, expression)
			if err != nil || !matched {
				t.Fatalf("same-scope planned parent rejected: matched=%v error=%v", matched, err)
			}
			expression.Values = []string{test.denied}
			matched, err = (RecordStore{}).CandidateScopeMatches(ctx, "workspace-a", child, expression)
			if err != nil || matched {
				t.Fatalf("other-scope planned parent admitted: matched=%v error=%v", matched, err)
			}
		})
	}
}
