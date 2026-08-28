package transactionmodel

import (
	"errors"
	"reflect"
	"testing"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestMutationPlanSnapshotsCommitAndBuildsDeterministicWriteSet(t *testing.T) {
	context := mutationPlanTestContext(t, map[string][]string{"order": {"status", "total"}})
	commit := RecordMutationCommit{
		Operation:       " update ",
		Object:          definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status", Config: map[string]any{"nested": map[string]any{"enabled": true}}}}},
		Record:          recordmodel.Record{ID: "order-1", Data: map[string]any{"total": "12.30", "status": "paid"}},
		Conditions:      map[string]any{"status": map[string]any{"eq": "draft"}},
		Audit:           &auditmodel.AuditEvent{After: map[string]any{"status": "paid"}},
		Audits:          []auditmodel.AuditEvent{{Metadata: map[string]any{"sequence": []any{map[string]any{"value": 1}}}}},
		Outbox:          []integrationmodel.IntegrationOutboxMessage{{Payload: map[string]any{"order": map[string]any{"id": "order-1"}}}},
		WorkflowIntents: []workflowmodel.WorkflowExecution{{Action: map[string]any{"type": "notify"}, Payload: map[string]any{"id": "order-1"}, Result: map[string]any{"queued": true}}},
	}
	plan, err := NewMutationPlan(context, commit, map[string]any{"status": "draft"})
	if err != nil {
		t.Fatal(err)
	}
	commit.Record.Data["status"] = "tampered"
	commit.Object.Fields[0].Config["nested"].(map[string]any)["enabled"] = false
	commit.Conditions["status"].(map[string]any)["eq"] = "tampered"
	commit.Outbox[0].Payload["order"].(map[string]any)["id"] = "tampered"
	got := plan.CanonicalCommit()
	if got.Operation != "update" || got.Record.Data["status"] != "paid" || got.Object.Fields[0].Config["nested"].(map[string]any)["enabled"] != true || got.Conditions["status"].(map[string]any)["eq"] != "draft" || got.Outbox[0].Payload["order"].(map[string]any)["id"] != "order-1" {
		t.Fatalf("plan did not preserve snapshot: %+v", got)
	}
	got.Record.Data["status"] = "exposed"
	if plan.CanonicalCommit().Record.Data["status"] != "paid" {
		t.Fatal("commit getter exposed mutable state")
	}
	writes := plan.WriteSet()
	if !reflect.DeepEqual(writes, []MutationWriteReference{{ObjectKey: "order", RecordID: "order-1", Fields: []string{"status", "total"}}}) {
		t.Fatalf("write set=%+v", writes)
	}
	writes[0].Fields[0] = "exposed"
	if plan.WriteSet()[0].Fields[0] != "status" {
		t.Fatal("write set getter exposed mutable state")
	}
}

func TestMutationPlanEnforcesEffectAuthorityIncludingDeleteWildcard(t *testing.T) {
	commit := RecordMutationCommit{Operation: "update", Object: definitionmodel.ObjectSchema{Key: "order"}, Record: recordmodel.Record{ID: "order-1", Data: map[string]any{"secret": true}}}
	_, err := NewMutationPlan(mutationPlanTestContext(t, map[string][]string{"order": {"status"}}), commit, nil)
	var typed *MutationPlanError
	if !errors.As(err, &typed) || typed.Code != "backend.mutation.effect_authority_denied" || typed.Field != "order.secret" {
		t.Fatalf("error=%#v", err)
	}
	deleteCommit := RecordMutationCommit{Operation: "delete", Object: definitionmodel.ObjectSchema{Key: "order"}, RecordID: "order-1"}
	plan, err := NewMutationPlan(mutationPlanTestContext(t, map[string][]string{"order": {"*"}}), deleteCommit, nil)
	if err != nil || !reflect.DeepEqual(plan.WriteSet()[0].Fields, []string{"*"}) {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestMutationPlanValidatesCanonicalCoordinates(t *testing.T) {
	context := mutationPlanTestContext(t, nil)
	valid := RecordMutationCommit{Operation: "create", Object: definitionmodel.ObjectSchema{Key: "order"}, Record: recordmodel.Record{ID: "order-1"}}
	for _, test := range []struct {
		name   string
		ctx    MutationContext
		commit RecordMutationCommit
		field  string
	}{
		{name: "operation", ctx: context, commit: func() RecordMutationCommit { value := valid; value.Operation = "merge"; return value }(), field: "operation"},
		{name: "object", ctx: context, commit: func() RecordMutationCommit { value := valid; value.Object.Key = ""; return value }(), field: "object_key"},
		{name: "record", ctx: context, commit: func() RecordMutationCommit { value := valid; value.Record.ID = ""; return value }(), field: "record_id"},
		{name: "context", ctx: MutationContext{}, commit: valid, field: "mutation_context"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewMutationPlan(test.ctx, test.commit, nil)
			var typed *MutationPlanError
			if !errors.As(err, &typed) || typed.Code != "backend.mutation.plan_invalid" || typed.Field != test.field {
				t.Fatalf("error=%#v", err)
			}
		})
	}
}

func TestMutationPlanValidatesPredicateCoordinatesOperatorsAndStableCodes(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status"}}}
	for _, test := range []struct {
		name      string
		predicate MutationPredicate
		field     string
	}{
		{name: "blank field", predicate: MutationPredicate{Field: "", Operator: "eq"}, field: ""},
		{name: "missing field", predicate: MutationPredicate{Field: "missing", Operator: "eq"}, field: "missing"},
		{name: "invalid operator", predicate: MutationPredicate{Field: "status", Operator: "between"}, field: "status.operator"},
		{name: "unstable error code", predicate: MutationPredicate{Field: "status", Operator: "eq", ErrorCode: "Order.Invalid"}, field: "status.error_code"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := mutationValidatePredicates(object, []MutationPredicate{test.predicate})
			var typed *MutationPlanError
			if !errors.As(err, &typed) || typed.Field != test.field || typed.Error() == "" {
				t.Fatalf("error=%#v", err)
			}
		})
	}
	valid := []MutationPredicate{
		{Field: "status", Operator: "eq", ErrorCode: "order.status_0-invalid"},
		{Field: "updated_at", Operator: "gte"},
	}
	commit := RecordMutationCommit{
		Operation: "update", Object: object, Record: recordmodel.Record{ID: "order-1"},
		Predicates: valid,
	}
	plan, err := NewMutationPlan(mutationPlanTestContext(t, nil), commit, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = plan.Context()
	_ = plan.Before()
	commit.Predicates = []MutationPredicate{{Field: "missing", Operator: "eq"}}
	if _, err := NewMutationPlan(mutationPlanTestContext(t, nil), commit, nil); err == nil {
		t.Fatal("invalid predicate reached mutation plan")
	}
	for value, want := range map[string]bool{
		"a": true, "z": true, "0": true, "9": true, ".": true, "_": true, "-": true,
		"A": false, "{": false, " ": false, "": false,
	} {
		if got := mutationStableCode(value); got != want {
			t.Fatalf("stable code %q=%t want=%t", value, got, want)
		}
	}
}

func TestMutationPlanCloneHelpersCoverNestedLocalizedAndStringMaps(t *testing.T) {
	object := definitionmodel.ObjectSchema{
		Key:  "order",
		I18n: map[string]map[string]string{"name": {"en-US": "Order"}},
		Fields: []definitionmodel.FieldSchema{{
			Key: "status", I18n: map[string]map[string]string{"label": {"zh-CN": "状态"}},
		}},
		Validations: []definitionmodel.ValidationSchema{{
			Key: "required", Fields: []string{"status"},
			I18n:   map[string]map[string]string{"message": {"en-US": "Required"}},
			Config: map[string]any{"labels": map[string]string{"draft": "Draft"}},
		}},
	}
	cloned := mutationCloneObjectSchema(object)
	cloned.I18n["name"]["en-US"] = "Changed"
	cloned.Validations[0].Fields[0] = "changed"
	cloned.Validations[0].I18n["message"]["en-US"] = "Changed"
	cloned.Validations[0].Config["labels"].(map[string]string)["draft"] = "Changed"
	if object.I18n["name"]["en-US"] != "Order" ||
		object.Validations[0].Fields[0] != "status" ||
		object.Validations[0].I18n["message"]["en-US"] != "Required" ||
		object.Validations[0].Config["labels"].(map[string]string)["draft"] != "Draft" {
		t.Fatal("nested object schema clone exposed source values")
	}
	if mutationCloneLocalizedText(nil) != nil {
		t.Fatal("nil localized text clone must stay nil")
	}
	if got := mutationPlanFields("create", map[string]any{" ": 1, "status": "draft"}, nil); !reflect.DeepEqual(got, []string{"status"}) {
		t.Fatalf("fields=%v", got)
	}
	if got := mutationPlanFields("update", map[string]any{"status": "draft"}, map[string]any{"status": "draft"}); len(got) != 0 {
		t.Fatalf("unchanged fields=%v", got)
	}
	values := []string{"a", "b"}
	clonedValues := mutationCloneValue(values).([]string)
	clonedValues[0] = "changed"
	if values[0] != "a" {
		t.Fatal("string slice clone exposed source")
	}
}

func TestMutationObjectWritePolicyAndRestoreOperation(t *testing.T) {
	for _, test := range []struct {
		object definitionmodel.ObjectSchema
		want   ObjectWritePolicy
	}{
		{object: definitionmodel.ObjectSchema{}, want: ObjectWritePolicyDirectCRUD},
		{object: definitionmodel.ObjectSchema{Config: map[string]any{"write_policy": 1}}, want: ObjectWritePolicyDirectCRUD},
		{object: definitionmodel.ObjectSchema{Config: map[string]any{"write_policy": "direct_crud"}}, want: ObjectWritePolicyDirectCRUD},
		{object: definitionmodel.ObjectSchema{Config: map[string]any{"write_policy": " action_only "}}, want: ObjectWritePolicyActionOnly},
	} {
		if got := MutationObjectWritePolicy(test.object); got != test.want {
			t.Fatalf("write policy=%q want=%q", got, test.want)
		}
	}
	commit := RecordMutationCommit{
		Operation: "restore", Object: definitionmodel.ObjectSchema{Key: "order"},
		Record: recordmodel.Record{ID: "order-1", Data: map[string]any{"status": "active"}},
	}
	if _, err := NewMutationPlan(mutationPlanTestContext(t, nil), commit, nil); err != nil {
		t.Fatal(err)
	}
}

func mutationPlanTestContext(t *testing.T, authority map[string][]string) MutationContext {
	t.Helper()
	context, err := NewMutationContext(MutationContextInput{WorkspaceID: "workspace-a", Source: MutationSourceHTTP, CorrelationID: "correlation-1", MetadataRevision: "revision-1", EffectAuthority: authority})
	if err != nil {
		t.Fatal(err)
	}
	return context
}
