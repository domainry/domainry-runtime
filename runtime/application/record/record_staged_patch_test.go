package record

import (
	"context"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestConditionalUpdateOfStagedParentRetainsAuthorizationAndVersion(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "parent", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	parent := recordmodel.Record{ID: "parent-1", UpdatedAt: "2026-09-11T00:00:00Z", Data: map[string]any{"name": "before"}}
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	for _, tc := range []struct {
		name, expected, code  string
		staged, access, write bool
	}{
		{"staged parent", parent.UpdatedAt, "", true, true, true},
		{"missing parent", parent.UpdatedAt, "backend.record.outside_scope", false, true, true},
		{"scope denied", parent.UpdatedAt, "backend.record.outside_scope", true, false, true},
		{"write denied", parent.UpdatedAt, "backend.record.owner_write_denied", true, true, false},
		{"stale version", "2026-09-10T00:00:00Z", "backend.record.version_conflict", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := NewRecordUpdateApplicationService(RecordUpdateDependencies{
				Repository: &updateRepositoryProbe{},
				ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
					return object, nil
				},
				LoadTargetForAction: func(context.Context, string, definitionmodel.ObjectSchema, string, *recordmodel.RecordScopeExpression) (recordmodel.Record, bool, error) {
					return recordmodel.Record{}, false, nil
				},
				CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
					return tc.access
				},
				CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return tc.write },
				Now:      func() time.Time { return time.Date(2026, 9, 11, 0, 1, 0, 0, time.UTC) },
			})
			ctx := t.Context()
			if tc.staged {
				ctx = recordservice.RecordWithPlannedRelations(ctx, map[string]map[string]recordmodel.Record{object.Key: {parent.ID: parent}})
			}
			plan, row, err := service.PlanConditionalUpdateMutation(ctx, object.Key, parent.ID, transactionmodel.ConditionalUpdateInput{
				Patch:      map[string]any{"name": "after"},
				Predicates: []transactionmodel.MutationPredicate{{Field: "updated_at", Operator: "eq", Value: tc.expected, ErrorCode: "backend.record.version_conflict"}},
			}, principal)
			if (tc.code == "" && err != nil) || (tc.code != "" && apperror.CodeOf(err) != tc.code) {
				t.Fatalf("code=%q err=%v, want %q", apperror.CodeOf(err), err, tc.code)
			}
			if tc.code != "" {
				return
			}
			commit := plan.CanonicalCommit()
			if row.Data["name"] != "after" || row.UpdatedAt == parent.UpdatedAt || commit.Optimistic.ExpectedUpdatedAt != parent.UpdatedAt || len(commit.Predicates) != 1 {
				t.Fatalf("conditional receipt or preconditions lost: row=%#v commit=%#v", row, commit)
			}
		})
	}
}

func TestPaymentTextPatchPreservesEmptyNullAndOmittedValues(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "payment_setting", Fields: []definitionmodel.FieldSchema{
		{Key: "bank_name", Type: "text"}, {Key: "account_number", Type: "text"}, {Key: "branch_name", Type: "text"}, {Key: "client_code", Type: "text"},
	}}
	before := recordmodel.Record{ID: "setting-1", UpdatedAt: "2026-09-11T00:00:00Z", Data: map[string]any{"bank_name": nil, "account_number": nil, "branch_name": "branch", "client_code": "keep"}}
	service := NewRecordUpdateApplicationService(RecordUpdateDependencies{
		Repository: &updateRepositoryProbe{found: true, record: before},
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		CanWrite:  func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
	})
	principal := recordFullAccessPrincipal(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	plan, row, err := service.PlanConditionalUpdateMutation(t.Context(), object.Key, before.ID, transactionmodel.ConditionalUpdateInput{Patch: map[string]any{"bank_name": "", "account_number": "", "branch_name": nil}}, principal)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("updated_at advanced=%v; bank_name=%#v account_number=%#v branch_name=%#v client_code=%#v", row.UpdatedAt != before.UpdatedAt, row.Data["bank_name"], row.Data["account_number"], row.Data["branch_name"], row.Data["client_code"])
	for _, receipt := range []recordmodel.Record{row, plan.CanonicalCommit().Record} {
		if receipt.UpdatedAt == before.UpdatedAt || receipt.Data["bank_name"] != "" || receipt.Data["account_number"] != "" || receipt.Data["branch_name"] != nil || receipt.Data["client_code"] != "keep" {
			t.Fatalf("patch intent lost: %#v", receipt)
		}
	}
}
