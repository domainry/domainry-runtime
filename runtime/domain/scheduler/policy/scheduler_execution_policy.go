package policy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func SchedulerLimit(limit int) int {
	if limit <= 0 {
		return 25
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func SchedulerNextAttempt(run recordmodel.Record, status string) int {
	current := SchedulerInt(run.Data["attempt"], 0)
	if status == "queued" || status == "leased" {
		if current <= 0 {
			return 1
		}
		return current
	}
	return current + 1
}

func SchedulerLeaseExpired(run recordmodel.Record, now time.Time) bool {
	raw := strings.TrimSpace(fmt.Sprint(run.Data["lease_expires_at"]))
	if raw == "" || raw == "<nil>" {
		return true
	}
	expiresAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return true
	}
	return !expiresAt.After(now)
}

func SchedulerRetryDue(run recordmodel.Record, now time.Time) bool {
	raw := strings.TrimSpace(fmt.Sprint(run.Data["next_retry_at"]))
	if raw == "" || raw == "<nil>" {
		return true
	}
	nextRetryAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return true
	}
	return !nextRetryAt.After(now)
}

func SchedulerInt(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if value, err := typed.Int64(); err == nil {
			return int(value)
		}
	case string:
		var out int
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%d", &out); err == nil {
			return out
		}
	}
	return fallback
}

func SchedulerMetadataJSON(metadata map[string]any) string {
	if len(metadata) == 0 {
		return "{}"
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func SchedulerSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "job"
	}
	return value
}
