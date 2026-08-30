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
	return normalizeAuditCursorColumns(ctx, database, renderer)
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
