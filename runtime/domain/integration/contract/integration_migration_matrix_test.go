package integrationcontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type migrationMatrix struct {
	ConnectorCount int                    `json:"connector_count"`
	Entries        []migrationMatrixEntry `json:"entries"`
}

type migrationMatrixEntry struct {
	ConnectorKey         string   `json:"connector_key"`
	Classification       string   `json:"classification"`
	Readiness            string   `json:"readiness"`
	TestStatus           string   `json:"test_status"`
	RetirementStatus     string   `json:"retirement_status"`
	RetiredTemplateFiles []string `json:"retired_template_files"`
}

func TestMigrationMatrixProvesAllLegacyConnectorsResolved(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "..", ".."))
	raw, err := os.ReadFile(filepath.Join("testdata", "runtime-connector-migration-matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	var matrix migrationMatrix
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatal(err)
	}
	connectors, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	if matrix.ConnectorCount != len(connectors) || len(matrix.Entries) != len(connectors) {
		t.Fatalf("matrix count=%d entries=%d catalog=%d", matrix.ConnectorCount, len(matrix.Entries), len(connectors))
	}
	entries := map[string]migrationMatrixEntry{}
	for _, entry := range matrix.Entries {
		if entry.ConnectorKey == "" || entries[entry.ConnectorKey].ConnectorKey != "" {
			t.Fatalf("empty or duplicate matrix key %q", entry.ConnectorKey)
		}
		entries[entry.ConnectorKey] = entry
		if entry.RetirementStatus != "retired_runtime_owned" {
			t.Fatalf("connector %s retirement=%q", entry.ConnectorKey, entry.RetirementStatus)
		}
		if entry.Readiness == "catalog_only" || strings.Contains(entry.TestStatus, "pending") {
			t.Fatalf("connector %s unresolved readiness=%q test_status=%q", entry.ConnectorKey, entry.Readiness, entry.TestStatus)
		}
		for _, retired := range entry.RetiredTemplateFiles {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(retired))); !os.IsNotExist(err) {
				t.Fatalf("connector %s retired template still exists: %s", entry.ConnectorKey, retired)
			}
		}
	}
	for _, connector := range connectors {
		entry, ok := entries[connector.Key]
		if !ok {
			t.Fatalf("catalog connector %s missing from matrix", connector.Key)
		}
		classification := connector.Classification
		if classification == "" {
			classification = "external_connector"
		}
		if entry.Classification != classification {
			t.Fatalf("connector %s matrix classification=%q catalog=%q", connector.Key, entry.Classification, classification)
		}
		if connector.LifecycleStatus == "reclassified" && entry.Readiness != "disabled" {
			t.Fatalf("reclassified connector %s readiness=%q", connector.Key, entry.Readiness)
		}
	}
}
