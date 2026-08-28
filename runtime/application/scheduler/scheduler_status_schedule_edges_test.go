package scheduler

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

func TestSchedulerRuntimeAvailabilityAndStatus(t *testing.T) {
	now := time.Date(2026, 7, 19, 19, 0, 0, 0, time.UTC)
	if NewSchedulerApplicationService(schedulerTestSchema(), nil, nil, nil).RuntimeAvailable(t.Context(), principalmodel.Principal{}) {
		t.Fatal("unknown principal reported runtime available")
	}
	partial := schedulerSchemaStub{snapshot: schedulerTestSchema().snapshot}
	partial.snapshot.Objects = partial.snapshot.Objects[:3]
	if NewSchedulerApplicationService(partial, nil, nil, nil).RuntimeAvailable(t.Context(), schedulerTestPrincipal("read")) {
		t.Fatal("partial schema reported runtime available")
	}
	if !NewSchedulerApplicationService(schedulerTestSchema(), nil, nil, nil).RuntimeAvailable(t.Context(), schedulerTestPrincipal("read")) {
		t.Fatal("complete schema reported unavailable")
	}

	definitionError := errors.New("definitions failed")
	runError := errors.New("runs failed")
	deadLetterError := errors.New("dead letters failed")
	repository := &schedulerRepositoryFake{list: func(_ context.Context, workspaceID string, object definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		if workspaceID != principalmodel.InstallationWorkspaceID {
			t.Fatalf("workspace = %q", workspaceID)
		}
		switch object.Key {
		case "job_definition":
			return recordmodel.RecordPageResult{}, definitionError
		case "job_run":
			return recordmodel.RecordPageResult{}, runError
		case "job_dead_letter":
			return recordmodel.RecordPageResult{}, deadLetterError
		default:
			t.Fatalf("unexpected object %q", object.Key)
			return recordmodel.RecordPageResult{}, nil
		}
	}}
	service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), nil, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
	if _, err := service.Status(t.Context(), principalmodel.SystemScope{}); err == nil {
		t.Fatal("invalid status scope accepted")
	}
	status, err := service.Status(t.Context(), schedulerRuntimeScope())
	if err != nil || status["definition_error"] != definitionError.Error() || status["run_error"] != runError.Error() || status["dead_letter_error"] != deadLetterError.Error() {
		t.Fatalf("error status = %+v, %v", status, err)
	}

	started := now.Add(-2 * time.Second)
	repository.list = func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		switch object.Key {
		case "job_definition":
			return recordmodel.RecordPageResult{Total: 4, Items: []recordmodel.Record{
				{Data: map[string]any{"status": "enabled", "next_run_at": now.Add(-time.Minute).Format(time.RFC3339)}},
				{Data: map[string]any{"status": "enabled", "next_run_at": now.Add(time.Minute).Format(time.RFC3339)}},
				{Data: map[string]any{"status": "disabled"}},
			}}, nil
		case "job_run":
			return recordmodel.RecordPageResult{Total: 6, Items: []recordmodel.Record{
				{Data: map[string]any{"status": "leased", "lease_expires_at": now.Add(-time.Second).Format(time.RFC3339), "started_at": started.Format(time.RFC3339), "finished_at": now.Format(time.RFC3339)}},
				{Data: map[string]any{"status": "running", "started_at": now.Format(time.RFC3339), "finished_at": started.Format(time.RFC3339)}},
				{Data: map[string]any{"status": "succeeded", "started_at": now.Add(-50 * time.Millisecond).Format(time.RFC3339), "finished_at": now.Format(time.RFC3339)}},
				{Data: map[string]any{"status": "failed", "started_at": "bad", "finished_at": "bad"}},
				{Data: map[string]any{"status": "retrying"}},
				{Data: map[string]any{}},
			}}, nil
		case "job_dead_letter":
			return recordmodel.RecordPageResult{Total: 2, Items: []recordmodel.Record{{Data: map[string]any{"status": "open"}}, {Data: map[string]any{"status": "resolved"}}}}, nil
		default:
			return recordmodel.RecordPageResult{}, nil
		}
	}
	status, err = service.Status(t.Context(), schedulerRuntimeScope())
	if err != nil {
		t.Fatal(err)
	}
	if status["enabled_definitions"] != 2 || status["due_definitions"] != 1 || status["claimed_runs"] != 2 || status["lease_expirations"] != 1 || status["unresolved_dead_letters"] != 1 {
		t.Fatalf("status = %+v", status)
	}
	counts := status["status_counts"].(map[string]int)
	if counts["unknown"] != 1 || counts["succeeded"] != 1 || counts["retrying"] != 1 {
		t.Fatalf("status counts = %+v", counts)
	}
	metrics := status["run_duration_ms"].(map[string]any)
	if metrics["count"] != 2 || metrics["max"] != 2000 || metrics["le_1000"] != 1 || metrics["gt_1000"] != 1 {
		t.Fatalf("duration metrics = %+v", metrics)
	}

	unavailable := NewSchedulerApplicationService(partial, nil, repository, nil)
	status, err = unavailable.Status(t.Context(), schedulerRuntimeScope())
	if err != nil || status["runtime_available"] != false {
		t.Fatalf("unavailable status = %+v, %v", status, err)
	}
}

func TestSchedulerDurationAccumulatorAndMetricParsing(t *testing.T) {
	accumulator := newSchedulerDurationAccumulator()
	if got := accumulator.metrics(); got["count"] != 0 || got["avg"] != 0 {
		t.Fatalf("empty metrics = %+v", got)
	}
	for _, value := range []int{-1, 100, 101, 1000, 1001, 2000} {
		accumulator.add(value)
	}
	got := accumulator.metrics()
	want := map[string]any{"count": 6, "total": 4202, "avg": 700, "max": 2000, "le_100": 2, "le_1000": 2, "gt_1000": 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("metrics = %+v, want %+v", got, want)
	}
	if _, ok := parseSchedulerMetricTime("bad"); ok {
		t.Fatal("invalid metric time accepted")
	}
	parsed, ok := parseSchedulerMetricTime(" 2026-07-19T19:00:00+08:00 ")
	if !ok || parsed.Location() != time.UTC {
		t.Fatalf("parsed = %v, %v", parsed, ok)
	}
	objects := schemaObjectMap([]definitionmodel.ObjectSchema{{Key: "a"}, {Key: "a", Name: "latest"}})
	if len(objects) != 1 || objects["a"].Name != "latest" {
		t.Fatalf("objects = %+v", objects)
	}
}

func TestSchedulerDueDefinitionAndScheduleBoundaryMatrix(t *testing.T) {
	now := time.Date(2026, 7, 19, 20, 0, 0, 0, time.UTC)
	items := []recordmodel.Record{
		{ID: "workflow-all", Data: map[string]any{"status": "enabled", "target_type": "workflow", "target_key": "scheduled:*"}},
		{ID: "workflow-one", Data: map[string]any{"status": "enabled", "target_type": "WORKFLOW", "target_key": "scheduled:one", "next_run_at": "bad"}},
		{ID: "workflow-invalid", Data: map[string]any{"target_type": "workflow", "target_key": "manual"}},
		{ID: "report", Data: map[string]any{"status": "enabled", "target_type": "report_export", "target_key": "sales", "next_run_at": now.Format(time.RFC3339)}},
		{ID: "report-empty", Data: map[string]any{"target_type": "report_export"}},
		{ID: "unsupported", Data: map[string]any{"target_type": "notification"}},
		{ID: "future", Data: map[string]any{"target_type": "workflow", "target_key": "scheduled:*", "next_run_at": now.Add(time.Minute).Format(time.RFC3339)}},
	}
	repository := &schedulerRepositoryFake{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{Items: items}, nil
	}}
	service := schedulerPersistenceService(repository, now)
	due, err := service.DueDefinitions(t.Context(), definitionmodel.ObjectSchema{Key: "job_definition"}, now, schedulerRuntimeScope())
	if err != nil || len(due) != 3 || due[0].ID != "workflow-all" || due[2].ID != "report" {
		t.Fatalf("due = %+v, %v", due, err)
	}
	if _, err := service.DueDefinitions(t.Context(), definitionmodel.ObjectSchema{}, now, principalmodel.SystemScope{}); err == nil {
		t.Fatal("invalid due scope accepted")
	}
	wantErr := errors.New("list failed")
	repository.list = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{}, wantErr
	}
	if _, err := service.dueDefinitions(t.Context(), "workspace-a", definitionmodel.ObjectSchema{}, now); !errors.Is(err, wantErr) {
		t.Fatalf("list error = %v", err)
	}

	for _, test := range []struct {
		value any
		want  bool
	}{{nil, true}, {"", true}, {"<nil>", true}, {"bad", true}, {now.Format(time.RFC3339), true}, {now.Add(time.Second).Format(time.RFC3339), false}} {
		if got := schedulerDefinitionDue(recordmodel.Record{Data: map[string]any{"next_run_at": test.value}}, now); got != test.want {
			t.Fatalf("due(%v) = %v, want %v", test.value, got, test.want)
		}
	}
	if _, ok := schedulerDefinitionNextRunAt(recordmodel.Record{Data: map[string]any{"next_run_at": "bad"}}); ok {
		t.Fatal("invalid next run accepted")
	}
	if got := schedulerRunID("nightly", "manual_run", now); got == schedulerRunID("nightly", "scheduler", now) {
		t.Fatal("manual and scheduled run IDs collide")
	}
	if got := schedulerRunIDForDefinition(recordmodel.Record{ID: "fallback", Data: map[string]any{}}, "scheduler", now); got == "" {
		t.Fatal("fallback definition ID produced empty run ID")
	}
	future := recordmodel.Record{Data: map[string]any{"next_run_at": now.Add(time.Hour).Format(time.RFC3339)}}
	if got := schedulerScheduledRunTime(future, "scheduler", now, 1); !got.Equal(now) {
		t.Fatalf("future scheduled time = %v", got)
	}
	catchUpOne := recordmodel.Record{Data: map[string]any{"next_run_at": now.Add(-time.Hour).Format(time.RFC3339), "missed_window_policy": "catch_up_one"}}
	if got := schedulerScheduledRunTime(catchUpOne, "scheduler", now, 1); !got.Equal(now.Add(-time.Hour)) {
		t.Fatalf("catch-up-one scheduled time = %v", got)
	}
	bounded := recordmodel.Record{Data: map[string]any{"schedule_type": "interval", "interval_seconds": 60, "next_run_at": now.Add(-5 * time.Minute).Format(time.RFC3339), "missed_window_policy": "catch_up_bounded", "max_catchup_windows": 2}}
	if got := schedulerScheduledRunTime(bounded, "scheduler", now, 0); !got.Before(now) {
		t.Fatalf("bounded scheduled time = %v", got)
	}
	definition := recordmodel.Record{ID: "definition", Data: map[string]any{"missed_window_policy": "catch_up_bounded"}}
	if got := schedulerDefinitionCursorAnchor(definition, recordmodel.Record{Data: map[string]any{}}, now); !got.Equal(now) {
		t.Fatalf("missing cursor anchor = %v", got)
	}
	if got := schedulerDefinitionCursorAnchor(definition, recordmodel.Record{Data: map[string]any{"scheduled_for": "bad"}}, now); !got.Equal(now) {
		t.Fatalf("invalid cursor anchor = %v", got)
	}
	scheduled := now.Add(-time.Hour)
	if got := schedulerDefinitionCursorAnchor(definition, recordmodel.Record{Data: map[string]any{"scheduled_for": scheduled.Format(time.RFC3339)}}, now); !got.Equal(scheduled) {
		t.Fatalf("cursor anchor = %v", got)
	}
	definition.Data["missed_window_policy"] = "skip"
	if got := schedulerDefinitionCursorAnchor(definition, recordmodel.Record{}, now); !got.Equal(now) {
		t.Fatalf("skip cursor anchor = %v", got)
	}
}
