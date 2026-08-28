package policy

import (
	"testing"
	"time"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestNotificationDefaultAndValidationPolicyMatrix(t *testing.T) {
	policy := NotificationDefaultDeliveryPolicy()
	if !policy.Enabled || policy.QuietStart != "22:00" || policy.QuietEnd != "08:00" || policy.Timezone != "Asia/Shanghai" || policy.MaxPerRecipientPerHour != 20 || policy.DedupeWindowSeconds != 300 || len(policy.FallbackChannels) != 2 {
		t.Fatalf("default policy = %#v", policy)
	}
	if err := NotificationValidateDeliveryPolicy(policy); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*notificationmodel.NotificationDeliveryPolicy)
		code   string
	}{
		{name: "frequency low", mutate: func(value *notificationmodel.NotificationDeliveryPolicy) { value.MaxPerRecipientPerHour = 0 }, code: "backend.notification.policy_frequency_invalid"},
		{name: "frequency high", mutate: func(value *notificationmodel.NotificationDeliveryPolicy) { value.MaxPerRecipientPerHour = 10001 }, code: "backend.notification.policy_frequency_invalid"},
		{name: "dedupe low", mutate: func(value *notificationmodel.NotificationDeliveryPolicy) { value.DedupeWindowSeconds = -1 }, code: "backend.notification.policy_dedupe_invalid"},
		{name: "dedupe high", mutate: func(value *notificationmodel.NotificationDeliveryPolicy) { value.DedupeWindowSeconds = 86401 }, code: "backend.notification.policy_dedupe_invalid"},
		{name: "timezone", mutate: func(value *notificationmodel.NotificationDeliveryPolicy) { value.Timezone = "invalid" }, code: "backend.notification.policy_timezone_invalid"},
		{name: "quiet start", mutate: func(value *notificationmodel.NotificationDeliveryPolicy) { value.QuietStart = "25:00" }, code: "backend.notification.policy_quiet_hours_invalid"},
		{name: "quiet end", mutate: func(value *notificationmodel.NotificationDeliveryPolicy) { value.QuietEnd = "invalid" }, code: "backend.notification.policy_quiet_hours_invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := policy
			test.mutate(&value)
			if got := apperror.CodeOf(NotificationValidateDeliveryPolicy(value)); got != test.code {
				t.Fatalf("code = %q, want %q", got, test.code)
			}
		})
	}
}

func TestNotificationNextQuietHoursEndMatrix(t *testing.T) {
	base := notificationmodel.NotificationDeliveryPolicy{Timezone: "UTC", QuietStart: "22:00", QuietEnd: "08:00"}
	for _, test := range []struct {
		name   string
		now    time.Time
		policy notificationmodel.NotificationDeliveryPolicy
		want   string
	}{
		{name: "invalid timezone", now: time.Date(2026, 7, 19, 23, 0, 0, 0, time.UTC), policy: notificationmodel.NotificationDeliveryPolicy{Timezone: "invalid"}, want: ""},
		{name: "overnight late", now: time.Date(2026, 7, 19, 23, 0, 0, 0, time.UTC), policy: base, want: "2026-07-20T08:00:00Z"},
		{name: "overnight early", now: time.Date(2026, 7, 19, 7, 0, 0, 0, time.UTC), policy: base, want: "2026-07-19T08:00:00Z"},
		{name: "overnight outside", now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC), policy: base, want: ""},
		{name: "same-day inside", now: time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC), policy: notificationmodel.NotificationDeliveryPolicy{Timezone: "UTC", QuietStart: "09:00", QuietEnd: "17:00"}, want: "2026-07-19T17:00:00Z"},
		{name: "same-day before", now: time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC), policy: notificationmodel.NotificationDeliveryPolicy{Timezone: "UTC", QuietStart: "09:00", QuietEnd: "17:00"}, want: ""},
		{name: "same-day after", now: time.Date(2026, 7, 19, 18, 0, 0, 0, time.UTC), policy: notificationmodel.NotificationDeliveryPolicy{Timezone: "UTC", QuietStart: "09:00", QuietEnd: "17:00"}, want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := NotificationNextQuietHoursEnd(test.now, test.policy); got != test.want {
				t.Fatalf("quiet end = %q, want %q", got, test.want)
			}
		})
	}
}
