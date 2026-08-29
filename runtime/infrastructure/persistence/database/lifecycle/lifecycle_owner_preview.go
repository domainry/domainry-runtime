package lifecycle

import (
	"context"
	"database/sql"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
)

func (e OwnerExecutor) previewSpec(ctx context.Context, workspaceID string, spec cleanupSpec, policyKey string, cutoff time.Time) (int64, time.Time, error) {
	const candidateAlias = "candidate"
	predicate := cleanupPredicate(spec, cutoff, candidateAlias)
	archivePredicate := ormbuilder.And(
		ormbuilder.Equal("source_table", spec.table),
		ormbuilder.EqualExpressions(ormbuilder.QualifiedColumn("archive", "resource_id"), ormbuilder.QualifiedColumn(candidateAlias, spec.idColumn)),
		ormbuilder.Equal("policy_key", policyKey),
	)
	if spec.tenantColumn != "" {
		archivePredicate = ormbuilder.And(archivePredicate, ormbuilder.EqualExpressions(ormbuilder.QualifiedColumn("archive", "workspace_id"), ormbuilder.QualifiedColumn(candidateAlias, spec.tenantColumn)))
	} else {
		archivePredicate = ormbuilder.And(archivePredicate, ormbuilder.Equal("workspace_id", workspaceID))
	}
	archive := ormbuilder.NewSelectBuilder(e.store.SQLRenderer, "lifecycle_archive_entries").Alias("archive").Columns("id").Where(archivePredicate)
	predicate = ormbuilder.And(predicate, ormbuilder.NotExistsSubquery(archive))
	builder := ormbuilder.NewSelectBuilder(e.store.SQLRenderer, spec.table).Alias(candidateAlias).Projections(ormbuilder.Project(ormbuilder.CountAll()), ormbuilder.Project(ormbuilder.Min(ormbuilder.Column(spec.timeColumn))))
	if spec.tenantColumn != "" {
		builder = ormbuilder.NewWorkspaceSelectBuilder(e.store.SQLRenderer, spec.table, workspaceID).Alias(candidateAlias).Projections(ormbuilder.Project(ormbuilder.CountAll()), ormbuilder.Project(ormbuilder.Min(ormbuilder.Column(spec.timeColumn))))
	}
	query, args, buildErr := builder.Where(predicate).Build()
	if buildErr != nil {
		return 0, time.Time{}, buildErr
	}
	var count int64
	parsed := time.Time{}
	if spec.unixNanoTime {
		var oldest sql.NullInt64
		if err := e.database().QueryRowContext(ctx, query, args...).Scan(&count, &oldest); err != nil {
			return 0, time.Time{}, err
		}
		if oldest.Valid {
			parsed = time.Unix(0, oldest.Int64).UTC()
		}
	} else {
		var oldest sql.NullString
		if err := e.database().QueryRowContext(ctx, query, args...).Scan(&count, &oldest); err != nil {
			return 0, time.Time{}, err
		}
		if oldest.Valid {
			parsed, _ = time.Parse(time.RFC3339Nano, oldest.String)
		}
	}
	return count, parsed, nil
}
