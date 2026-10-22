package record_test

import (
	"testing"

	"github.com/domainry/domainry-orm/recordschema"
	ormschema "github.com/domainry/domainry-orm/schema"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func createGeneratedRecordTable(t *testing.T, store *persistence.RuntimeStore, objectKey string, columns ...ormschema.ColumnDefinition) {
	t.Helper()
	statement, args, err := recordschema.NewTable(store.RuntimeRenderer(), objectKey).Columns(columns...).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
}
