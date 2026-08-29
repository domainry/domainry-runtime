package evidence

import (
	"context"
	"fmt"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

type Profile struct{}

func NewProfile() Profile { return Profile{} }

func (Profile) Types(string) persistencedriver.EvidenceSchemaTypes {
	return persistencedriver.EvidenceSchemaTypes{
		LargeText: "LONGTEXT", IdempotencyScope: "VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin",
		AuditCursor: "VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin", RetirementEngine: "VARCHAR(32)",
		RetirementNamespace: "VARCHAR(128)", RetirementKind: "VARCHAR(64)", RetirementObject: "VARCHAR(191)",
	}
}

func (Profile) Normalize(ctx context.Context, database persistencedriver.SchemaDatabase, renderer ormdialect.Renderer) error {
	if err := normalizeLargeColumns(ctx, database, renderer); err != nil {
		return err
	}
	return normalizeAuditCursorColumns(ctx, database, renderer)
}

func normalizeLargeColumns(ctx context.Context, database persistencedriver.SchemaDatabase, renderer ormdialect.Renderer) error {
	specs := map[string][]string{"report_export_artifacts": {"content_base64"}, "business_audit_export_artifacts": {"content_base64"}, "record_batch_job_chunks": {"content"}}
	query := "SELECT TABLE_NAME, COLUMN_NAME, DATA_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND ((TABLE_NAME = " + renderer.Placeholder(1) + " AND COLUMN_NAME = " + renderer.Placeholder(2) + ") OR (TABLE_NAME = " + renderer.Placeholder(3) + " AND COLUMN_NAME = " + renderer.Placeholder(4) + ") OR (TABLE_NAME = " + renderer.Placeholder(5) + " AND COLUMN_NAME = " + renderer.Placeholder(6) + "))"
	rows, err := database.QueryContext(ctx, query, "report_export_artifacts", "content_base64", "business_audit_export_artifacts", "content_base64", "record_batch_job_chunks", "content")
	if err != nil {
		return fmt.Errorf("inspect MySQL large evidence columns: %w", err)
	}
	modifications := map[string][]string{}
	for rows.Next() {
		var table, column, dataType string
		if err := rows.Scan(&table, &column, &dataType); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan MySQL large evidence column: %w", err)
		}
		if !strings.EqualFold(dataType, "longtext") {
			modifications[table] = append(modifications[table], column)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate MySQL large evidence columns: %w", err)
	}
	_ = rows.Close()
	for table, columns := range modifications {
		for _, column := range columns {
			valid := false
			for _, candidate := range specs[table] {
				valid = valid || column == candidate
			}
			if !valid {
				return fmt.Errorf("inspect MySQL large evidence columns: unexpected %s.%s", table, column)
			}
			if _, err := database.ExecContext(ctx, "ALTER TABLE "+renderer.Table(table)+" MODIFY COLUMN "+renderer.Identifier(column)+" LONGTEXT NOT NULL"); err != nil {
				return fmt.Errorf("normalize MySQL large evidence column %s.%s: %w", table, column, err)
			}
		}
	}
	return nil
}

func normalizeAuditCursorColumns(ctx context.Context, database persistencedriver.SchemaDatabase, renderer ormdialect.Renderer) error {
	const cursorType = "VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin"
	specs := []struct{ name, nullability string }{{"id", "NOT NULL"}, {"created_at", "NOT NULL"}}
	query := "SELECT COLUMN_NAME, COLUMN_TYPE, CHARACTER_SET_NAME, COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = " + renderer.Placeholder(1) + " AND COLUMN_NAME IN (" + renderer.Placeholder(2) + ", " + renderer.Placeholder(3) + ")"
	rows, err := database.QueryContext(ctx, query, "_audit_events", specs[0].name, specs[1].name)
	if err != nil {
		return fmt.Errorf("inspect MySQL audit cursor columns: %w", err)
	}
	type columnState struct{ columnType, characterSet, collation string }
	states := map[string]columnState{}
	for rows.Next() {
		var name string
		var state columnState
		if err := rows.Scan(&name, &state.columnType, &state.characterSet, &state.collation); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan MySQL audit cursor column: %w", err)
		}
		states[name] = state
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate MySQL audit cursor columns: %w", err)
	}
	_ = rows.Close()
	modifications := []string{}
	for _, spec := range specs {
		state, exists := states[spec.name]
		if !exists {
			return fmt.Errorf("inspect MySQL audit cursor columns: %s is missing", spec.name)
		}
		if strings.EqualFold(state.columnType, "varchar(191)") && strings.EqualFold(state.characterSet, "ascii") && strings.EqualFold(state.collation, "ascii_bin") {
			continue
		}
		modifications = append(modifications, "MODIFY COLUMN "+renderer.Identifier(spec.name)+" "+cursorType+" "+spec.nullability)
	}
	if len(modifications) == 0 {
		return nil
	}
	if _, err := database.ExecContext(ctx, "ALTER TABLE "+renderer.Table("_audit_events")+" "+strings.Join(modifications, ", ")); err != nil {
		return fmt.Errorf("normalize MySQL audit cursor columns: %w", err)
	}
	return nil
}
