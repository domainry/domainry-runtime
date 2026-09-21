package validation

import (
	"fmt"
	"strings"

	businesscalendarpolicy "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/policy"
)

func (state *validationState) validateBusinessCalendars() {
	seen := map[string]int{}
	for index, calendar := range state.manifest.BusinessCalendars {
		path := fmt.Sprintf("business_calendars[%d]", index)
		key := strings.TrimSpace(calendar.Key)
		if first, duplicate := seen[key]; duplicate && key != "" {
			state.add(path+".key", "duplicate business calendar key %q; first declared at business_calendars[%d]", key, first)
			continue
		}
		seen[key] = index
		if err := businesscalendarpolicy.Validate(calendar); err != nil {
			code := businesscalendarpolicy.ValidationCode(err)
			if code == "" {
				code = err.Error()
			}
			state.add(path, "violates business calendar contract: %s", code)
		}
	}
}

func (state *validationState) validateWorkflowTimerBusinessCalendar(path string, key, timezone string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	for _, calendar := range state.manifest.BusinessCalendars {
		if strings.TrimSpace(calendar.Key) != key {
			continue
		}
		if timezone = strings.TrimSpace(timezone); timezone != "" && timezone != strings.TrimSpace(calendar.Timezone) {
			state.add(path+".timezone", "backend.workflow.business_calendar_timezone_mismatch: calendar %q uses %q", key, calendar.Timezone)
		}
		return
	}
	state.add(path+".business_calendar_key", "backend.workflow.business_calendar_not_found: %q", key)
}
