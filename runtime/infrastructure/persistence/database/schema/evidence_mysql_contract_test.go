package schema

import (
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
)

func TestAutomationEvidenceUsesBoundedMySQLCompositeIndexColumns(t *testing.T) {
	engine := mysql.NewEngine()
	store := &schemaCaptureStore{
		db: &schemaCaptureDB{}, driver: "mysql", profile: engine,
		renderer: engine.SQLDialect().WithSchema(""),
	}
	if err := EnsureEvidenceSchemaFor(t.Context(), store, EvidenceSchemaCapabilities{Automation: true}); err != nil {
		t.Fatal(err)
	}
	var automationDDL string
	for _, statement := range store.db.statements {
		if strings.Contains(statement, "`_automation_runs`") {
			automationDDL = statement
			break
		}
	}
	if automationDDL == "" {
		t.Fatal("automation schema DDL was not emitted")
	}
	for _, fragment := range []string{
		"`workspace_id` VARCHAR(128) NOT NULL",
		"`run_kind` VARCHAR(32) NOT NULL",
		"`rule_key` VARCHAR(128) NOT NULL",
		"`object_key` VARCHAR(128) NOT NULL",
		"`record_id` VARCHAR(191) NOT NULL",
		"`status` VARCHAR(64) NOT NULL",
		"`created_at` VARCHAR(40) NOT NULL",
	} {
		if !strings.Contains(automationDDL, fragment) {
			t.Fatalf("automation MySQL DDL omitted bounded index column %q: %s", fragment, automationDDL)
		}
	}
}
