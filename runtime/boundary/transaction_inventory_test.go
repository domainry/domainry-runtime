package boundary_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type reviewedTransactionRisk struct {
	file    string
	symbols []string
}

var reviewedTransactionRisks = map[string]reviewedTransactionRisk{
	"MW01": {file: "runtime/application/action/action_bulk_application_service.go", symbols: []string{"ExecuteBulkAction", "Complete"}},
	"MW02": {file: "runtime/application/record/record_import_application_service.go", symbols: []string{"ApplyIdempotent", "CompleteOperation"}},
}

func TestTransactionMutationInventoryIsCurrent(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	documentPath := filepath.Join(repositoryRoot, "docs", "architecture", "runtime-transaction-mutation-inventory.md")
	document := readArchitectureDocument(t, documentPath)

	for _, family := range []string{"Record CRUD", "Action", "Bulk", "Import", "Workflow decision", "Metadata publish", "ChangePlan apply/rollback", "Identity", "Notification", "Scheduler command", "Integration event"} {
		if !strings.Contains(document, "| "+family+" |") {
			t.Errorf("transaction inventory missing mutation family %q", family)
		}
	}
	for index := 1; index <= 22; index++ {
		profile := fmt.Sprintf("T%02d", index)
		if !strings.Contains(document, "| "+profile+" |") && !strings.Contains(document, "| "+profile+" ") {
			t.Errorf("transaction inventory missing detail profile %s", profile)
		}
	}
	for _, classification := range []string{"`atomic_required`", "`eventually_consistent`", "`compensatable`", "`read_only`"} {
		if !strings.Contains(document, classification) {
			t.Errorf("transaction inventory missing classification %s", classification)
		}
	}

	riskPattern := regexp.MustCompile(`(?m)^\| (MW[0-9]{2}) \|`)
	matches := riskPattern.FindAllStringSubmatch(document, -1)
	documented := make(map[string]bool, len(matches))
	for _, match := range matches {
		documented[match[1]] = true
		if _, reviewed := reviewedTransactionRisks[match[1]]; !reviewed {
			t.Errorf("transaction inventory adds unreviewed unresolved entry %s", match[1])
		}
	}
	if len(documented) > len(reviewedTransactionRisks) {
		t.Fatalf("transaction inventory unresolved baseline increased: got %d, max %d", len(documented), len(reviewedTransactionRisks))
	}

	ids := make([]string, 0, len(reviewedTransactionRisks))
	for id := range reviewedTransactionRisks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		risk := reviewedTransactionRisks[id]
		raw, err := os.ReadFile(filepath.Join(repositoryRoot, risk.file))
		if err != nil {
			t.Fatalf("read reviewed transaction risk %s source: %v", id, err)
		}
		current := true
		for _, symbol := range risk.symbols {
			if !strings.Contains(string(raw), symbol) {
				current = false
				break
			}
		}
		if current && !documented[id] {
			t.Errorf("transaction risk %s remains in %s but was removed from inventory without resolution", id, risk.file)
		}
		if !current && documented[id] {
			t.Errorf("transaction risk %s evidence no longer matches %s; remove the resolved inventory entry and tighten the reviewed baseline", id, risk.file)
		}
	}
}

func readArchitectureDocument(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestSQLTransactionOwnersContainNoExternalSideEffects(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	databaseRoot := filepath.Join(repositoryRoot, "runtime", "infrastructure", "persistence", "database")
	for _, file := range transactionGoFilesUnder(t, databaseRoot) {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		content := string(raw)
		if !strings.Contains(content, "BeginTx(") {
			continue
		}
		for _, forbidden := range []string{"\"net/http\"", "os.WriteFile(", "os.Create(", "\"os/exec\"", "exec.Command(", "exec.CommandContext(", "time.Sleep(", ".Do(request"} {
			if strings.Contains(content, forbidden) {
				t.Errorf("transaction owner %s contains forbidden external/long-running operation %q", filepath.ToSlash(strings.TrimPrefix(file, repositoryRoot+string(filepath.Separator))), forbidden)
			}
		}
	}
}

func transactionGoFilesUnder(t *testing.T, root string) []string {
	t.Helper()
	files := []string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}
