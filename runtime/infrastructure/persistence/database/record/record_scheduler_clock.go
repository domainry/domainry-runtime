package record

import (
	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"fmt"
	"math"

	"strconv"
	"strings"
	"time"
)

func (r RecordStore) SchedulerNow(ctx context.Context) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	database := r.database()
	if database == nil {
		return time.Time{}, fmt.Errorf("record database is unavailable")
	}
	if r.store == nil {
		return time.Time{}, fmt.Errorf("record database profile is unavailable")
	}
	queryValue := r.store.RuntimeProfile().DatabaseCurrentTimeQuery()
	var raw any
	if err := database.QueryRowContext(ctx, queryValue.Statement, queryValue.Arguments...).Scan(&raw); err != nil {
		return time.Time{}, fmt.Errorf("read database current timestamp: %w", err)
	}
	switch value := raw.(type) {
	case time.Time:
		return value.UTC(), nil
	case int64:
		return time.Unix(value, 0).UTC(), nil
	case float64:
		seconds, fraction := math.Modf(value)
		return time.Unix(int64(seconds), int64(fraction*float64(time.Second))).UTC(), nil
	case string:
		return parseSchedulerDatabaseTimeOrEpoch(value)
	case []byte:
		return parseSchedulerDatabaseTimeOrEpoch(string(value))
	default:
		return time.Time{}, fmt.Errorf("unsupported database timestamp type %T", raw)
	}
}

func parseSchedulerDatabaseTimeOrEpoch(value string) (time.Time, error) {
	if epoch, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
		seconds, fraction := math.Modf(epoch)
		return time.Unix(int64(seconds), int64(fraction*float64(time.Second))).UTC(), nil
	}
	return parseSchedulerDatabaseTime(value)
}

func parseSchedulerDatabaseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("parse database current timestamp %q", value)
}

func (r RecordStore) ListDueRecordTimerWorkspaces(ctx context.Context, object definitionmodel.ObjectSchema, now time.Time) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(object.Key) == "" {
		return nil, fmt.Errorf("record workspace object key is required")
	}
	s := r.store
	nowValue := now.UTC().Format(time.RFC3339Nano)
	predicate := query.Or(
		query.And(query.Equal("status", "scheduled"), query.LessThanOrEqual("due_at", nowValue)),
		query.And(query.Equal("status", "leased"), query.LessThanOrEqual("due_at", nowValue), query.LessThanOrEqual("lease_expires_at", nowValue)),
	)
	queryValue, args, buildErr := query.NewSelectBuilder(s.SQLRenderer, object.Key).Columns("workspace_id").Distinct().Where(predicate).OrderBy(query.Ascending("workspace_id")).Build()
	if buildErr != nil {
		return nil, buildErr
	}
	rows, err := r.database().QueryContext(ctx, queryValue, args...)
	if err != nil {
		return nil, fmt.Errorf("list record workspaces: %w", err)
	}
	defer rows.Close()
	workspaces := []string{}
	for rows.Next() {
		var workspaceID string
		if err := rows.Scan(&workspaceID); err != nil {
			return nil, err
		}
		if workspaceID = strings.TrimSpace(workspaceID); workspaceID != "" {
			workspaces = append(workspaces, workspaceID)
		}
	}
	return workspaces, rows.Err()
}
