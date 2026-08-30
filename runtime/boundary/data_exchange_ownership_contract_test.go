package boundary

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataExchangeEngineeringOwnershipDoesNotLeakBackIntoRuntime(t *testing.T) {
	root := filepath.Join("..", "..")
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, implementationModule := range []string{
		"github.com/domainry/domainry-data-exchange ",
		"github.com/domainry/domainry-data-exchange/",
	} {
		if strings.Contains(string(goMod), implementationModule) {
			t.Fatalf("Runtime dependency graph contains Data Exchange implementation %q; depend on the SDK only", implementationModule)
		}
	}

	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(content)
		if strings.Contains(text, "github.com/domainry/domainry-data-exchange/") {
			t.Errorf("Runtime production code imports Data Exchange implementation: %s", path)
		}
		for _, table := range []string{"_data_exchange_jobs", "_data_exchange_job_chunks", "_data_exchange_artifacts", "_data_exchange_queue_scopes"} {
			if strings.Contains(text, table) {
				t.Errorf("Runtime production code contains Data Exchange-owned table %q: %s", table, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	providerPath := filepath.Join(root, "runtime", "application", "record", "record_data_exchange_providers.go")
	content, err := os.ReadFile(providerPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, duplicate := range []string{"DecodeCSV", "NewCSVEncoder", "bytes.NewReader"} {
		if strings.Contains(string(content), duplicate) {
			t.Errorf("Record Data Exchange provider still performs CSV round-trip via %s", duplicate)
		}
	}
}
