package bootstrap_test

import (
	"os"
	"strings"
	"testing"
)

func TestDatabaseBootstrapAndMigrationSQLRequireContext(t *testing.T) {
	for _, name := range []string{"../store_migrations.go", "../../mysql/dialect.go", "../../postgres/dialect.go", "../../sqlite/dialect.go"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{".Exec(", ".Query(", ".QueryRow(", ".Begin(", ".Ping("} {
			if strings.Contains(string(raw), forbidden) {
				t.Errorf("%s uses non-context database call %s", name, forbidden)
			}
		}
	}
	if _, err := os.Stat("../runtime_store.go"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../runtime_store.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "func Open(") {
		t.Fatal("storage.Open compatibility entrypoint reintroduced; use OpenContext")
	}
}
