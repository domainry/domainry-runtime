package record

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestRecordPersistenceForbidsOffsetPagination(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve record pagination source")
	}
	raw, err := os.ReadFile(strings.TrimSuffix(source, "record_pagination_boundary_test.go") + "record_store.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToUpper(string(raw)), " OFFSET ") {
		t.Fatal("workspace record persistence must use an ID cursor instead of OFFSET")
	}
}

func TestActionRecordQueryContractUsesStableIDCursor(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve record pagination source")
	}
	runtimeRoot := strings.TrimSuffix(source, "runtime/infrastructure/persistence/database/record/record_pagination_boundary_test.go")
	for _, relative := range []string{"pkg/runtimeext/transaction.go", "runtime/application/action/action_executor.go"} {
		raw, err := os.ReadFile(runtimeRoot + relative)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if strings.Contains(text, "Offset") || strings.Contains(text, ".Offset") {
			t.Errorf("Action record query must not expose offset pagination: %s", relative)
		}
		if !strings.Contains(text, "AfterID") {
			t.Errorf("Action record query lost required stable ID cursor: %s", relative)
		}
	}
}
