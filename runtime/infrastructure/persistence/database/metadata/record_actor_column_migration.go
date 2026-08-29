package metadata

import (
	"context"
	"fmt"

	ormbuilder "github.com/domainry/domainry-orm/builder"
)

var legacyRecordActorColumns = []struct {
	legacy  string
	current string
}{
	{legacy: "create_user_id", current: "create_by"},
	{legacy: "update_user_id", current: "update_by"},
}

func (r MetadataStore) migrateLegacyRecordActorColumns(ctx context.Context, table string, existing map[string]bool) error {
	for _, actor := range legacyRecordActorColumns {
		if !existing[actor.legacy] {
			continue
		}
		if !existing[actor.current] {
			statement, args, err := ormbuilder.NewRenameColumnBuilder(r.store.SQLRenderer, table, actor.legacy, actor.current).Build()
			if err != nil {
				return fmt.Errorf("build Record actor column migration %s.%s: %w", table, actor.legacy, err)
			}
			if _, err := r.schemaDatabase().ExecContext(ctx, statement, args...); err != nil {
				return fmt.Errorf("rename Record actor column %s.%s: %w", table, actor.legacy, err)
			}
			delete(existing, actor.legacy)
			existing[actor.current] = true
			continue
		}
		merge := "UPDATE " + r.store.TableIdentifier(table) + " SET " + r.store.Identifier(actor.current) + " = COALESCE(" + r.store.Identifier(actor.current) + ", " + r.store.Identifier(actor.legacy) + ")"
		if _, err := r.schemaDatabase().ExecContext(ctx, merge); err != nil {
			return fmt.Errorf("merge Record actor column %s.%s: %w", table, actor.legacy, err)
		}
		statement, args, err := ormbuilder.NewDropColumnBuilder(r.store.SQLRenderer, table, actor.legacy).Build()
		if err != nil {
			return fmt.Errorf("build obsolete Record actor column removal %s.%s: %w", table, actor.legacy, err)
		}
		if _, err := r.schemaDatabase().ExecContext(ctx, statement, args...); err != nil {
			return fmt.Errorf("remove obsolete Record actor column %s.%s: %w", table, actor.legacy, err)
		}
		delete(existing, actor.legacy)
	}
	return nil
}
