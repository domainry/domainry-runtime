package agent

import (
	"os"
	"strings"
	"testing"
)

func TestAgentPersistencePackageDoesNotOwnDDL(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"CREATE TABLE", "ALTER TABLE", "DROP TABLE", "NewCreateTableBuilder", "NewAlterTableBuilder"} {
			if strings.Contains(string(source), forbidden) {
				t.Fatalf("agent CRUD owner %s contains schema mutation %q", entry.Name(), forbidden)
			}
		}
	}
}
