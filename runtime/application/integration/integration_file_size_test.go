package integration

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIntegrationOwnerGoFilesStayBelowFiveHundredLines(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration owner test path")
	}
	root := filepath.Dir(current)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read integration owner directory: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		file, openErr := os.Open(path)
		if openErr != nil {
			t.Errorf("open %s: %v", path, openErr)
			continue
		}
		lines := 0
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			lines++
		}
		closeErr := file.Close()
		if scanner.Err() != nil || closeErr != nil {
			t.Errorf("read %s: scan=%v close=%v", path, scanner.Err(), closeErr)
			continue
		}
		if lines > 500 {
			t.Errorf("integration owner Go file exceeds 500 lines: %s (%d)", path, lines)
		}
	}
}
