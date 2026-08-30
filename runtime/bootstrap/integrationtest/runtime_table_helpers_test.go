package integrationtest

import (
	recordschema "github.com/domainry/domainry-orm/recordschema"
	ormschema "github.com/domainry/domainry-orm/schema"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func planToProduceCreateTable(t *testing.T, store *persistence.RuntimeStore, object definitionmodel.ObjectSchema) {
	t.Helper()
	columns := make([]ormschema.ColumnDefinition, 0, len(object.Fields))
	for _, field := range object.Fields {
		columns = append(columns, ormschema.Column(field.Key, ormschema.Text()))
	}
	statement, args, err := recordschema.NewTable(store.SQLRenderer, object.Key).
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
