package deployment

import (
	"testing"
	"time"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
)

func TestEvaluateIntegrationHealthUsesStrictConfigurableBoundaries(t *testing.T) {
	policy := IntegrationHealthAlertPolicy{
		ReadyDueLimit: 10, FailedDueLimit: 2, ExpiredLeaseLimit: 0, DeadLetterLimit: 0, GoogleHTTP429HourLimit: 4, GoogleGmailRateLimitHourLimit: 5, FeishuRateLimitHourLimit: 5, GoogleGmailHistoryGapHourLimit: 1,
		QueueOldestAgeLimit: 5 * time.Minute, GooglePushDelayLimit: 2 * time.Minute, GoogleSyncDelayLimit: 3 * time.Minute,
	}
	snapshot := integrationsdk.ProviderRunSnapshot{
		ObservedAt: "2026-09-29T02:10:00Z", OldestRunnableDueAt: "2026-09-29T02:04:59Z",
		ReadyDue: 10, FailedDue: 3, ExpiredProcessing: 1, DeadLetter: 0,
		GoogleHTTP429EventsHour: 2, GoogleHTTP429ForegroundEventsHour: 3,
		GoogleGmailProjectRateLimitEventsHour: 2, GoogleGmailProjectRateLimitForegroundEventsHour: 1,
		GoogleGmailUserRateLimitEventsHour: 2, GoogleGmailUserRateLimitForegroundEventsHour: 1,
		FeishuRateLimitEventsHour: 2, FeishuRateLimitForegroundEventsHour: 4,
		GoogleGmailHistoryMessagesWithoutPushHour: 1, GoogleGmailHistoryCursorExpiredHour: 1,
		GooglePushMaxDelayMsHour:         (2 * time.Minute).Milliseconds(),
		GoogleSyncMaxEndToEndDelayMsHour: (2 * time.Minute).Milliseconds(), GoogleSyncOldestPendingAgeMs: (4 * time.Minute).Milliseconds(),
		GlobalRateUsed: 999, GlobalRateLimit: 1000,
	}

	conditions, err := EvaluateIntegrationHealth(snapshot, policy)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"ready_due": false, "failed_due": true, "expired_processing_leases": true, "dead_letter": false,
		"google_http_429_hour": true, "google_gmail_rate_limit_hour": true, "feishu_rate_limit_hour": true, "google_gmail_history_gap_hour": true, "oldest_runnable_age_ms": true, "google_push_delay_ms": false, "google_sync_delay_ms": true,
	}
	if len(conditions) != len(want) {
		t.Fatalf("conditions=%+v", conditions)
	}
	for _, condition := range conditions {
		if condition.Firing != want[condition.Key] {
			t.Fatalf("condition %s firing=%v current=%d limit=%d", condition.Key, condition.Firing, condition.Current, condition.Limit)
		}
		if condition.ObservedAt != "2026-09-29T02:10:00Z" {
			t.Fatalf("condition %s observed_at=%q", condition.Key, condition.ObservedAt)
		}
	}
}

func TestEvaluateIntegrationHealthDoesNotTreatInternalDispatchBudgetAsProviderQuota(t *testing.T) {
	conditions, err := EvaluateIntegrationHealth(integrationsdk.ProviderRunSnapshot{
		ObservedAt: "2026-09-29T02:10:00Z", GlobalRateUsed: 1000, GlobalRateLimit: 1000,
	}, IntegrationHealthAlertPolicy{
		QueueOldestAgeLimit: time.Minute, GooglePushDelayLimit: time.Minute, GoogleSyncDelayLimit: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, condition := range conditions {
		if condition.Firing {
			t.Fatalf("internal dispatch budget incorrectly fired condition %+v", condition)
		}
	}
}

func TestEvaluateIntegrationHealthUsesOnlyAvailableOfficialQuotaEvidence(t *testing.T) {
	percent := int64(80)
	policy := IntegrationHealthAlertPolicy{
		GoogleQuotaUsedPercentLimit: 80, QueueOldestAgeLimit: time.Minute,
		GooglePushDelayLimit: time.Minute, GoogleSyncDelayLimit: time.Minute,
	}
	snapshot := integrationsdk.ProviderRunSnapshot{
		ObservedAt: "2026-09-29T02:10:00Z", GlobalRateUsed: 1000, GlobalRateLimit: 1000,
		GoogleOfficialQuota: integrationsdk.ProviderQuotaSnapshot{
			Status: integrationsdk.ProviderQuotaStatusAvailable, MaxUsedPercent: &percent,
		},
	}
	conditions, err := EvaluateIntegrationHealth(snapshot, policy)
	if err != nil {
		t.Fatal(err)
	}
	quotaFound := false
	for _, condition := range conditions {
		if condition.Key == "google_official_quota_used_percent" {
			quotaFound = true
			if !condition.Firing || condition.Current != 80 || condition.Limit != 80 || condition.Unit != "percent" {
				t.Fatalf("quota condition=%+v", condition)
			}
		}
	}
	if !quotaFound {
		t.Fatalf("official quota condition missing: %+v", conditions)
	}
	belowThreshold := int64(79)
	snapshot.GoogleOfficialQuota.MaxUsedPercent = &belowThreshold
	conditions, err = EvaluateIntegrationHealth(snapshot, policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, condition := range conditions {
		if condition.Key == "google_official_quota_used_percent" && condition.Firing {
			t.Fatalf("official quota below the threshold fired: %+v", condition)
		}
	}

	snapshot.GoogleOfficialQuota.Status = integrationsdk.ProviderQuotaStatusUnavailable
	conditions, err = EvaluateIntegrationHealth(snapshot, policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, condition := range conditions {
		if condition.Key == "google_official_quota_used_percent" {
			t.Fatalf("unavailable official quota was evaluated: %+v", condition)
		}
	}
}

func TestEvaluateIntegrationHealthRejectsInvalidSourceTimestamps(t *testing.T) {
	policy := IntegrationHealthAlertPolicy{QueueOldestAgeLimit: time.Minute, GooglePushDelayLimit: time.Minute, GoogleSyncDelayLimit: time.Minute}
	if _, err := EvaluateIntegrationHealth(integrationsdk.ProviderRunSnapshot{ObservedAt: "invalid"}, policy); err == nil {
		t.Fatal("expected invalid observed_at rejection")
	}
	if _, err := EvaluateIntegrationHealth(integrationsdk.ProviderRunSnapshot{ObservedAt: "2026-09-29T02:10:00Z", OldestRunnableDueAt: "invalid"}, policy); err == nil {
		t.Fatal("expected invalid oldest_runnable_due_at rejection")
	}
}
