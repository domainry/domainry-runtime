package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s LifecycleStore) GlobalMetrics(ctx context.Context, scope principalmodel.SystemScope, now time.Time) (lifecyclemodel.Metrics, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return lifecyclemodel.Metrics{}, err
	}
	metrics := lifecyclemodel.Metrics{}
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, "lifecycle_cleanup_jobs").Projections(
		ormbuilder.Project(ormbuilder.CountAll()), ormbuilder.Project(ormbuilder.Min(ormbuilder.Column("updated_at"))),
	).Where(ormbuilder.In("status", lifecyclemodel.CleanupStatusPending, lifecyclemodel.CleanupStatusPaused, lifecyclemodel.CleanupStatusFailed)).Build()
	if buildErr != nil {
		return metrics, fmt.Errorf("build lifecycle cleanup metrics query: %w", buildErr)
	}
	var oldest sql.NullString
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&metrics.EligibleBacklog, &oldest); err != nil {
		return metrics, err
	}
	if oldest.Valid {
		metrics.OldestEligible = parseLifecycleTime(oldest.String)
	}
	query, args, buildErr = ormbuilder.NewSelectBuilder(s.store.SQLRenderer, "lifecycle_legal_holds").Projections(ormbuilder.Project(ormbuilder.CountAll())).Where(ormbuilder.And(
		ormbuilder.LessThanOrEqual("starts_at", lifecycleTime(now)), ormbuilder.Or(ormbuilder.Equal("ends_at", ""), ormbuilder.GreaterThan("ends_at", lifecycleTime(now))),
	)).Build()
	if buildErr != nil {
		return metrics, fmt.Errorf("build lifecycle legal hold metrics query: %w", buildErr)
	}
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&metrics.LegalHoldCount); err != nil {
		return metrics, err
	}
	query, args, buildErr = ormbuilder.NewSelectBuilder(s.store.SQLRenderer, "lifecycle_audit_evidence").Columns("event", "payload_json").Where(
		ormbuilder.In("event", "lifecycle.cleanup.succeeded", "lifecycle.cleanup.failed"),
	).Build()
	if buildErr != nil {
		return metrics, fmt.Errorf("build lifecycle audit metrics query: %w", buildErr)
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
