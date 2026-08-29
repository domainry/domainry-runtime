package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func (s LifecycleStore) Metrics(ctx context.Context, workspaceID string, now time.Time) (lifecyclemodel.Metrics, error) {
	metrics := lifecyclemodel.Metrics{}
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "lifecycle_cleanup_jobs", workspaceID).Projections(ormbuilder.Project(ormbuilder.CountAll()), ormbuilder.Project(ormbuilder.Min(ormbuilder.Column("updated_at")))).Where(ormbuilder.In("status", lifecyclemodel.CleanupStatusPending, lifecyclemodel.CleanupStatusPaused, lifecyclemodel.CleanupStatusFailed)).Build()
	if buildErr != nil {
		return metrics, buildErr
	}
	var oldest sql.NullString
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&metrics.EligibleBacklog, &oldest); err != nil {
		return metrics, err
	}
	if oldest.Valid {
		metrics.OldestEligible, _ = time.Parse(time.RFC3339Nano, oldest.String)
	}
	query, args, buildErr = ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "lifecycle_legal_holds", workspaceID).Projections(ormbuilder.Project(ormbuilder.CountAll())).Where(ormbuilder.And(ormbuilder.LessThanOrEqual("starts_at", lifecycleTime(now)), ormbuilder.Or(ormbuilder.Equal("ends_at", ""), ormbuilder.GreaterThan("ends_at", lifecycleTime(now))))).Build()
	if buildErr != nil {
		return metrics, buildErr
	}
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&metrics.LegalHoldCount); err != nil {
		return metrics, err
	}
	query, args, buildErr = ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "lifecycle_audit_evidence", workspaceID).Columns("event", "payload_json").Where(ormbuilder.In("event", "lifecycle.cleanup.succeeded", "lifecycle.cleanup.failed")).Build()
	if buildErr != nil {
		return metrics, buildErr
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return metrics, err
	}
	defer rows.Close()
	for rows.Next() {
		var event, payload string
		if err := rows.Scan(&event, &payload); err != nil {
			return metrics, err
		}
		if event == "lifecycle.cleanup.failed" {
			metrics.FailureTotal++
			continue
		}
		var evidence lifecyclemodel.AuditEvidence
		var job lifecyclemodel.CleanupJob
		if json.Unmarshal([]byte(payload), &evidence) == nil && json.Unmarshal(evidence.Payload, &job) == nil {
			metrics.PurgedTotal += job.Purged
		}
	}
	if err := rows.Err(); err != nil {
		return metrics, err
	}
	metrics.Warning = metrics.FailureTotal > 0 || metrics.EligibleBacklog > 1000 || (!metrics.OldestEligible.IsZero() && now.Sub(metrics.OldestEligible) > 24*time.Hour)
	return metrics, nil
}

func lifecycleTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseLifecycleTime(value string) time.Time {
	result, _ := time.Parse(time.RFC3339Nano, value)
	return result
}

func lifecycleColumns(store *database.RuntimeStore, columns ...string) string {
	result := ""
	for index, column := range columns {
		if index > 0 {
			result += ", "
		}
		result += store.Identifier(column)
	}
	return result
}
