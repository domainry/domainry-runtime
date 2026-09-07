package action

import (
	"context"
	"reflect"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestBusinessActionQueryPreservesTypedFilterSortAndProjection(t *testing.T) {
	var captured recordmodel.RecordListQuery
	var capturedPrincipal principalmodel.Principal
	original := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{
		Key: "member", Permissions: []string{"class_booking.book"},
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "class_booking", Scope: "owner", Write: true}},
	},
	)
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{ListRecords: func(_ context.Context, objectKey string, query recordmodel.RecordListQuery, principal principalmodel.Principal) (recordmodel.RecordPageResult, error) {
			if objectKey != "class_booking" {
				t.Fatalf("object=%q", objectKey)
			}
			captured = query
			capturedPrincipal = principal
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "booking-1", Data: map[string]any{"status": "booked"}}}, Total: 51}, nil
		}},
		invocation: actionmodel.ActionInvocation{Principal: original},
		action: definitionmodel.ActionSchema{
			Key: "class_booking.book", ObjectKey: "class_booking",
			EffectSet: &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{{ObjectKey: "class_booking"}}},
		},
	}
	result, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{
		Operation: runtimeext.QueryList, ObjectKey: "class_booking",
		Filters: []runtimeext.Filter{
			{Field: "status", Operator: "eq", Value: "booked"},
			{Field: "name", Operator: "contains", Value: " 50%_off~猫 "},
			{Operator: "or", Children: []runtimeext.Filter{
				{Field: "member_id", Operator: "eq", Value: "member-1"},
				{Operator: "not", Children: []runtimeext.Filter{{Field: "member_id", Operator: "eq", Value: "member-2"}}},
			}},
		},
		Sorts:      []runtimeext.Sort{{Field: "created_at", Direction: "DESC"}},
		Projection: []string{"status", "member_id"}, Limit: 25, AfterID: "booking-0",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantFilter := &recordmodel.RecordFilterExpression{Operator: "and", Children: []recordmodel.RecordFilterExpression{
		{Operator: "eq", Field: "status", Value: "booked"},
		{Operator: "contains", Field: "name", Value: " 50%_off~猫 "},
		{Operator: "or", Children: []recordmodel.RecordFilterExpression{
			{Operator: "eq", Field: "member_id", Value: "member-1"},
			{Operator: "not", Children: []recordmodel.RecordFilterExpression{{Operator: "eq", Field: "member_id", Value: "member-2"}}},
		}},
	}}
	if captured.Page != 1 || captured.PageSize != 25 || captured.AfterID != "booking-0" || !reflect.DeepEqual(captured.FilterExpression, wantFilter) ||
		!reflect.DeepEqual(captured.Sort, []recordmodel.RecordSortRule{{Field: "created_at", Direction: "desc"}}) ||
		!reflect.DeepEqual(captured.SelectFields, []string{"status", "member_id"}) {
		t.Fatalf("captured query=%+v", captured)
	}
	if len(result.Records) != 1 || result.Records[0].ID != "booking-1" || result.Count != 51 || !result.Exists {
		t.Fatalf("result=%+v", result)
	}
	hasRead := false
	for _, permission := range capturedPrincipal.PermissionKeys() {
		hasRead = hasRead || permission == "class_booking.read"
	}
	capturedRole := accessfixture.Of(capturedPrincipal)
	if !hasRead ||
		len(capturedRole.DataPolicies) != 1 || capturedRole.DataPolicies[0].Scope != "owner" ||
		len(original.PermissionKeys()) != 1 {
		t.Fatalf("authorized=%#v original=%#v", capturedPrincipal, original)
	}
}

func TestBusinessActionQueryReadsCanonicalRecordCreatedEarlierInSameUnitOfWork(t *testing.T) {
	created := recordmodel.Record{
		ID: "employee-profile-1", OwnerOrgID: "store-north", UpdatedAt: "2026-09-07T00:00:00Z",
		Data: map[string]any{"identity_user_id": "identity-user-1", "employment_status": "active"},
	}
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{GetRecord: func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
			t.Fatal("staged create fell through to the physical repository")
			return recordmodel.Record{}, nil
		}},
		invocation: actionmodel.ActionInvocation{Principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}},
		action: definitionmodel.ActionSchema{
			Key: "employee.create", ObjectKey: "employee_profile",
			EffectSet: &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{{ObjectKey: "employee_profile"}}},
		},
		unitOfWork: newActionTestUnitOfWork(),
		mutatedRecords: map[string]recordmodel.Record{
			"employee_profile\x00employee-profile-1": created,
		},
		created: []actionmodel.ActionObjectRecordRef{{ObjectKey: "employee_profile", RecordID: "employee-profile-1"}},
	}

	result, err := execution.QueryRecords(t.Context(), runtimeext.RecordQuery{
		Operation: runtimeext.QueryGet, ObjectKey: "employee_profile", RecordID: "employee-profile-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Exists || result.Count != 1 || len(result.Records) != 1 || result.Records[0].ID != created.ID ||
		result.Records[0].UpdatedAt != created.UpdatedAt || result.Records[0].Fields["identity_user_id"] != "identity-user-1" {
		t.Fatalf("result=%+v", result)
	}
	if observed, ok := execution.observedRecords["employee_profile\x00employee-profile-1"]; !ok || observed.ID != created.ID {
		t.Fatalf("observed=%#v", execution.observedRecords)
	}
}

func TestBusinessActionQueryRejectsMalformedPublicAST(t *testing.T) {
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{ListRecords: func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
			t.Fatal("invalid query reached ListRecords")
			return recordmodel.RecordPageResult{}, nil
		}},
		action: definitionmodel.ActionSchema{EffectSet: &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{{ObjectKey: "member"}}}},
	}
	tests := []struct {
		name  string
		query runtimeext.RecordQuery
		code  string
	}{
		{name: "operator", query: runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: "member", Filters: []runtimeext.Filter{{Field: "name", Operator: "regex", Value: "x"}}}, code: "backend.action.query_operator_unsupported"},
		{name: "contains numeric", query: runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: "member", Filters: []runtimeext.Filter{{Field: "name", Operator: "contains", Value: 1}}}, code: "backend.action.query_filter_invalid"},
		{name: "contains missing field", query: runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: "member", Filters: []runtimeext.Filter{{Operator: "contains", Value: "x"}}}, code: "backend.action.query_filter_invalid"},
		{name: "contains extra values", query: runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: "member", Filters: []runtimeext.Filter{{Field: "name", Operator: "contains", Value: "x", Values: []any{"y"}}}}, code: "backend.action.query_filter_invalid"},
		{name: "contains children", query: runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: "member", Filters: []runtimeext.Filter{{Field: "name", Operator: "contains", Value: "x", Children: []runtimeext.Filter{{Field: "name", Value: "y"}}}}}, code: "backend.action.query_filter_invalid"},
		{name: "in without values", query: runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: "member", Filters: []runtimeext.Filter{{Field: "id", Operator: "in"}}}, code: "backend.action.query_filter_invalid"},
		{name: "or without children", query: runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: "member", Filters: []runtimeext.Filter{{Operator: "or"}}}, code: "backend.action.query_filter_invalid"},
		{name: "sort direction", query: runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: "member", Sorts: []runtimeext.Sort{{Field: "name", Direction: "sideways"}}}, code: "backend.action.query_sort_invalid"},
		{name: "empty projection", query: runtimeext.RecordQuery{Operation: runtimeext.QueryList, ObjectKey: "member", Projection: []string{" "}}, code: "backend.action.query_projection_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := execution.QueryRecords(t.Context(), test.query)
			if code := apperror.CodeOf(err); code != test.code {
				t.Fatalf("code=%q err=%v", code, err)
			}
		})
	}
}
