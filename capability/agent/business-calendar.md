# When should deadlines and recurring work use a Business Calendar?

Runtime owns the versioned calendar definition. Workflow consumes it to add working duration; Scheduler receives an immutable protocol snapshot and applies an explicit non-working-day policy.

## Problems solved

- Makes working-time deadlines deterministic across timezones, lunch breaks, holidays, temporary closures, and make-up working days.
- Prevents a later calendar edit from silently moving an already-created Workflow timer or changing an already-published Scheduler definition revision.

## Business scenarios

- An approval is due after eight working hours in `Asia/Shanghai`, excluding lunch and published holidays.
- A daily job should either skip a closed local date or roll to the next working date.
- A weekend is temporarily opened for work, or a normal weekday is closed for a local exception.

## Use when

- A Workflow timer adds business duration rather than elapsed 24x7 duration.
- A calendar-based Scheduler definition needs `skip` or `roll_forward` behavior on a non-working date.

## Do not use when

- The requirement is an absolute timestamp or an elapsed 24x7 duration.
- An event, not time, determines when work starts or resumes.
- The requested behavior needs arbitrary callback code; calendars are closed source-controlled data, not executable extensions.

## How to use

Publish `schema.business_calendar` with a stable `key`, non-empty `revision`, IANA `timezone`, weekly half-open working intervals, optional `holidays`, and optional `date_exceptions`. An exception replaces the weekly schedule for that local date: an empty interval list closes it, while a non-empty list may open a weekend or change the hours.

Working interval endpoints are local wall-clock times in the declared timezone, not elapsed offsets from UTC or from local midnight. A `09:00` boundary therefore remains 09:00 across daylight-saving transitions; elapsed working duration inside an interval still follows the actual timeline.

A Workflow timer references `business_calendar_key`; Runtime resolves the exact definition and persists the calendar revision with the timer evidence. A Scheduler definition references the same key and chooses `non_working_day_policy` as `skip` or `roll_forward`; Runtime embeds the complete immutable calendar snapshot in the Scheduler-owned definition protocol.

## Adaptation cookbook

| Business requirement | Adapt with | Concrete implementation | Wrong adaptation |
| --- | --- | --- | --- |
| Approval is due after eight office hours | Workflow business-duration timer | Reference the published calendar key and declare the working-duration offset; keep the resulting calendar revision on the timer | Adding eight elapsed hours in a Handler or reading the current calendar again when the timer fires |
| Daily settlement must not run on holidays | Scheduler + `skip` | Reference the calendar and set `non_working_day_policy: "skip"`; the closed occurrence produces no run | Hiding holiday checks in the target Action after Scheduler already created a run |
| A closed-date occurrence must move to the next working date | Scheduler + `roll_forward` | Reference the calendar and set `non_working_day_policy: "roll_forward"`; preview the resulting occurrences before enablement | Implementing a Runtime callback that Scheduler invokes for every tick |
| Saturday is a one-time working day | Date exception | Add that local date with explicit intervals and publish a new calendar revision | Mutating an old revision or changing the weekly Saturday rule for one exception |

## Example

```json
{
  "payload": {
    "key": "cn_operations",
    "name": "China operations",
    "revision": "2026.2",
    "timezone": "Asia/Shanghai",
    "weekly_working_intervals": [
      {
        "weekday": "monday",
        "intervals": [
          {"start": "09:00", "end": "12:00"},
          {"start": "13:00", "end": "18:00"}
        ]
      }
    ],
    "holidays": ["2026-10-01"],
    "date_exceptions": [
      {"date": "2026-10-10", "intervals": [{"start": "09:00", "end": "17:00"}]}
    ]
  }
}
```

## Permissions and scope

Calendar authoring requires `runtime.appschema.validate_application_definition`. It defines time semantics only; it grants no target permission. Scheduler targets still require their managed service Role, and Workflow actors still use Workflow authorization.

## Boundaries

The calendar is source-controlled, versioned data. Runtime does not load project calendar code, and Scheduler does not call an arbitrary Runtime callback. Scheduler supports calendar snapshots only for calendar schedules, not fixed intervals; Workflow and already-created timers never follow a newer revision implicitly.
