package query

import (
	"reflect"
	"strings"
	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordContainsBindsLiteralAndKeepsAuthorizationConjunctive(t *testing.T) {
	filter := &recordmodel.RecordFilterExpression{Operator: "or", Children: []recordmodel.RecordFilterExpression{
		{Field: "customer", Operator: "contains", Value: " 50%_off~猫 ' "},
		{Field: "id", Operator: "contains", Value: "[abc]"},
	}}
	where, args, err := BuildTenantWhere(fuzzQueryStore{}, "workspace-a", recordmodel.RecordListQuery{
		AuthorizationMode: recordmodel.RecordQueryAuthorizationDeny, OwnerOrganizationScopeID: "store-a", FilterExpression: filter,
	})
	if err != nil || strings.Count(where, "ESCAPE '~'") != 2 || !strings.Contains(where, "1 = 0") ||
		!strings.Contains(where, `"workspace_id" = $1`) || !strings.Contains(where, `"owner_org_id" = $2`) ||
		!strings.Contains(where, `("customer" LIKE $3 ESCAPE '~' OR "id" LIKE $4 ESCAPE '~')`) ||
		strings.Contains(where, "50%") || !reflect.DeepEqual(args, []any{"workspace-a", "store-a", "% 50~%~_off~~猫 ' %", "%[abc]%"}) {
		t.Fatalf("where=%s args=%#v error=%v", where, args, err)
	}
	if _, err := recordFilterPredicate(recordmodel.RecordFilterExpression{Field: "name", Operator: "contains", Value: 1}, 0); err == nil {
		t.Fatal("unvalidated non-text literal reached SQL")
	}
}
