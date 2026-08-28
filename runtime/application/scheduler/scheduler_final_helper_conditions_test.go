package scheduler

import (
	"context"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

func TestSchedulerScheduleRemainingConditionEdges(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	definition := recordmodel.Record{ID: "fallback", Data: map[string]any{}}
	if got := schedulerRunIDForDefinition(definition, "scheduler", now); got[:len("jobrun_fallback_")] != "jobrun_fallback_" {
		t.Fatalf("fallback run id=%q", got)
	}
	if got := schedulerRunIDForDefinition(recordmodel.Record{ID: "blank", Data: map[string]any{"key": ""}}, "scheduler", now); got[:len("jobrun_blank_")] != "jobrun_blank_" {
		t.Fatalf("blank key run id=%q", got)
	}
	if got := schedulerManualRunID(definition, "short"); got == "" {
		t.Fatal("short manual key produced empty id")
	}
	if _, ok := schedulerDefinitionNextRunAt(recordmodel.Record{Data: map[string]any{"next_run_at": ""}}); ok {
		t.Fatal("blank next run accepted")
	}
	bounded := recordmodel.Record{Data: map[string]any{"schedule_type": "interval", "interval_seconds": 60}}
	if got := schedulerBoundedCatchupWindow(bounded, now.Add(-5*time.Minute), now, 0); got.After(now) {
		t.Fatalf("bounded window=%v", got)
	}
	_ = schedulerBoundedCatchupWindow(bounded, now.Add(-2000*time.Minute), now, 2000)
	if got := schedulerDefinitionCursorAnchor(recordmodel.Record{Data: map[string]any{"missed_window_policy": "catch_up_bounded"}}, recordmodel.Record{Data: map[string]any{"scheduled_for": ""}}, now); !got.Equal(now) {
		t.Fatalf("blank cursor=%v", got)
	}
}

func TestSchedulerStatusRemainingRunConditionEdges(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	repository := &schedulerRepositoryFake{list: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		if object.Key != "job_run" {
			return recordmodel.RecordPageResult{}, nil
		}
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{
			{Data: map[string]any{"status": "", "started_at": now.Format(time.RFC3339), "finished_at": "bad"}},
			{Data: map[string]any{"status": "leased", "lease_expires_at": now.Add(time.Minute).Format(time.RFC3339)}},
		}}, nil
	}}
	service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), nil, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
	status, err := service.Status(t.Context(), schedulerRuntimeScope())
	if err != nil || status["lease_expirations"] != 0 {
		t.Fatalf("status=%#v err=%v", status, err)
	}
}

func TestSchedulerPreviewScansNonDefinitionAndErrorCodeFallback(t *testing.T) {
	schema := schedulerSchemaStub{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{
		{Key: "other"}, {Key: "job_definition"},
	}}}
	service := NewSchedulerApplicationService(schema, nil, &schedulerRepositoryFake{}, nil)
	_, _ = service.PreviewDefinition(t.Context(), map[string]any{}, schedulerTestPrincipal("workspace.admin"))
	if code := serviceErrorCode(&apperror.AppError{Code: " "}); code != "backend.internal" {
		t.Fatalf("code=%q", code)
	}
}
