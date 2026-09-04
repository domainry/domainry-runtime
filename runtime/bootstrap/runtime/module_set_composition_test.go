package runtime

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"testing"
	"time"

	agentmodule "github.com/domainry/domainry-agent/module"
	connector "github.com/domainry/domainry-connector-sdk"
	dataexchangemodule "github.com/domainry/domainry-data-exchange/module"
	"github.com/domainry/domainry-foundation/modulecapability"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identitymodule "github.com/domainry/domainry-identity/module"
	integrationmodule "github.com/domainry/domainry-integration/module"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	monitoringremote "github.com/domainry/domainry-monitoring-sdk/remote"
	monitoringcapability "github.com/domainry/domainry-monitoring/capability"
	monitoringmodule "github.com/domainry/domainry-monitoring/module"
	monitoringserver "github.com/domainry/domainry-monitoring/saas"
	notificationmodule "github.com/domainry/domainry-notification/module"
	"github.com/domainry/domainry-orm/query"
	reportmodule "github.com/domainry/domainry-report/module"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	schedulermodule "github.com/domainry/domainry-scheduler/module"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"golang.org/x/mod/modfile"
)

const moduleSetLockContractVersion = "domainry-runtime-module-set-v1"

type moduleSetLock struct {
	ContractVersion string                `json:"contract_version"`
	SetSHA256       string                `json:"set_sha256"`
	Modules         []moduleSetLockModule `json:"modules"`
}

type moduleSetLockModule struct {
	Key                      string `json:"key"`
	Implementation           string `json:"implementation"`
	ImplementationVersion    string `json:"implementation_version"`
	ImplementationSum        string `json:"implementation_sum"`
	SDK                      string `json:"sdk"`
	SDKVersion               string `json:"sdk_version"`
	SDKSum                   string `json:"sdk_sum"`
	CapabilityContractSHA256 string `json:"capability_contract_sha256"`
}

func TestPinnedModuleSetLockMatchesGoMod(t *testing.T) {
	lock := loadModuleSetLock(t)
	if lock.ContractVersion != moduleSetLockContractVersion || len(lock.Modules) != 11 {
		t.Fatalf("module-set lock contract=%q modules=%d", lock.ContractVersion, len(lock.Modules))
	}
	wantDigest := moduleSetLockSHA256(lock)
	if lock.SetSHA256 != wantDigest {
		t.Fatalf("module-set digest=%q want=%q", lock.SetSHA256, wantDigest)
	}

	root := moduleSetRepositoryRoot(t)
	rawGoMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := modfile.Parse("go.mod", rawGoMod, nil)
	if err != nil {
		t.Fatal(err)
	}
	required := make(map[string]string, len(parsed.Require))
	for _, item := range parsed.Require {
		if !item.Indirect {
			required[item.Mod.Path] = item.Mod.Version
		}
	}
	sums := readModuleSetGoSums(t, filepath.Join(root, "go.sum"))
	seenKeys := map[string]bool{}
	for _, module := range lock.Modules {
		if module.Key == "" || seenKeys[module.Key] {
			t.Fatalf("module-set lock has invalid or duplicate key %q", module.Key)
		}
		seenKeys[module.Key] = true
		for _, dependency := range []struct {
			kind, path, version, sum string
		}{
			{kind: "implementation", path: module.Implementation, version: module.ImplementationVersion, sum: module.ImplementationSum},
			{kind: "sdk", path: module.SDK, version: module.SDKVersion, sum: module.SDKSum},
		} {
			if required[dependency.path] != dependency.version {
				t.Errorf("%s %s go.mod version=%q lock=%q", module.Key, dependency.kind, required[dependency.path], dependency.version)
			}
			if sums[dependency.path+" "+dependency.version] != dependency.sum {
				t.Errorf("%s %s go.sum=%q lock=%q", module.Key, dependency.kind, sums[dependency.path+" "+dependency.version], dependency.sum)
			}
		}
	}
}

func TestPinnedModuleSetComposesAllElevenBindingsOnOneHostDatabase(t *testing.T) {
	for _, topology := range []struct {
		name       string
		monitoring func(*testing.T) monitoringsdk.Factory
		wantMode   string
	}{
		{name: "all-module", monitoring: moduleSetMonitoringModuleFactory, wantMode: "module"},
		{name: "monitoring-saas", monitoring: moduleSetMonitoringSaaSFactory, wantMode: "saas"},
	} {
		t.Run(topology.name, func(t *testing.T) {
			cfg := moduleSetTestConfig(t)
			firstDigests, firstLedgerRows := runPinnedModuleSet(t, cfg, topology.monitoring(t), topology.wantMode)
			secondDigests, secondLedgerRows := runPinnedModuleSet(t, cfg, topology.monitoring(t), topology.wantMode)
			if fmt.Sprint(secondDigests) != fmt.Sprint(firstDigests) {
				t.Fatalf("module contract digests changed after restart: first=%v second=%v", firstDigests, secondDigests)
			}
			if secondLedgerRows != firstLedgerRows {
				t.Fatalf("module migrations were reapplied after restart: first=%d second=%d", firstLedgerRows, secondLedgerRows)
			}
		})
	}
}

func TestPinnedModuleSetComposesAllElevenBindingsAcrossRealDialects(t *testing.T) {
	stamp := time.Now().UnixNano()
	tests := []struct {
		name   string
		dsnEnv string
		cfg    func(*testing.T, string) config.Config
	}{
		{name: "postgres", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN", cfg: func(t *testing.T, dsn string) config.Config {
			schema := fmt.Sprintf("runtime_module_set_%d", stamp)
			admin, err := pgx.Connect(t.Context(), dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				// Test infrastructure provisioning has no domainry-orm equivalent.
				_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
				_ = admin.Close(cleanupCtx)
			})
			return config.Config{DatabaseDriver: "postgres", DatabaseDSN: dsn, DatabaseSchema: schema, DatabaseMigrationMode: "apply"}
		}},
		{name: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN", cfg: func(t *testing.T, dsn string) config.Config {
			parsed, err := mysqldriver.ParseDSN(dsn)
			if err != nil {
				t.Fatal(err)
			}
			adminConfig := parsed.Clone()
			adminConfig.DBName = ""
			admin, err := sql.Open("mysql", adminConfig.FormatDSN())
			if err != nil {
				t.Fatal(err)
			}
			databaseName := fmt.Sprintf("runtime_module_set_%d", stamp)
			identifier := "`" + databaseName + "`"
			// Test infrastructure provisioning has no domainry-orm equivalent.
			if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+identifier+" CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
				_ = admin.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				_, _ = admin.ExecContext(cleanupCtx, "DROP DATABASE IF EXISTS "+identifier)
				_ = admin.Close()
			})
			parsed.DBName = databaseName
			return config.Config{DatabaseDriver: "mysql", DatabaseDSN: parsed.FormatDSN(), DatabaseMigrationMode: "apply"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(test.dsnEnv))
			if dsn == "" {
				if strings.TrimSpace(os.Getenv("RUNTIME_REQUIRE_REAL_DIALECTS")) == "1" {
					t.Fatalf("%s is required", test.dsnEnv)
				}
				t.Skipf("%s is not configured", test.dsnEnv)
			}
			cfg := moduleSetTestConfig(t)
			database := test.cfg(t, dsn)
			cfg.DatabaseDriver, cfg.DatabaseDSN, cfg.DatabaseSchema = database.DatabaseDriver, database.DatabaseDSN, database.DatabaseSchema
			cfg.DatabaseMigrationMode, cfg.DBPath = database.DatabaseMigrationMode, ""
			firstDigests, firstLedgerRows := runPinnedModuleSet(t, cfg, moduleSetMonitoringModuleFactory(t), "module")
			secondDigests, secondLedgerRows := runPinnedModuleSet(t, cfg, moduleSetMonitoringModuleFactory(t), "module")
			if fmt.Sprint(secondDigests) != fmt.Sprint(firstDigests) || secondLedgerRows != firstLedgerRows {
				t.Fatalf("real-dialect restart drifted: first_digests=%v second_digests=%v first_ledger=%d second_ledger=%d", firstDigests, secondDigests, firstLedgerRows, secondLedgerRows)
			}
		})
	}
}

func runPinnedModuleSet(t *testing.T, cfg config.Config, monitoringFactory monitoringsdk.Factory, monitoringMode string) (map[string]string, int) {
	t.Helper()
	store, err := PrepareProjectDatabase(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	identityBinding, err := identitymodule.NewFactory(identitymodule.Options{DatabaseDriver: cfg.DatabaseDriver, DatabasePath: cfg.DBPath}).OpenWithDatabase(
		t.Context(),
		identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience)},
		identitysdk.DatabaseHandle{Pool: store.DB(), Driver: store.Driver(), Schema: store.DatabaseSchema(), FilePath: cfg.DBPath, Migrations: store, ModuleMigrations: store},
	)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	handlers := runtimeext.NewBusinessHandlerRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()

	application := NewVerifiedProjectWithAllTopologyFactoriesAndDatabase(
		t.Context(), cfg, handlers, connectors,
		runtimehttp.RuntimeReleaseIdentity{}, deploymentapplication.RuntimeReleaseArtifactEvidence{},
		identityBinding,
		notificationmodule.NewFactory(notificationmodule.Options{}),
		monitoringFactory,
		schedulermodule.NewFactory(schedulermodule.Options{}),
		dataexchangemodule.NewFactory(dataexchangemodule.Options{}),
		integrationmodule.NewFactory(),
		reportmodule.NewFactory(),
		store,
		agentmodule.NewFactory(agentmodule.Options{BaseURL: "http://127.0.0.1", APIKey: "module-set-test", AgentID: 1}),
	)
	defer func() {
		if err := application.CloseContext(context.Background()); err != nil {
			t.Errorf("close composed Runtime: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Errorf("close host database: %v", err)
		}
	}()

	inventory, err := application.ModuleInventory()
	if err != nil {
		t.Fatal(err)
	}
	wantModes := map[string]string{
		"identity": "module", "metadata": "module", "audit": "module", "lifecycle": "module",
		"notification": "module", "integration": "module", "monitoring": monitoringMode,
		"scheduler": "module", "data_exchange": "module", "agent": "module", "report": "module",
	}
	if len(inventory.Modules) != len(wantModes) {
		t.Fatalf("module inventory has %d Bindings, want %d: %+v", len(inventory.Modules), len(wantModes), inventory.Modules)
	}
	for _, descriptor := range inventory.Modules {
		wantMode, found := wantModes[descriptor.Key]
		if !found {
			t.Fatalf("unexpected module descriptor %+v", descriptor)
		}
		if string(descriptor.Mode) != wantMode {
			t.Fatalf("module %s mode=%s want=%s", descriptor.Key, descriptor.Mode, wantMode)
		}
		delete(wantModes, descriptor.Key)
	}
	if len(wantModes) != 0 {
		t.Fatalf("missing module Bindings: %v", wantModes)
	}

	digests := moduleSetCapabilityDigests(t, application)
	t.Logf("composed module capability digests: %v", digests)
	assertSingleHostMigrationLedger(t, store)
	return digests, moduleMigrationLedgerRows(t, store)
}

func moduleSetCapabilityDigests(t *testing.T, application *Runtime) map[string]string {
	t.Helper()
	lock := loadModuleSetLock(t)
	expected := make(map[string]string, len(lock.Modules))
	for _, module := range lock.Modules {
		expected[module.Key] = module.CapabilityContractSHA256
	}
	bindings := map[string]any{
		"identity": application.identityBinding, "metadata": application.metadataBinding,
		"audit": application.auditBinding, "lifecycle": application.lifecycleBinding,
		"notification": application.notificationBinding, "integration": application.integrationBinding,
		"monitoring": application.monitoringBinding, "scheduler": application.schedulerBinding,
		"data_exchange": application.dataExchangeBinding, "agent": application.agentBinding,
		"report": application.reportBinding,
	}
	digests := make(map[string]string, len(bindings))
	for key, raw := range bindings {
		binding, ok := raw.(modulecapability.Binding)
		if !ok || binding == nil {
			t.Fatalf("%s Binding %T does not expose the module capability contract", key, raw)
		}
		summary, err := binding.CapabilitySummary(t.Context())
		if err != nil {
			t.Fatalf("load %s capability summary: %v", key, err)
		}
		if summary.Identity.Key != key || len(summary.Identity.ContractSHA256) != 64 {
			t.Fatalf("%s capability identity=%+v", key, summary.Identity)
		}
		if summary.Identity.ContractSHA256 != expected[key] {
			t.Fatalf("%s capability contract digest=%q lock=%q", key, summary.Identity.ContractSHA256, expected[key])
		}
		digests[key] = summary.Identity.ContractSHA256
	}
	return digests
}

func assertSingleHostMigrationLedger(t *testing.T, store *persistence.RuntimeStore) {
	t.Helper()
	var rows *sql.Rows
	var err error
	switch store.Driver() {
	case "sqlite":
		// sqlite_master is the only authoritative SQLite schema inventory.
		rows, err = store.DB().QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND name LIKE '%schema_migrations%' ORDER BY name`)
	case "postgres", "pgx":
		// information_schema has no domainry-orm equivalent for ledger discovery.
		rows, err = store.DB().QueryContext(t.Context(), `SELECT table_name FROM information_schema.tables WHERE table_schema=$1 AND table_name LIKE '%schema_migrations%' ORDER BY table_name`, store.DatabaseSchema())
	case "mysql":
		// information_schema has no domainry-orm equivalent for ledger discovery.
		rows, err = store.DB().QueryContext(t.Context(), `SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name LIKE '%schema_migrations%' ORDER BY table_name`)
	default:
		t.Fatalf("unsupported composition-gate database driver %q", store.Driver())
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ledgers []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		ledgers = append(ledgers, name)
	}
	if strings.Join(ledgers, ",") != "_schema_migrations" {
		t.Fatalf("migration ledgers=%v want only _schema_migrations", ledgers)
	}

	ownerQuery, ownerArgs, err := query.NewSelectBuilder(store.SQLRenderer, "_schema_migrations").
		Columns("kind").
		Distinct().
		Where(query.Like("kind", "module:%")).
		OrderBy(query.Ascending("kind")).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	ownerRows, err := store.DB().QueryContext(t.Context(), ownerQuery, ownerArgs...)
	if err != nil {
		t.Fatal(err)
	}
	defer ownerRows.Close()
	var owners []string
	for ownerRows.Next() {
		var kind string
		if err := ownerRows.Scan(&kind); err != nil {
			t.Fatal(err)
		}
		owners = append(owners, strings.TrimPrefix(kind, "module:"))
	}
	wantOwners := []string{"agent", "audit", "data_exchange", "identity", "integration", "lifecycle", "metadata", "notification", "report", "scheduler"}
	if fmt.Sprint(owners) != fmt.Sprint(wantOwners) {
		t.Fatalf("host migration owners=%v want=%v", owners, wantOwners)
	}
}

func moduleMigrationLedgerRows(t *testing.T, store *persistence.RuntimeStore) int {
	t.Helper()
	statement, args, err := query.NewSelectBuilder(store.SQLRenderer, "_schema_migrations").
		Projections(query.Project(query.CountAll())).
		Where(query.Like("kind", "module:%")).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.DB().QueryRowContext(t.Context(), statement, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func moduleSetTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := bootstrapTestConfig(t)
	cfg.RuntimeInstanceID = "runtime-module-set"
	raw, err := os.ReadFile(cfg.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["agents"] = []any{map[string]any{
		"key": "composition_probe", "name": "Composition probe", "description": "Opens the Agent Binding for the Runtime composition gate.",
		"tools": []any{"createRecord"},
	}}
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ManifestPath = filepath.Join(t.TempDir(), "module-set-manifest.json")
	if err := os.WriteFile(cfg.ManifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func moduleSetMonitoringModuleFactory(t *testing.T) monitoringsdk.Factory {
	t.Helper()
	return monitoringmodule.NewFactory(monitoringmodule.Options{})
}

func moduleSetMonitoringSaaSFactory(t *testing.T) monitoringsdk.Factory {
	t.Helper()
	service, err := monitoringserver.New(monitoringserver.Options{BearerToken: "module-set-secret"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.Routes())
	t.Cleanup(server.Close)
	source, err := monitoringcapability.Open(monitoringcapability.Inputs{})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := source.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return monitoringremote.NewFactory(monitoringremote.Config{
		Endpoint: server.URL, Token: "module-set-secret", Client: server.Client(),
		CapabilityContractSHA256: summary.Identity.ContractSHA256,
	})
}

func loadModuleSetLock(t *testing.T) moduleSetLock {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(moduleSetRepositoryRoot(t), "config", "runtime-module-set.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lock moduleSetLock
	if err := json.Unmarshal(raw, &lock); err != nil {
		t.Fatal(err)
	}
	return lock
}

func moduleSetRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("resolve module-set test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
}

func readModuleSetGoSums(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 || strings.HasSuffix(fields[1], "/go.mod") {
			continue
		}
		values[fields[0]+" "+fields[1]] = fields[2]
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

func moduleSetLockSHA256(lock moduleSetLock) string {
	modules := append([]moduleSetLockModule(nil), lock.Modules...)
	sort.Slice(modules, func(i, j int) bool { return modules[i].Key < modules[j].Key })
	digest := sha256.New()
	_, _ = fmt.Fprintln(digest, lock.ContractVersion)
	for _, module := range modules {
		_, _ = fmt.Fprintf(digest, "%s\n%s@%s#%s\n%s@%s#%s\n%s\n",
			module.Key,
			module.Implementation, module.ImplementationVersion, module.ImplementationSum,
			module.SDK, module.SDKVersion, module.SDKSum,
			module.CapabilityContractSHA256,
		)
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}
