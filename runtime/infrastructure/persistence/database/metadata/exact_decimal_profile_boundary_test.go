package metadata

import (
	"os"
	"strings"
	"testing"
)

func TestExactDecimalOrchestratorContainsNoEngineSQL(t *testing.T) {
	source, err := os.ReadFile("exact_decimal_migration.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"PRAGMA", "information_schema", "::numeric", "ALGORITHM=COPY",
		"CREATE TABLE", "ALTER TABLE", "DROP TABLE", "runtime_exact_decimal_encode",
	} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("exact-decimal orchestrator contains engine SQL %q", forbidden)
		}
	}
}
