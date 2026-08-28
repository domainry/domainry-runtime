package validation

import (
	"errors"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestSchedulerAuthoringValidationRejectsInvalidCalendarFields(t *testing.T) {
	base := map[string]any{"target_type": "workflow", "target_key": "scheduled:reminder", "timezone": "UTC", "time_of_day": "08:00"}
	weekly := recordcontract.RecordCloneData(base)
	weekly["schedule_type"], weekly["day_of_week"] = "weekly_at", "funday"
	if err := SchedulerValidateDefinitionContract(t.Context(), weekly); serviceErrorCode(err) != "backend.scheduler.day_of_week_invalid" {
		t.Fatalf("weekly error=%v code=%s", err, serviceErrorCode(err))
	}
	monthly := recordcontract.RecordCloneData(base)
	monthly["schedule_type"], monthly["day_of_month"] = "monthly_at", 32
	if err := SchedulerValidateDefinitionContract(t.Context(), monthly); serviceErrorCode(err) != "backend.scheduler.day_of_month_invalid" {
		t.Fatalf("monthly error=%v code=%s", err, serviceErrorCode(err))
	}
	invalidRange := recordcontract.RecordCloneData(base)
	invalidRange["schedule_type"], invalidRange["interval_seconds"], invalidRange["max_attempts"] = "interval", 10, 101
	err := SchedulerValidateDefinitionContract(t.Context(), invalidRange)
	params := errorParams(err)
	if serviceErrorCode(err) != "backend.scheduler.max_attempts_invalid" || params["field"] != "max_attempts" || params["minimum"] != "1" || params["maximum"] != "100" || params["actual"] != "101" {
		t.Fatalf("scheduler range issue lacks bounds: code=%s params=%#v", serviceErrorCode(err), params)
	}
	invalidTarget := recordcontract.RecordCloneData(base)
	invalidTarget["target_type"] = "script"
	err = SchedulerValidateDefinitionContract(t.Context(), invalidTarget)
	params = errorParams(err)
	if serviceErrorCode(err) != "backend.scheduler.target_type_unsupported" || params["field"] != "target_type" || params["allowed"] == "" || params["actual"] != "script" {
		t.Fatalf("scheduler target issue lacks allowed values: code=%s params=%#v", serviceErrorCode(err), params)
	}
}

func errorParams(err error) map[string]string {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.Params
	}
	return nil
}

func serviceErrorCode(err error) string {
	return apperror.CodeOf(err)
}
