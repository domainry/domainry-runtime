package integrationtest

import (
	"testing"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func planToProduceCreateTable(t *testing.T, store *persistence.RuntimeStore, object definitionmodel.ObjectSchema) {
	t.Helper()
	columns := []ormbuilder.SchemaColumn{
		ormbuilder.DefineColumn("workspace_id", ormbuilder.TextKeyType(191)).NotNull(),
		ormbuilder.DefineColumn("id", ormbuilder.TextKeyType(191)).NotNull(),
		ormbuilder.DefineColumn("created_at", ormbuilder.TextType()).NotNull(),
		ormbuilder.DefineColumn("updated_at", ormbuilder.TextType()).NotNull(),
	}
	for _, field := range object.Fields {
		columns = append(columns, ormbuilder.DefineColumn(field.Key, ormbuilder.TextType()))
	}
	statement, args, err := ormbuilder.NewCreateTableBuilder(store.SQLRenderer, object.Key).
		Columns(columns...).
		Unique("workspace_id", "id").
		Build()
	if err != nil {
		t.Fatalf("build table %s: %v", object.Key, err)
	}
	if _, err := store.DB().Exec(statement, args...); err != nil {
		t.Fatalf("create table %s: %v", object.Key, err)
	}
}
