package boundary_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type retirementInventory struct {
	DatabaseObjects   []retirementDatabaseObject `json:"database_objects"`
	LegacyCodeObjects []retirementCodeObject     `json:"legacy_code_objects"`
}

type retirementDynamicAllowlist struct {
	Entries []struct {
		Kind                string `json:"kind"`
		Owner               string `json:"owner"`
		Path                string `json:"path"`
		Scope               string `json:"scope"`
		Guard               string `json:"guard"`
		RetiredObjectAccess bool   `json:"retired_object_access"`
	} `json:"entries"`
}

type retirementDatabaseObject struct {
	ObjectKind        string   `json:"object_kind"`
	Database          string   `json:"database"`
	Schema            string   `json:"schema"`
	ObjectNames       []string `json:"object_names"`
	Owner             string   `json:"owner"`
	Replacement       string   `json:"replacement"`
	ReadPaths         []string `json:"read_paths"`
	WritePaths        []string `json:"write_paths"`
	Migration         string   `json:"migration"`
	Retention         string   `json:"retention"`
	BackupID          string   `json:"backup_id"`
	ObservationWindow string   `json:"observation_window"`
	ForbiddenPatterns []string `json:"forbidden_patterns"`
	Status            string   `json:"status"`
}

type retirementCodeObject struct {
	ObjectKind        string   `json:"object_kind"`
	ObjectName        string   `json:"object_name"`
	Owner             string   `json:"owner"`
	Replacement       string   `json:"replacement"`
	Migration         string   `json:"migration"`
	Retention         string   `json:"retention"`
	BackupID          string   `json:"backup_id"`
	ObservationWindow string   `json:"observation_window"`
	Status            string   `json:"status"`
	ForbiddenPatterns []string `json:"forbidden_patterns"`
}

func TestRuntimeDatabaseRetirementInventoryCoversFreshSchema(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	inventory := readRetirementInventory(t, repositoryRoot)
	registered := map[string]bool{}
	allowedStatuses := map[string]bool{"active": true, "migration_only": true, "compatibility_read": true, "compatibility_write": true, "retirement_candidate": true, "blocked": true}
	for _, group := range inventory.DatabaseObjects {
		if group.ObjectKind != "table" || group.Database == "" || group.Schema == "" || group.Owner == "" || group.Migration == "" || group.Retention == "" || group.BackupID == "" || group.ObservationWindow == "" || !allowedStatuses[group.Status] {
			t.Errorf("incomplete database retirement inventory group: %+v", group)
		}
		if len(group.ReadPaths) == 0 || len(group.WritePaths) == 0 {
			t.Errorf("database retirement inventory group %s has no reader/writer evidence", group.Owner)
		}
		for _, name := range group.ObjectNames {
			if name == "" || registered[name] {
				t.Errorf("database retirement inventory has empty or duplicate object %q", name)
			}
			registered[name] = true
		}
		if group.Status == "retirement_candidate" {
			if len(group.ForbiddenPatterns) == 0 {
				t.Errorf("retirement candidate %s has no production-code resurrection guard", group.Owner)
			}
			for _, pattern := range group.ForbiddenPatterns {
				assertProductionGoDoesNotContain(t, filepath.Join(repositoryRoot, "runtime"), pattern)
			}
		}
	}

	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "retirement-inventory.db"), IntegrationSecretKey: "retirement-inventory-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := store.DB().QueryContext(t.Context(), "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		if !registered[table] {
			t.Errorf("Runtime schema table %s is missing from database retirement inventory", table)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestRetiredRuntimeDatabaseCodeCannotReturn(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	inventory := readRetirementInventory(t, repositoryRoot)
	allowedStatuses := map[string]bool{"active": true, "migration_only": true, "compatibility_read": true, "compatibility_write": true, "retirement_candidate": true, "blocked": true, "code_removed": true}
	for _, object := range inventory.LegacyCodeObjects {
		if object.ObjectKind == "" || object.ObjectName == "" || object.Owner == "" || object.Replacement == "" || object.Migration == "" || object.Retention == "" || object.BackupID == "" || object.ObservationWindow == "" || !allowedStatuses[object.Status] {
			t.Errorf("incomplete Legacy Persistence inventory object: %+v", object)
		}
		if object.Status != "code_removed" {
			continue
		}
		if object.ObjectKind == "go_file" || object.ObjectKind == "go_package" {
			if _, err := os.Stat(filepath.Join(repositoryRoot, filepath.FromSlash(object.ObjectName))); !os.IsNotExist(err) {
				t.Errorf("retired Runtime database object path returned: %s", object.ObjectName)
			}
		}
		for _, pattern := range object.ForbiddenPatterns {
			assertProductionGoDoesNotContain(t, filepath.Join(repositoryRoot, "runtime"), pattern)
		}
	}
}

func TestRetiredRuntimeDatabaseExternalContractsCannotReturn(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	contractRoots := []string{
		"runtime/platform/config",
		"runtime/bootstrap",
		"runtime/transport/http/openapi",
		"scripts",
	}
	forbiddenPatterns := []string{
		"LEGACY_DATABASE",
		"LEGACY_DB",
		"DATABASE_LEGACY",
		"DB_LEGACY",
		"LegacyDatabase",
		"legacyDatabase",
		"legacy_database",
		"DeprecatedDatabase",
		"deprecatedDatabase",
		"deprecated_database",
		"/legacy-database",
		"/deprecated-database",
	}
	for _, root := range contractRoots {
		for _, pattern := range forbiddenPatterns {
			assertFilesDoNotContain(t, filepath.Join(repositoryRoot, filepath.FromSlash(root)), pattern)
		}
	}
}

func TestRuntimeDynamicDatabaseReferencesAreExplicitlyAllowlisted(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	raw, err := os.ReadFile(filepath.Join(repositoryRoot, "docs", "architecture", "runtime-database-dynamic-reference-allowlist.json"))
	if err != nil {
		t.Fatal(err)
	}
	var allowlist retirementDynamicAllowlist
	if err := json.Unmarshal(raw, &allowlist); err != nil {
		t.Fatal(err)
	}
	allowedKinds := map[string]bool{"manifest_object_table": true, "report_query": true, "migration_catalog": true, "lazy_schema": true}
	seen := map[string]bool{}
	for _, entry := range allowlist.Entries {
		if !allowedKinds[entry.Kind] || entry.Owner == "" || entry.Path == "" || entry.Scope == "" || entry.Guard == "" || entry.RetiredObjectAccess || seen[entry.Path] {
			t.Errorf("invalid dynamic database allowlist entry: %+v", entry)
		}
		seen[entry.Path] = true
		if info, statErr := os.Stat(filepath.Join(repositoryRoot, filepath.FromSlash(entry.Path))); statErr != nil || !info.IsDir() {
			t.Errorf("dynamic database allowlist path is not a directory: %s err=%v", entry.Path, statErr)
		}
	}
	if len(seen) != 4 {
		t.Errorf("dynamic database allowlist entries=%d want=4", len(seen))
	}
}

func readRetirementInventory(t *testing.T, repositoryRoot string) retirementInventory {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repositoryRoot, "docs", "architecture", "runtime-database-retirement-inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory retirementInventory
	if err := json.Unmarshal(raw, &inventory); err != nil {
		t.Fatal(err)
	}
	return inventory
}

func assertProductionGoDoesNotContain(t *testing.T, root, pattern string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), pattern) {
			t.Errorf("retired Runtime database reference %q returned in %s", pattern, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertFilesDoNotContain(t *testing.T, root, pattern string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		switch filepath.Ext(path) {
		case ".go", ".json", ".sh", ".yaml", ".yml":
		default:
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), pattern) {
			t.Errorf("retired Runtime database external contract %q returned in %s", pattern, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
