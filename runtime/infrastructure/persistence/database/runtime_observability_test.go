package database

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRuntimeStoreObservesAllSQLWithoutQueryTextLabels(t *testing.T) {
	store, err := OpenContext(t.Context(), config.Config{
		DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "observability.db"),
		IntegrationSecretKey: "test-integration-secret-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE observability_secret (id TEXT PRIMARY KEY, password TEXT)`); err != nil {
		t.Fatal(err)
	}
	tx, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO observability_secret (id, password) VALUES (?, ?)`, "one", "hidden-value"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	output := store.SQLMetrics().OpenMetrics(t.Context())
	for _, expected := range []string{
		`domainry_runtime_db_query_duration_seconds`,
		`role="runtime",operation="ddl",outcome="success"`,
		`role="runtime",operation="insert",outcome="success"`,
		`role="runtime",operation="transaction",outcome="rollback"`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("database metrics missing %q:\n%s", expected, output)
		}
	}
	for _, forbidden := range []string{"observability_secret", "password", "hidden-value"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("database telemetry leaked SQL or values: %s", output)
		}
	}
}
