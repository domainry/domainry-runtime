package record

import (
	"strings"
	"testing"

	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordCursorNullOrderingUsesPortableORMExpressions(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	for _, driver := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			store := openRuntimeStore(t)
			if err := store.SetEngineForTesting(driver); err != nil {
				t.Fatal(err)
			}
			q := recordmodel.RecordListQuery{StableNullsLast: true, Sort: []recordmodel.RecordSortRule{{Field: "name", Direction: "desc"}, {Field: "id", Direction: "asc"}}}
			statement, args, err := query.NewWorkspaceSelectBuilder(store.SQLRenderer, object.Key, "workspace").Columns("id").OrderBy(recordLocalizedOrders("workspace", object, q)...).Build()
			if err != nil || !strings.Contains(statement, "CASE WHEN") || !strings.Contains(statement, " IS NULL") || strings.Contains(statement, "NULLS LAST") {
				t.Fatalf("statement=%s args=%v err=%v", statement, args, err)
			}
		})
	}
}
