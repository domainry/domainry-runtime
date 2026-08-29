package audit

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditPersistenceRemainsBuilderOnlyAndDialectNeutral(t *testing.T) {
	forbidden := []string{
		`"SELECT `,
		`"INSERT `,
		`"UPDATE `,
		`"DELETE `,
		`Driver() ==`,
		`Driver() !=`,
		`switch driver`,
	}
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, token := range forbidden {
			if strings.Contains(string(source), token) {
				t.Errorf("audit persistence %s reintroduced forbidden SQL architecture token %q", path, token)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
