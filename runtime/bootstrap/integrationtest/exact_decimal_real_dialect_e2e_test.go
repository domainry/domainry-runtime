package integrationtest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestExactDecimalFixtureIsByteIdenticalAcrossSQLitePostgresAndMySQL(t *testing.T) {
	postgresDSN := strings.TrimSpace(os.Getenv("RUNTIME_POSTGRES_TEST_DSN"))
	mysqlDSN := strings.TrimSpace(os.Getenv("RUNTIME_MYSQL_TEST_DSN"))
	if postgresDSN == "" || mysqlDSN == "" {
		if strings.TrimSpace(os.Getenv("RUNTIME_REQUIRE_REAL_DIALECTS")) == "1" {
			t.Fatalf("RUNTIME_POSTGRES_TEST_DSN and RUNTIME_MYSQL_TEST_DSN are required when RUNTIME_REQUIRE_REAL_DIALECTS=1")
		}
		t.Skip("real PostgreSQL and MySQL DSNs are required for the cross-dialect decimal fixture")
	}
	key := time.Now().UnixNano()
	cases := []struct {
		name   string
		config func(*testing.T) config.Config
	}{
		{name: "sqlite", config: func(t *testing.T) config.Config {
			return config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "decimal.db")}
		}},
		{name: "postgres", config: func(t *testing.T) config.Config {
			return realDialectPostgresConfig(t, postgresDSN, "runtime_decimal_"+decimalFixtureKey(key))
		}},
		{name: "mysql", config: func(t *testing.T) config.Config {
			return realDialectMySQLConfig(t, mysqlDSN, "runtime_decimal_"+decimalFixtureKey(key))
		}},
	}
	results := map[string][]byte{}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			results[test.name] = runExactDecimalPersistenceFixture(t, test.config(t))
		})
	}
	for _, dialect := range []string{"postgres", "mysql"} {
		if !bytes.Equal(results["sqlite"], results[dialect]) {
			t.Fatalf("decimal fixture differs: sqlite=%s %s=%s", results["sqlite"], dialect, results[dialect])
		}
	}
}

func runExactDecimalPersistenceFixture(t *testing.T, cfg config.Config) []byte {
	t.Helper()
	store, err := persistence.OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "exact_decimal_fixture", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "currency", Config: map[string]any{"precision": 19, "scale": 2, "rounding_mode": "half_even", "currency_code": "CNY"}}}}
	metadata := appschemapersistence.NewApplicationSchemaStore(store)
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "install exact decimal cross-dialect fixture")
	if err := metadata.SyncManifest(t.Context(), scope, manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}); err != nil {
		t.Fatal(err)
	}
	records := recordpersistence.NewRecordStore(store)
	for index, amount := range []string{"0.10", "0.20", "0.30", "-2.00", "2.00", "10.00"} {
		stamp := "2026-07-21T00:00:00Z"
		if err := records.InsertRecord(t.Context(), "workspace-a", object, recordmodel.Record{ID: decimalFixtureKey(int64(index)), CreatedAt: stamp, UpdatedAt: stamp, Data: map[string]any{"amount": amount}}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := records.ListRecords(t.Context(), "workspace-a", object, recordmodel.RecordListQuery{Page: 1, PageSize: 20, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, Sort: []recordmodel.RecordSortRule{{Field: "amount", Direction: "asc"}}})
	if err != nil {
		t.Fatal(err)
	}
	amounts := make([]string, len(page.Items))
	for index := range page.Items {
		amounts[index] = page.Items[index].Data["amount"].(string)
	}
	encoded, err := json.Marshal(amounts)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func decimalFixtureKey(value int64) string {
	if value < 0 {
		value = -value
	}
	return strconv.FormatInt(value, 10)
}
