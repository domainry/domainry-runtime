package schema

import (
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
)

func TestManagedDatabaseCohortSchemaIsCanonicalForEveryDialect(t *testing.T) {
	for _, test := range []struct {
		name     string
		renderer interface {
			Identifier(string) string
			Table(string) string
			Placeholder(int) string
		}
	}{
		{"sqlite", sqlite.NewEngine().SQLDialect().WithSchema("")},
		{"postgres", postgres.NewEngine().SQLDialect().WithSchema("")},
		{"mysql", mysql.NewEngine().SQLDialect().WithSchema("")},
	} {
		t.Run(test.name, func(t *testing.T) {
			statement, err := ManagedDatabaseCohortCreateStatement(test.renderer)
			if err != nil {
				t.Fatal(err)
			}
			for _, fragment := range []string{ManagedDatabaseCohortTable, "marker_id", "SMALLINT NOT NULL PRIMARY KEY", "contract_version", "VARCHAR(128) NOT NULL", "database_identity_sha256", "CHAR(64) NOT NULL"} {
				if !strings.Contains(statement, fragment) {
					t.Fatalf("DDL %q lacks %q", statement, fragment)
				}
			}
		})
	}
	if _, err := ManagedDatabaseCohortCreateStatement(nil); err == nil {
		t.Fatal("nil renderer accepted")
	}
}
