package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordTimerScheduleHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := ResolveRecordTimerSchedule(ctx, RecordTimerSchedule{ScheduleMode: "business_calendar", DueAt: time.Now(), Timezone: "UTC", BusinessCalendarKey: "24x7"}, recordTimerRecord(), StandardRecordTimerBusinessCalendar{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled record timer schedule error = %v", err)
	}
}

func recordTimerRecord() recordmodel.Record { return recordmodel.Record{Data: map[string]any{}} }
