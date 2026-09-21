package integrationtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// definitionUpgradeManifest builds the manifest generations of the upgrade
// scenario. Every generation carries a content-derived version so the same
// definitions always project under the same Metadata schema_version.
func definitionUpgradeManifest(t *testing.T, dir, name string, customerFields []map[string]any, extraObjects ...map[string]any) string {
	t.Helper()
	objectKeys := []string{"customer", "opportunity"}
	for _, object := range extraObjects {
		objectKeys = append(objectKeys, object["key"].(string))
	}
	roles := []map[string]any{}
	for _, role := range []string{"admin", "business_admin"} {
		permissions := []map[string]any{}
		for _, object := range objectKeys {
			for _, operation := range []string{"read", "create", "update"} {
				permissions = append(permissions, map[string]any{"permission_key": object + "." + operation, "data_scope": "all"})
			}
		}
		roles = append(roles, map[string]any{"key": role, "name": strings.ToUpper(role[:1]) + role[1:], "permissions": permissions})
	}
	objects := []map[string]any{
		{"key": "customer", "name": "Customer", "fields": customerFields},
		{"key": "opportunity", "name": "Opportunity", "fields": []map[string]any{
			{"key": "customer", "name": "Customer", "type": "relation", "required": true, "validation": map[string]any{"target": "customer"}},
			{"key": "title", "name": "Title", "type": "text", "required": true},
		}},
	}
	objects = append(objects, extraObjects...)
	manifest := map[string]any{"schema_version": "2", "template_id": "definition_upgrade", "version": "pending", "name": "Definition upgrade", "objects": objects, "roles": roles}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := manifestmodel.DecodeManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	version, err := manifestmodel.ManifestDefinitionVersion(decoded)
	if err != nil {
		t.Fatal(err)
	}
	manifest["version"] = version
	if raw, err = json.MarshalIndent(manifest, "", "  "); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func definitionUpgradeBoot(t *testing.T, cfg config.Config, manifestPath string) (runtime *bootstrap.Runtime, err error) {
	t.Helper()
	cfg.ManifestPath = manifestPath
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				err = recoveredErr
				return
			}
			err = fmt.Errorf("%v", recovered)
		}
	}()
	return newIntegrationRuntime(t, cfg), nil
}

func definitionUpgradeCount(t *testing.T, store *persistence.RuntimeStore, statement string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := store.DB().QueryRowContext(t.Context(), statement, args...).Scan(&count); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
	return count
}

func TestDefinitionUpgradeAcrossRuntimeBoots(t *testing.T) {
	runDefinitionUpgradeScenario(t, func(t *testing.T) config.Config {
		return config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db")}
	})
}

func TestDefinitionUpgradeAcrossRuntimeBootsPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("RUNTIME_POSTGRES_TEST_DSN"))
	if dsn == "" {
		if strings.TrimSpace(os.Getenv("RUNTIME_REQUIRE_REAL_DIALECTS")) == "1" {
			t.Fatal("RUNTIME_POSTGRES_TEST_DSN is required when RUNTIME_REQUIRE_REAL_DIALECTS=1")
		}
		t.Skip("RUNTIME_POSTGRES_TEST_DSN is required for the PostgreSQL definition upgrade scenario")
	}
	runDefinitionUpgradeScenario(t, func(t *testing.T) config.Config {
		return realDialectPostgresConfig(t, dsn, fmt.Sprintf("runtime_definition_upgrade_%d", time.Now().UnixNano()))
	})
}

func runDefinitionUpgradeScenario(t *testing.T, databaseConfig func(*testing.T) config.Config) {
	t.Helper()
	base := databaseConfig(t)
	base.AppLocale = "en-US"
	base.UploadDir = filepath.Join(t.TempDir(), "uploads")
	manifests := t.TempDir()
	v1Fields := []map[string]any{
		{"key": "name", "name": "Name", "type": "text", "required": true},
		{"key": "code", "name": "Code", "type": "text"},
	}
	v2Fields := append(append([]map[string]any{}, v1Fields...),
		map[string]any{"key": "tier", "name": "Tier", "type": "text", "required": true, "upgrade": map[string]any{"existing_rows": "backfill", "backfill_value": "standard"}},
		map[string]any{"key": "region", "name": "Region", "type": "text", "required": true, "upgrade": map[string]any{"existing_rows": "exempt"}},
	)
	invoice := map[string]any{"key": "invoice", "name": "Invoice", "fields": []map[string]any{{"key": "number", "name": "Number", "type": "text", "required": true}}}
	v1 := definitionUpgradeManifest(t, manifests, "v1", v1Fields)
	v2 := definitionUpgradeManifest(t, manifests, "v2", v2Fields, invoice)
	v2b := definitionUpgradeManifest(t, manifests, "v2b", append(append([]map[string]any{}, v1Fields...), map[string]any{"key": "segment", "name": "Segment", "type": "text", "required": true}))
	v3Fields := append([]map[string]any{}, v2Fields...)
	v3Fields[2] = map[string]any{"key": "tier", "name": "Tier", "type": "integer", "required": true, "upgrade": map[string]any{"existing_rows": "backfill", "backfill_value": 1}}
	v3 := definitionUpgradeManifest(t, manifests, "v3", v3Fields, invoice)

	openStore := func() *persistence.RuntimeStore {
		store, err := persistence.OpenContext(t.Context(), base)
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	receiptsTable := func(store *persistence.RuntimeStore) string {
		return store.TableIdentifier("_application_schema_upgrade_receipts")
	}
	versionsTable := func(store *persistence.RuntimeStore) string {
		return store.TableIdentifier("_metadata_definition_versions")
	}
	customerTable := func(store *persistence.RuntimeStore) string { return store.TableIdentifier("customer") }

	// Boot 1: v1 creates roles and records, including a soft-deleted customer
	// and an opportunity that relates to a customer.
	runtime, err := definitionUpgradeBoot(t, base, v1)
	if err != nil {
		t.Fatalf("boot v1: %v", err)
	}
	handler := runtime.Routes()
	schema := runtimeFixtureRequest[map[string]any](t, handler, "business_admin", http.MethodGet, "/discovery/schema", nil)
	assertRuntimeFixtureArrayHasKey(t, schema, "objects", "customer")
	assertRuntimeFixtureArrayHasKey(t, schema, "objects", "opportunity")
	acme := runtimeFixtureRequest[map[string]any](t, handler, "business_admin", http.MethodPost, "/records/customer", map[string]any{"data": map[string]any{"name": "Acme", "code": "ACME"}})
	beta := runtimeFixtureRequest[map[string]any](t, handler, "business_admin", http.MethodPost, "/records/customer", map[string]any{"data": map[string]any{"name": "Beta", "code": "BETA"}})
	retired := runtimeFixtureRequest[map[string]any](t, handler, "business_admin", http.MethodPost, "/records/customer", map[string]any{"data": map[string]any{"name": "Retired", "code": "RET"}})
	acmeID, betaID, retiredID := acme["id"].(string), beta["id"].(string), retired["id"].(string)
	opportunity := runtimeFixtureRequest[map[string]any](t, handler, "business_admin", http.MethodPost, "/records/opportunity", map[string]any{"data": map[string]any{"customer": acmeID, "title": "Acme expansion"}})
	opportunityID := opportunity["id"].(string)
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := openStore()
	if _, err := store.DB().ExecContext(t.Context(), "UPDATE "+customerTable(store)+" SET "+store.Identifier("deleted")+" = "+store.Placeholder(1)+" WHERE "+store.Identifier("id")+" = "+store.Placeholder(2), true, retiredID); err != nil {
		t.Fatal(err)
	}
	// Runtime seeds a baseline customer of its own, so row expectations are
	// anchored on the physical count after the first boot.
	customersAfterV1 := definitionUpgradeCount(t, store, "SELECT COUNT(*) FROM "+customerTable(store))
	if customersAfterV1 < 3 {
		t.Fatalf("customer rows after v1=%d", customersAfterV1)
	}
	receiptsAfterV1 := definitionUpgradeCount(t, store, "SELECT COUNT(*) FROM "+receiptsTable(store))
	if receiptsAfterV1 == 0 {
		t.Fatal("v1 boot wrote no upgrade receipts")
	}
	_ = store.Close()

	// Boot 2: v2 adds an object, a backfilled required field and an exempt
	// required field against the same database.
	runtime, err = definitionUpgradeBoot(t, base, v2)
	if err != nil {
		t.Fatalf("boot v2: %v", err)
	}
	handler = runtime.Routes()
	upgradedAcme := runtimeFixtureRequest[map[string]any](t, handler, "business_admin", http.MethodGet, "/records/customer/items/"+acmeID, nil)
	assertDefinitionUpgradeField(t, upgradedAcme, "tier", "standard")
	assertDefinitionUpgradeField(t, upgradedAcme, "name", "Acme")
	related := runtimeFixtureRequest[map[string]any](t, handler, "business_admin", http.MethodGet, "/records/opportunity/items/"+opportunityID, nil)
	assertDefinitionUpgradeField(t, related, "customer", acmeID)
	// Exempt semantics: the old row updates without region, a create still
	// needs it, and clearing a filled region is rejected.
	updatedBeta := runtimeFixtureRequest[map[string]any](t, handler, "business_admin", http.MethodPatch, "/records/customer/items/"+betaID, map[string]any{"data": map[string]any{"name": "Beta Industries"}})
	assertDefinitionUpgradeField(t, updatedBeta, "name", "Beta Industries")
	assertDefinitionUpgradeField(t, updatedBeta, "tier", "standard")
	runtimeFixtureRequestStatus(t, handler, "business_admin", http.MethodPost, "/records/customer", map[string]any{"data": map[string]any{"name": "Gamma", "tier": "gold"}}, http.StatusBadRequest)
	// Clearing a filled exempt field is rejected at validation level (covered
	// by the record validation unit tests); the PATCH contract drops empty
	// patch values before validation, so it cannot express a clear here.
	gamma := runtimeFixtureRequest[map[string]any](t, handler, "business_admin", http.MethodPost, "/records/customer", map[string]any{"data": map[string]any{"name": "Gamma", "tier": "gold", "region": "north"}})
	assertDefinitionUpgradeField(t, gamma, "region", "north")
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	store = openStore()
	// The new object exists physically (Runtime seeds its baseline row); the
	// in-process Identity fixture has no invoice permissions, so the table is
	// asserted directly instead of through discovery.
	if invoices := definitionUpgradeCount(t, store, "SELECT COUNT(*) FROM "+store.TableIdentifier("invoice")); invoices < 0 {
		t.Fatalf("invoice rows after v2=%d", invoices)
	}
	if rows := definitionUpgradeCount(t, store, "SELECT COUNT(*) FROM "+customerTable(store)); rows != customersAfterV1+1 {
		t.Fatalf("customer rows after v2=%d want %d", rows, customersAfterV1+1)
	}
	if deleted := definitionUpgradeCount(t, store, "SELECT COUNT(*) FROM "+customerTable(store)+" WHERE "+store.Identifier("deleted")+" = "+store.Placeholder(1)+" AND "+store.Identifier("id")+" = "+store.Placeholder(2), true, retiredID); deleted != 1 {
		t.Fatalf("soft-deleted marker lost after v2: %d", deleted)
	}
	if backfilled := definitionUpgradeCount(t, store, "SELECT COUNT(*) FROM "+customerTable(store)+" WHERE "+store.Identifier("tier")+" = "+store.Placeholder(1), "standard"); backfilled != customersAfterV1 {
		t.Fatalf("backfilled customers after v2=%d want %d (soft-deleted rows are backfilled too)", backfilled, customersAfterV1)
	}
	if gold := definitionUpgradeCount(t, store, "SELECT COUNT(*) FROM "+customerTable(store)+" WHERE "+store.Identifier("tier")+" = "+store.Placeholder(1)+" AND "+store.Identifier("region")+" = "+store.Placeholder(2), "gold", "north"); gold != 1 {
		t.Fatalf("created customer after v2=%d", gold)
	}
	receiptsAfterV2 := definitionUpgradeCount(t, store, "SELECT COUNT(*) FROM "+receiptsTable(store))
	if receiptsAfterV2 <= receiptsAfterV1 {
		t.Fatalf("v2 receipts=%d v1 receipts=%d", receiptsAfterV2, receiptsAfterV1)
	}
	if completed := definitionUpgradeCount(t, store, "SELECT COUNT(*) FROM "+receiptsTable(store)+" WHERE "+store.Identifier("step_key")+" = "+store.Placeholder(1)+" AND "+store.Identifier("status")+" = "+store.Placeholder(2), "backfill:customer.tier@"+definitionUpgradeVersion(t, v2), "completed"); completed != 1 {
		t.Fatalf("tier backfill receipt count=%d", completed)
	}
	versionsAfterV2 := definitionUpgradeCount(t, store, "SELECT COUNT(*) FROM "+versionsTable(store))
	_ = store.Close()

	// Boot 3: v2 again is idempotent.
	runtime, err = definitionUpgradeBoot(t, base, v2)
	if err != nil {
		t.Fatalf("boot v2 again: %v", err)
	}
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	store = openStore()
	if receipts := definitionUpgradeCount(t, store, "SELECT COUNT(*) FROM "+receiptsTable(store)); receipts != receiptsAfterV2 {
		t.Fatalf("receipts changed on repeated v2 boot: %d vs %d", receipts, receiptsAfterV2)
	}
	if versions := definitionUpgradeCount(t, store, "SELECT COUNT(*) FROM "+versionsTable(store)); versions != versionsAfterV2 {
		t.Fatalf("definition versions changed on repeated v2 boot: %d vs %d", versions, versionsAfterV2)
	}
	_ = store.Close()

	// Boot 4: v3 changes tier to an exact physical type and must be refused
	// with a clear code before anything is written.
	if _, err := definitionUpgradeBoot(t, base, v3); err == nil || !strings.Contains(err.Error(), "backend.metadata.definition_upgrade_blocked") || !strings.Contains(err.Error(), "backend.metadata.upgrade_field_type_change_unsupported(customer.tier)") {
		t.Fatalf("boot v3 err=%v", err)
	}
	store = openStore()
	if receipts := definitionUpgradeCount(t, store, "SELECT COUNT(*) FROM "+receiptsTable(store)); receipts != receiptsAfterV2 {
		t.Fatalf("blocked v3 boot wrote receipts: %d vs %d", receipts, receiptsAfterV2)
	}
	_ = store.Close()

	// Boot 5: v2 still starts after the refused upgrade.
	runtime, err = definitionUpgradeBoot(t, base, v2)
	if err != nil {
		t.Fatalf("boot v2 after blocked v3: %v", err)
	}
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}

	// Boot 6: v2b adds a required field without a rule on a populated table.
	if _, err := definitionUpgradeBoot(t, base, v2b); err == nil || !strings.Contains(err.Error(), "backend.metadata.definition_upgrade_blocked") || !strings.Contains(err.Error(), "backend.metadata.upgrade_required_field_rule_missing(customer.segment)") {
		t.Fatalf("boot v2b err=%v", err)
	}

	// Boot 7: v1 still works; the v2 columns are retained physically.
	runtime, err = definitionUpgradeBoot(t, base, v1)
	if err != nil {
		t.Fatalf("boot v1 after v2: %v", err)
	}
	handler = runtime.Routes()
	downgradedAcme := runtimeFixtureRequest[map[string]any](t, handler, "business_admin", http.MethodGet, "/records/customer/items/"+acmeID, nil)
	assertDefinitionUpgradeField(t, downgradedAcme, "name", "Acme")
	delta := runtimeFixtureRequest[map[string]any](t, handler, "business_admin", http.MethodPost, "/records/customer", map[string]any{"data": map[string]any{"name": "Delta"}})
	assertDefinitionUpgradeField(t, delta, "name", "Delta")
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func definitionUpgradeVersion(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := manifestmodel.DecodeManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return manifest.Version
}

func assertDefinitionUpgradeField(t *testing.T, record map[string]any, field string, expected any) {
	t.Helper()
	data, _ := record["data"].(map[string]any)
	if data == nil || data[field] != expected {
		t.Fatalf("expected record field %s=%v, got %#v", field, expected, record)
	}
}
