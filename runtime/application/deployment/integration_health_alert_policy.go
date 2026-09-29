package deployment

import (
	"fmt"
	"strings"
	"time"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
)

// IntegrationHealthAlertPolicy is the installation-owned SLA for durable
// Integration health alerts. Count and duration limits fire only when the
// observed value is greater than the limit. Official quota percentages fire
// when they reach the configured threshold.
type IntegrationHealthAlertPolicy struct {
	ReadyDueLimit                  int64
	FailedDueLimit                 int64
	ExpiredLeaseLimit              int64
	DeadLetterLimit                int64
	GoogleHTTP429HourLimit         int64
	GoogleGmailRateLimitHourLimit  int64
	FeishuRateLimitHourLimit       int64
	GoogleGmailHistoryGapHourLimit int64
	GoogleQuotaUsedPercentLimit    int64
	QueueOldestAgeLimit            time.Duration
	GooglePushDelayLimit           time.Duration
	GoogleSyncDelayLimit           time.Duration
}

func (p IntegrationHealthAlertPolicy) Validate() error {
	if p.ReadyDueLimit < 0 || p.FailedDueLimit < 0 || p.ExpiredLeaseLimit < 0 || p.DeadLetterLimit < 0 || p.GoogleHTTP429HourLimit < 0 || p.GoogleGmailRateLimitHourLimit < 0 || p.FeishuRateLimitHourLimit < 0 || p.GoogleGmailHistoryGapHourLimit < 0 || p.GoogleQuotaUsedPercentLimit < 0 || p.GoogleQuotaUsedPercentLimit > 100 {
		return fmt.Errorf("integration health alert count limits cannot be negative and quota percent cannot exceed 100")
	}
	if p.QueueOldestAgeLimit <= 0 || p.GooglePushDelayLimit <= 0 || p.GoogleSyncDelayLimit <= 0 {
		return fmt.Errorf("integration health alert duration limits must be positive")
	}
	return nil
}

// IntegrationHealthCondition is one stable source condition. Returning both
// firing and healthy conditions lets the host publish an exact recovery
// transition for an existing Notification alert group.
type IntegrationHealthCondition struct {
	Key        string
	MetricName string
	Unit       string
	Current    int64
	Limit      int64
	Firing     bool
	ObservedAt string
}

func EvaluateIntegrationHealth(snapshot integrationsdk.ProviderRunSnapshot, policy IntegrationHealthAlertPolicy) ([]IntegrationHealthCondition, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	observedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(snapshot.ObservedAt))
	if err != nil {
		return nil, fmt.Errorf("parse Integration health observed_at: %w", err)
	}
	observedAt = observedAt.UTC()

	oldestRunnableAge := int64(0)
	if raw := strings.TrimSpace(snapshot.OldestRunnableDueAt); raw != "" {
		oldest, parseErr := time.Parse(time.RFC3339Nano, raw)
		if parseErr != nil {
			return nil, fmt.Errorf("parse Integration oldest_runnable_due_at: %w", parseErr)
		}
		if age := observedAt.Sub(oldest); age > 0 {
			oldestRunnableAge = age.Milliseconds()
		}
	}

	google429Hour := snapshot.GoogleHTTP429EventsHour + snapshot.GoogleHTTP429ForegroundEventsHour
	googleGmailRateLimitHour := snapshot.GoogleGmailProjectRateLimitEventsHour + snapshot.GoogleGmailProjectRateLimitForegroundEventsHour + snapshot.GoogleGmailUserRateLimitEventsHour + snapshot.GoogleGmailUserRateLimitForegroundEventsHour
	feishuRateLimitHour := snapshot.FeishuRateLimitEventsHour + snapshot.FeishuRateLimitForegroundEventsHour
	googleGmailHistoryGapHour := snapshot.GoogleGmailHistoryMessagesWithoutPushHour + snapshot.GoogleGmailHistoryCursorExpiredHour
	googleSyncDelay := max(snapshot.GoogleSyncMaxEndToEndDelayMsHour, snapshot.GoogleSyncOldestPendingAgeMs)
	conditions := []IntegrationHealthCondition{
		{Key: "ready_due", MetricName: "ready due tasks", Unit: "tasks", Current: snapshot.ReadyDue, Limit: policy.ReadyDueLimit},
		{Key: "failed_due", MetricName: "retryable failed tasks due", Unit: "tasks", Current: snapshot.FailedDue, Limit: policy.FailedDueLimit},
		{Key: "expired_processing_leases", MetricName: "expired processing leases", Unit: "tasks", Current: snapshot.ExpiredProcessing, Limit: policy.ExpiredLeaseLimit},
		{Key: "dead_letter", MetricName: "dead-letter tasks", Unit: "tasks", Current: snapshot.DeadLetter, Limit: policy.DeadLetterLimit},
		{Key: "google_http_429_hour", MetricName: "Google HTTP 429 events in the last hour", Unit: "events", Current: google429Hour, Limit: policy.GoogleHTTP429HourLimit},
		{Key: "google_gmail_rate_limit_hour", MetricName: "Gmail quota rate-limit rejections in the last hour", Unit: "events", Current: googleGmailRateLimitHour, Limit: policy.GoogleGmailRateLimitHourLimit},
		{Key: "feishu_rate_limit_hour", MetricName: "Feishu official rate-limit rejections in the last hour", Unit: "events", Current: feishuRateLimitHour, Limit: policy.FeishuRateLimitHourLimit},
		{Key: "google_gmail_history_gap_hour", MetricName: "Gmail history gaps in the last hour", Unit: "observations", Current: googleGmailHistoryGapHour, Limit: policy.GoogleGmailHistoryGapHourLimit},
		{Key: "oldest_runnable_age_ms", MetricName: "oldest runnable task age", Unit: "milliseconds", Current: oldestRunnableAge, Limit: policy.QueueOldestAgeLimit.Milliseconds()},
		{Key: "google_push_delay_ms", MetricName: "Google Push receive delay", Unit: "milliseconds", Current: snapshot.GooglePushMaxDelayMsHour, Limit: policy.GooglePushDelayLimit.Milliseconds()},
		{Key: "google_sync_delay_ms", MetricName: "Google Push-to-sync commit delay", Unit: "milliseconds", Current: googleSyncDelay, Limit: policy.GoogleSyncDelayLimit.Milliseconds()},
	}
	if snapshot.GoogleOfficialQuota.Status == integrationsdk.ProviderQuotaStatusAvailable && snapshot.GoogleOfficialQuota.MaxUsedPercent != nil && policy.GoogleQuotaUsedPercentLimit > 0 {
		conditions = append(conditions, IntegrationHealthCondition{
			Key: "google_official_quota_used_percent", MetricName: "Google official quota maximum usage", Unit: "percent",
			Current: *snapshot.GoogleOfficialQuota.MaxUsedPercent, Limit: policy.GoogleQuotaUsedPercentLimit,
		})
	}
	for index := range conditions {
		if conditions[index].Key == "google_official_quota_used_percent" {
			conditions[index].Firing = conditions[index].Current >= conditions[index].Limit
		} else {
			conditions[index].Firing = conditions[index].Current > conditions[index].Limit
		}
		conditions[index].ObservedAt = observedAt.Format(time.RFC3339Nano)
	}
	return conditions, nil
}
