package database

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestProductionSQLUsesTableIdentifierAfterRelationKeywords(t *testing.T) {
	root := "."
	unqualified := regexp.MustCompile(`(?i)(?:INSERT(?: OR REPLACE)? INTO|UPDATE|DELETE FROM|FROM|JOIN|CREATE TABLE IF NOT EXISTS|ALTER TABLE|REFERENCES|ON|DROP TABLE|TRUNCATE)\s*"\s*\+\s*[A-Za-z_][A-Za-z0-9_.]*\.(?:Identifier|identifier)\(`)
	violations := []string{}
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
		if productionSQLHasUnqualifiedRelation(raw, unqualified) {
			violations = append(violations, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("production SQL relation references must use TableIdentifier: %s", strings.Join(violations, ", "))
	}
}

func TestProductionSQLRelationInventoryDistinguishesUpsertUpdateFromRelationUpdate(t *testing.T) {
	pattern := regexp.MustCompile(`(?i)(?:INSERT(?: OR REPLACE)? INTO|UPDATE|DELETE FROM|FROM|JOIN|CREATE TABLE IF NOT EXISTS|ALTER TABLE|REFERENCES|ON|DROP TABLE|TRUNCATE)\s*"\s*\+\s*[A-Za-z_][A-Za-z0-9_.]*\.(?:Identifier|identifier)\(`)
	for _, valid := range [][]byte{
		[]byte(`query + " ON DUPLICATE KEY UPDATE " + store.Identifier("value")`),
		[]byte(`query + " ON CONFLICT (key) DO UPDATE SET " + store.Identifier("value")`),
	} {
		if productionSQLHasUnqualifiedRelation(valid, pattern) {
			t.Fatalf("upsert column update was classified as a relation reference: %s", valid)
		}
	}
	if invalid := []byte(`query := "UPDATE " + store.Identifier("records")`); !productionSQLHasUnqualifiedRelation(invalid, pattern) {
		t.Fatal("bare UPDATE relation escaped the TableIdentifier inventory")
	}
}

func productionSQLHasUnqualifiedRelation(raw []byte, pattern *regexp.Regexp) bool {
	source := strings.NewReplacer(
		"ON DUPLICATE KEY UPDATE", "ON DUPLICATE KEY UPSERT",
		"DO UPDATE SET", "DO UPSERT SET",
	).Replace(string(raw))
	return pattern.MatchString(source)
}
