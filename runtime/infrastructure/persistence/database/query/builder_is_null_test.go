package query

import (
	"strings"
	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestTenantWhereCompilesIsNullFilter(t *testing.T) {
	where, args, err := BuildTenantWhere(fuzzQueryStore{}, "workspace-a", recordmodel.RecordListQuery{
		FilterExpression: &recordmodel.RecordFilterExpression{Operator: "is_null", Field: "deleted_at"},
	})
	if err != nil || !strings.Contains(where, `"deleted_at" IS NULL`) || len(args) != 1 {
		t.Fatalf("where=%s args=%#v err=%v", where, args, err)
	}
}
