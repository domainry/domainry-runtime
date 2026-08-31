package model

import (
	"fmt"
	"time"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

// Schedule is the canonical request for one record-scoped delayed execution.
type Schedule struct {
	TimerKey             string
	ObjectKey            string
	RecordID             string
	Purpose              string
	ScheduleMode         string
	DueAt                time.Time
	SourceField          string
	OffsetSeconds        int
	Timezone             string
	BusinessCalendarKey  string
	TargetType           string
	TargetKey            string
	PayloadJSON          string
	Priority             int
	Sequence             int64
	MaxAttempts          int
	RetryDelaySeconds    int
	RetryMaxDelaySeconds int
	SupersedesTimerID    string
}

func (schedule Schedule) String() string {
	return fmt.Sprintf("%s:%s:%s:%s", schedule.TimerKey, schedule.ObjectKey, schedule.RecordID, schedule.Purpose)
}

// Lease is the fencing evidence returned by a successful timer claim.
type Lease struct {
	Record recordmodel.Record
	Owner  string
	Token  int
}
