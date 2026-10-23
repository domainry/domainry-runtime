package record

import (
	"strings"
	"testing"

	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordCursorNullOrderingUsesDialectNativeORMOrder(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	for _, driver := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			store := openRuntimeStore(t)
			if err := store.SetEngineForTesting(driver); err != nil {
				t.Fatal(err)
			}
			q := recordmodel.RecordListQuery{StableNullsLast: true, Sort: []recordmodel.RecordSortRule{{Field: "name", Direction: "desc"}, {Field: "id", Direction: "asc"}}}
			statement, args, err := query.NewWorkspaceSelectBuilder(store.SQLRenderer, object.Key, "workspace").Columns("id").OrderBy(recordLocalizedOrders("workspace", object, q)...).Build()
			if err != nil {
				t.Fatalf("statement=%s args=%v err=%v", statement, args, err)
			}
			if strings.Contains(statement, "CASE WHEN") || len(args) != 1 {
				t.Fatalf("null ordering must not add bound parameters: statement=%s args=%v", statement, args)
			}
			if driver == "mysql" {
				if !strings.Contains(statement, "ISNULL(") || strings.Contains(statement, "NULLS LAST") {
					t.Fatalf("statement=%s args=%v", statement, args)
				}
			} else if strings.Count(statement, "NULLS LAST") != 2 {
				t.Fatalf("statement=%s args=%v", statement, args)
			}
		})
	}
}
