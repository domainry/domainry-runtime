package integrationtest

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func planToProduceCreateTable(t *testing.T, store *persistence.RuntimeStore, object definitionmodel.ObjectSchema) {
	t.Helper()
	statement := `CREATE TABLE "` + object.Key + `" ("workspace_id" TEXT NOT NULL, "id" TEXT NOT NULL, "created_at" TEXT NOT NULL, "updated_at" TEXT NOT NULL`
	for _, field := range object.Fields {
		statement += `, "` + field.Key + `" TEXT`
	}
	statement += `, UNIQUE ("workspace_id", "id"))`
	if _, err := store.DB().Exec(statement); err != nil {
		t.Fatalf("create table %s: %v", object.Key, err)
	}
}
