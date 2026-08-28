package validation

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"

	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

// SchedulerValidateDefinitionContract exposes Runtime's canonical scheduler
// authoring rules without requiring a running record service.
func SchedulerValidateDefinitionContract(ctx context.Context, data map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return validateSchedulerDefinitionData(ctx, data)
}

func validateSchedulerDefinitionData(ctx context.Context, data map[string]any) error {
	targetType := schedulerDefinitionTargetType(recordmodel.Record{Data: data})
	switch targetType {
	case "workflow", "report_export", "report_snapshot_refresh":
	case "":
		return badRequest("backend.scheduler.target_type_required", "field", "target_type")
	default:
		return badRequest("backend.scheduler.target_type_unsupported", "field", "target_type", "allowed", "report_export,report_snapshot_refresh,workflow", "actual", targetType)
	}
	targetKey := strings.TrimSpace(fmt.Sprint(data["target_key"]))
	if targetKey == "" || targetKey == "<nil>" {
		return badRequest("backend.scheduler.target_key_required", "field", "target_key")
	}
	if targetType == "workflow" && targetKey != "scheduled:*" && !strings.HasPrefix(targetKey, "scheduled:") {
		return badRequest("backend.scheduler.workflow_target_invalid", "field", "target_key", "actual", targetKey)
	}
	if (targetType == "report_export" || targetType == "report_snapshot_refresh") && strings.HasPrefix(targetKey, "scheduled:") {
		return badRequest("backend.scheduler.report_target_invalid", "field", "target_key", "actual", targetKey)
	}
	maxAttempts := schedulerpolicy.SchedulerInt(data["max_attempts"], 1)
	if maxAttempts < 1 || maxAttempts > 100 {
		return badRequest("backend.scheduler.max_attempts_invalid", "field", "max_attempts", "minimum", "1", "maximum", "100", "actual", fmt.Sprint(maxAttempts))
	}
	timeoutSeconds := schedulerpolicy.SchedulerInt(data["timeout_seconds"], 300)
	if timeoutSeconds < 1 || timeoutSeconds > 24*60*60 {
		return badRequest("backend.scheduler.timeout_invalid", "field", "timeout_seconds", "minimum", "1", "maximum", fmt.Sprint(24*60*60), "actual", fmt.Sprint(timeoutSeconds))
	}
	if policy := schedulerMissedWindowPolicy(recordmodel.Record{Data: data}); policy == "catch_up_bounded" {
		maxCatchupWindows := schedulerpolicy.SchedulerInt(data["max_catchup_windows"], 0)
		if maxCatchupWindows < 0 || maxCatchupWindows > 100 {
			return badRequest("backend.scheduler.max_catchup_windows_invalid", "field", "max_catchup_windows", "minimum", "0", "maximum", "100", "actual", fmt.Sprint(maxCatchupWindows))
		}
	}
	rawMissedWindowPolicy := strings.TrimSpace(fmt.Sprint(data["missed_window_policy"]))
	if rawMissedWindowPolicy != "" && rawMissedWindowPolicy != "<nil>" {
		if normalized := schedulerMissedWindowPolicy(recordmodel.Record{Data: data}); normalized == "skip" && strings.ToLower(rawMissedWindowPolicy) != "skip" {
			return badRequest("backend.scheduler.missed_window_policy_invalid", "field", "missed_window_policy", "allowed", "catch_up_bounded,catch_up_one,skip", "actual", rawMissedWindowPolicy)
		}
	}
	return SchedulerValidateScheduleFragment(ctx, data)
}

// SchedulerValidateScheduleFragment validates the Scheduler-owned schedule
// payload without requiring callers to manufacture a complete job definition.
func SchedulerValidateScheduleFragment(ctx context.Context, data map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if timezone := strings.TrimSpace(fmt.Sprint(data["timezone"])); timezone != "" && timezone != "<nil>" {
		if _, err := time.LoadLocation(timezone); err != nil {
			return badRequest("backend.scheduler.timezone_invalid", "field", "timezone", "actual", timezone, "expected", "IANA timezone")
		}
	}
	rawScheduleType := strings.ToLower(strings.TrimSpace(fmt.Sprint(data["schedule_type"])))
	if rawScheduleType != "" && rawScheduleType != "<nil>" {
		switch rawScheduleType {
		case "interval", "daily_at", "weekly_at", "monthly_at", "cron":
		default:
			return badRequest("backend.scheduler.schedule_type_invalid", "field", "schedule_type", "allowed", "cron,daily_at,interval,monthly_at,weekly_at", "actual", rawScheduleType)
		}
	}
	definition := recordmodel.Record{Data: data}
	switch schedulerpolicy.SchedulerScheduleType(definition) {
	case "interval":
		if schedulerpolicy.SchedulerScheduleIntervalSeconds(definition) <= 0 {
			return badRequest("backend.scheduler.interval_invalid", "field", "interval_seconds", "minimum", "1", "actual", fmt.Sprint(schedulerpolicy.SchedulerScheduleIntervalSeconds(definition)))
		}
	case "daily_at":
		if _, _, _, ok := schedulerpolicy.SchedulerParseClock(schedulerpolicy.SchedulerFirstNonEmptyString(
			fmt.Sprint(data["time_of_day"]),
			fmt.Sprint(data["schedule_time"]),
			fmt.Sprint(data["daily_at"]),
			fmt.Sprint(data["schedule_expression"]),
		)); !ok {
			return badRequest("backend.scheduler.time_of_day_invalid", "field", "time_of_day")
		}
	case "weekly_at":
		if _, _, _, ok := schedulerpolicy.SchedulerParseClock(schedulerpolicy.SchedulerFirstNonEmptyString(
			fmt.Sprint(data["time_of_day"]),
			fmt.Sprint(data["schedule_time"]),
			fmt.Sprint(data["weekly_at"]),
			fmt.Sprint(data["schedule_expression"]),
		)); !ok {
			return badRequest("backend.scheduler.time_of_day_invalid", "field", "time_of_day")
		}
		if _, ok := schedulerpolicy.SchedulerParseWeekday(schedulerpolicy.SchedulerFirstNonEmptyString(fmt.Sprint(data["day_of_week"]), fmt.Sprint(data["weekday"]), fmt.Sprint(data["week_day"]))); !ok {
			return badRequest("backend.scheduler.day_of_week_invalid", "field", "day_of_week")
		}
	case "monthly_at":
		if _, _, _, ok := schedulerpolicy.SchedulerParseClock(schedulerpolicy.SchedulerFirstNonEmptyString(
			fmt.Sprint(data["time_of_day"]),
			fmt.Sprint(data["schedule_time"]),
			fmt.Sprint(data["monthly_at"]),
			fmt.Sprint(data["schedule_expression"]),
		)); !ok {
			return badRequest("backend.scheduler.time_of_day_invalid", "field", "time_of_day")
		}
		day := schedulerpolicy.SchedulerInt(data["day_of_month"], schedulerpolicy.SchedulerInt(data["month_day"], 0))
		if day < 1 || day > 31 {
			return badRequest("backend.scheduler.day_of_month_invalid", "field", "day_of_month", "minimum", "1", "maximum", "31", "actual", fmt.Sprint(day))
		}
	case "cron":
		if _, ok := schedulerpolicy.SchedulerCronSchedule(definition); !ok {
			return badRequest("backend.scheduler.cron_invalid", "field", "schedule_expression")
		}
	case "legacy_hourly", "legacy_daily", "legacy_weekly", "legacy_monthly":
	}
	return nil
}

func schedulerDefinitionTargetType(record recordmodel.Record) string {
	targetType := strings.ToLower(strings.TrimSpace(fmt.Sprint(record.Data["target_type"])))
	if targetType == "<nil>" {
		return ""
	}
	return targetType
}

func schedulerMissedWindowPolicy(definition recordmodel.Record) string {
	policy := strings.ToLower(strings.TrimSpace(fmt.Sprint(definition.Data["missed_window_policy"])))
	if policy == "catch_up_one" || policy == "catch_up_bounded" {
		return policy
	}
	return "skip"
}

func badRequest(code string, values ...string) error {
	params := map[string]string{}
	for index := 0; index+1 < len(values); index += 2 {
		if key := strings.TrimSpace(values[index]); key != "" {
			params[key] = values[index+1]
		}
	}
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: code, Params: params}
}
