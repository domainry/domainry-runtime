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
