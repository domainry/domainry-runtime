package mysql

import (
	"testing"

	appschemastorage "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/storage"
	runtimemysql "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
)

func TestBatchAddColumnsSQLUsesOneMySQLAlterTable(t *testing.T) {
	renderer := runtimemysql.NewEngine().SQLDialect().WithSchema("")
	statement := NewApplicationSchemaStorageProfile().BatchAddColumnsSQL(renderer, "invoice", []appschemastorage.ColumnDefinition{
		{Name: "invoice_number", Type: "VARCHAR(191)"},
		{Name: "total", Type: "DECIMAL(19,4)"},
	})
	want := "ALTER TABLE `invoice` ADD COLUMN `invoice_number` VARCHAR(191), ADD COLUMN `total` DECIMAL(19,4)"
	if statement != want {
		t.Fatalf("batch add SQL = %q, want %q", statement, want)
	}
}
