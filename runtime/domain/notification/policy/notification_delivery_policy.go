package policy

import (
	"time"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func NotificationDefaultDeliveryPolicy() notificationmodel.NotificationDeliveryPolicy {
	return notificationmodel.NotificationDeliveryPolicy{Enabled: true, QuietStart: "22:00", QuietEnd: "08:00", Timezone: "Asia/Shanghai", MaxPerRecipientPerHour: 20, DedupeWindowSeconds: 300, FallbackChannels: []string{"collaboration", "email"}}
}

func NotificationValidateDeliveryPolicy(value notificationmodel.NotificationDeliveryPolicy) error {
	if value.MaxPerRecipientPerHour < 1 || value.MaxPerRecipientPerHour > 10000 {
		return notificationPolicyError("backend.notification.policy_frequency_invalid")
	}
	if value.DedupeWindowSeconds < 0 || value.DedupeWindowSeconds > 86400 {
		return notificationPolicyError("backend.notification.policy_dedupe_invalid")
	}
	if _, err := time.LoadLocation(value.Timezone); err != nil {
		return notificationPolicyError("backend.notification.policy_timezone_invalid")
	}
	for _, clock := range []string{value.QuietStart, value.QuietEnd} {
		if _, err := time.Parse("15:04", clock); err != nil {
			return notificationPolicyError("backend.notification.policy_quiet_hours_invalid")
		}
	}
	return nil
}

func NotificationNextQuietHoursEnd(now time.Time, policy notificationmodel.NotificationDeliveryPolicy) string {
	location, err := time.LoadLocation(policy.Timezone)
	if err != nil {
		return ""
	}
	local := now.In(location)
	start, _ := time.Parse("15:04", policy.QuietStart)
	end, _ := time.Parse("15:04", policy.QuietEnd)
	minute, startMinute, endMinute := local.Hour()*60+local.Minute(), start.Hour()*60+start.Minute(), end.Hour()*60+end.Minute()
	inQuiet := startMinute < endMinute && minute >= startMinute && minute < endMinute || startMinute >= endMinute && (minute >= startMinute || minute < endMinute)
	if !inQuiet {
		return ""
	}
	endAt := time.Date(local.Year(), local.Month(), local.Day(), end.Hour(), end.Minute(), 0, 0, location)
	if !endAt.After(local) {
		endAt = endAt.AddDate(0, 0, 1)
	}
	return endAt.UTC().Format(time.RFC3339)
}

func notificationPolicyError(code string) error {
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: code}
}
