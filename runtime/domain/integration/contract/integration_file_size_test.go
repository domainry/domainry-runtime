package integrationcontract

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestConnectorGoFilesStayBelowFiveHundredLines(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve connector catalog test path")
	}
	connectorRoot := filepath.Clean(filepath.Join(filepath.Dir(current), ".."))
	err := filepath.WalkDir(connectorRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		defer file.Close()
		lines := 0
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			lines++
		}
		if scanErr := scanner.Err(); scanErr != nil {
			return scanErr
		}
		if lines > 500 {
			t.Errorf("connector Go file exceeds 500 lines: %s (%d)", path, lines)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk connector Go files: %v", err)
	}
}

func TestConnectorsDoNotStartUnownedGoroutines(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve connector catalog test path")
	}
	connectorRoot := filepath.Clean(filepath.Join(filepath.Dir(current), ".."))
	goStatement := regexp.MustCompile(`^\s*go\s+`)
	err := filepath.WalkDir(connectorRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return walkErr
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		lines := strings.Split(string(content), "\n")
		for index, line := range lines {
			if !goStatement.MatchString(line) {
				continue
			}
			owned := index > 0 && strings.Contains(lines[index-1], "connector-goroutine-owned:")
			if !owned && index > 1 {
				owned = strings.Contains(lines[index-2], "connector-goroutine-owned:")
			}
			if !owned {
				t.Errorf("connector starts an unowned goroutine: %s:%d", path, index+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk connector Go files: %v", err)
	}
}
