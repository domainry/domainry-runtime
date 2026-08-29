package datamigration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
)

type FinalizeReport struct {
	IndexesCreated     []string `json:"indexes_created,omitempty"`
	ForeignKeysCreated []string `json:"foreign_keys_created,omitempty"`
}

func (c Copier) Finalize(ctx context.Context) (FinalizeReport, error) {
	verification, err := c.Verify(ctx)
	if err != nil {
		return FinalizeReport{}, err
	}
	if !verification.Current {
		return FinalizeReport{}, fmt.Errorf("data migration cannot finalize before row-count and digest verification")
	}
	report := FinalizeReport{}
	for _, table := range c.Plan.Tables {
		if !ormdialect.ValidIdentifier(table.Name) {
			return report, fmt.Errorf("unsafe deferred table identifier %q", table.Name)
		}
		for _, index := range table.DeferredIndexes {
			if !ormdialect.ValidIdentifier(index.Name) {
				return report, fmt.Errorf("unsafe deferred index identifier %q", index.Name)
			}
			for _, column := range index.Columns {
				if !ormdialect.ValidIdentifier(column) {
					return report, fmt.Errorf("unsafe deferred index column identifier")
				}
			}
			unique := ""
			if index.Unique {
				unique = "UNIQUE "
			}
			statement := "CREATE " + unique + "INDEX IF NOT EXISTS " + quote(index.Name) + " ON " + quote(c.TargetSchema) + "." + quote(table.Name) + " (" + joinQuoted(index.Columns) + ")"
			if _, err := c.Target.ExecContext(ctx, statement); err != nil {
				return report, fmt.Errorf("create deferred index %s: %w", index.Name, err)
			}
			report.IndexesCreated = append(report.IndexesCreated, table.Name+"."+index.Name)
		}
		for position, foreign := range table.DeferredForeignKeys {
			if !ormdialect.ValidIdentifier(foreign.ReferencedTable) {
				return report, fmt.Errorf("unsafe deferred referenced table identifier %q", foreign.ReferencedTable)
			}
			for _, column := range append(append([]string(nil), foreign.Columns...), foreign.ReferencedColumns...) {
				if !ormdialect.ValidIdentifier(column) {
					return report, fmt.Errorf("unsafe deferred foreign-key column identifier")
				}
			}
			name := migratedForeignKeyName(table.Name, position)
			var exists bool
			if err := c.Target.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_constraint con JOIN pg_class relation ON relation.oid = con.conrelid JOIN pg_namespace namespace ON namespace.oid = relation.relnamespace WHERE namespace.nspname = $1 AND relation.relname = $2 AND con.conname = $3 AND con.contype = 'f')`, c.TargetSchema, table.Name, name).Scan(&exists); err != nil {
				return report, err
			}
			if exists {
				continue
			}
			statement := "ALTER TABLE " + quote(c.TargetSchema) + "." + quote(table.Name) + " ADD CONSTRAINT " + quote(name) + " FOREIGN KEY (" + joinQuoted(foreign.Columns) + ") REFERENCES " + quote(c.TargetSchema) + "." + quote(foreign.ReferencedTable) + " (" + joinQuoted(foreign.ReferencedColumns) + ") NOT VALID"
			if _, err := c.Target.ExecContext(ctx, statement); err != nil {
				return report, fmt.Errorf("create deferred foreign key %s: %w", name, err)
			}
			if _, err := c.Target.ExecContext(ctx, "ALTER TABLE "+quote(c.TargetSchema)+"."+quote(table.Name)+" VALIDATE CONSTRAINT "+quote(name)); err != nil {
				return report, fmt.Errorf("validate deferred foreign key %s: %w", name, err)
			}
			report.ForeignKeysCreated = append(report.ForeignKeysCreated, table.Name+"."+name)
		}
	}
	return report, nil
}

func migratedForeignKeyName(table string, position int) string {
	base := fmt.Sprintf("migrated_fk_%s_%d", table, position+1)
	if len(base) <= 63 {
		return base
	}
	sum := sha256.Sum256([]byte(base))
	suffix := hex.EncodeToString(sum[:4])
	return strings.TrimSuffix(base[:54], "_") + "_" + suffix
}
