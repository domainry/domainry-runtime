package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestSchedulerDueDefinitionsAndCursorPaginationCoverFilteringEdges(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	if _, err := (&SchedulerApplicationService{}).dueDefinitions(t.Context(), "workspace", definitionmodel.ObjectSchema{}, now); err == nil {
		t.Fatal("missing definition source accepted")
	}
	definitions := []recordmodel.Record{
		{ID: "future", Data: map[string]any{"status": "enabled", "target_type": "workflow", "target_key": "scheduled:*", "next_run_at": now.Add(time.Hour).Format(time.RFC3339)}},
		{ID: "unsupported", Data: map[string]any{"status": "enabled", "target_type": "message"}},
		{ID: "workflow", Data: map[string]any{"status": "enabled", "target_type": "workflow", "target_key": "manual"}},
		{ID: "empty-export", Data: map[string]any{"status": "enabled", "target_type": "report_export", "target_key": ""}},
		{ID: "empty-refresh", Data: map[string]any{"status": "enabled", "target_type": "report_snapshot_refresh", "target_key": ""}},
		{ID: "refresh", Data: map[string]any{"status": "enabled", "target_type": "report_snapshot_refresh", "target_key": "sales"}},
	}
	cursorPage := make([]recordmodel.Record, 500)
	for index := range cursorPage {
		cursorPage[index] = recordmodel.Record{ID: "cursor", Data: map[string]any{"scheduler_definition_key": "key"}}
	}
	cursorPage[0] = recordmodel.Record{ID: "fallback-empty", Data: map[string]any{"scheduler_definition_key": ""}}
	cursorPage[1] = recordmodel.Record{ID: "fallback-nil", Data: map[string]any{"scheduler_definition_key": nil}}
	repository := &schedulerRepositoryFake{list: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		if object.Key == "job_definition" {
			return recordmodel.RecordPageResult{Items: definitions}, nil
		}
		if query.Page == 1 {
			return recordmodel.RecordPageResult{Items: cursorPage, Total: 501}, nil
		}
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "last"}}, Total: 501}, nil
	}}
	service := schedulerPersistenceService(repository, now)
	due, err := service.dueDefinitions(t.Context(), "workspace", definitionmodel.ObjectSchema{}, now)
	if err != nil || len(due) != 1 || due[0].ID != "refresh" {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	repository.list = func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		if object.Key == "job_definition" {
			return recordmodel.RecordPageResult{Items: definitions}, nil
		}
		return recordmodel.RecordPageResult{}, errors.New("cursor")
	}
	if _, err := service.dueDefinitions(t.Context(), "workspace", definitionmodel.ObjectSchema{}, now); err == nil {
		t.Fatal("cursor error ignored")
	}
	repository.list = func(_ context.Context, _ string, _ definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{Items: cursorPage, Total: 500}, nil
	}
	if cursors, err := service.schedulerCursorMap(t.Context(), "workspace"); err != nil || len(cursors) == 0 {
		t.Fatalf("full final cursor page=%d err=%v", len(cursors), err)
	}
}

func TestSchedulerClaimDueRecordTimersCoversScopeSourceListAndEmptyEdges(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	if _, err := recordTimerTestService(&schedulerRepositoryFake{}, now, true).ClaimDueRecordTimers(t.Context(), "workspace", now, 1, principalmodel.SystemScope{}); err == nil {
		t.Fatal("invalid scope accepted")
	}
	if _, err := recordTimerTestService(&schedulerRepositoryFake{}, now, false).ClaimDueRecordTimers(t.Context(), "workspace", now, 1, schedulerRuntimeScope()); err == nil {
		t.Fatal("missing timer object accepted")
	}
	want := errors.New("list")
	repository := &schedulerRepositoryFake{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{}, want
	}}
	if _, err := recordTimerTestService(repository, now, true).ClaimDueRecordTimers(t.Context(), "workspace", time.Time{}, 1, schedulerRuntimeScope()); !errors.Is(err, want) {
		t.Fatalf("list err=%v", err)
	}
	calls := 0
	repository.list = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		calls++
		if calls == 2 {
			return recordmodel.RecordPageResult{}, want
		}
		return recordmodel.RecordPageResult{}, nil
	}
	if _, err := recordTimerTestService(repository, now, true).ClaimDueRecordTimers(t.Context(), "workspace", now, 2, schedulerRuntimeScope()); !errors.Is(err, want) {
		t.Fatalf("expired list err=%v", err)
	}
	repository.list = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "live", Data: map[string]any{"status": "leased", "lease_expires_at": now.Add(time.Minute).Format(time.RFC3339Nano)}}}, Total: 1}, nil
	}
	if leases, err := recordTimerTestService(repository, now, true).ClaimDueRecordTimers(t.Context(), "workspace", now, 1, schedulerRuntimeScope()); err != nil || leases != nil {
		t.Fatalf("ineligible leases=%#v err=%v", leases, err)
	}
}

func TestSchedulerClaimDueRecordTimersCoversBatchAndRowCASFallbacks(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	candidate := recordmodel.Record{ID: "timer", Data: map[string]any{"status": "scheduled", "fencing_token": 1}}
	newRepository := func() *schedulerRepositoryFake {
		return &schedulerRepositoryFake{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{candidate}, Total: 1}, nil
		}}
	}
	repository := newRepository()
	if leases, err := recordTimerTestService(repository, now, true).ClaimDueRecordTimers(t.Context(), "workspace", now, 1, schedulerRuntimeScope()); err != nil || len(leases) != 1 {
		t.Fatalf("batch leases=%#v err=%v", leases, err)
	}
	repository = newRepository()
	repository.commit = func(context.Context, string, []transactionmodel.RecordMutationCommit) error {
		return errors.New("conflict")
	}
	repository.update = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
		return false, errors.New("update")
	}
	if _, err := recordTimerTestService(repository, now, true).ClaimDueRecordTimers(t.Context(), "workspace", now, 1, schedulerRuntimeScope()); err == nil {
		t.Fatal("row update error ignored")
	}
	repository.update = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
		return false, nil
	}
	if leases, err := recordTimerTestService(repository, now, true).ClaimDueRecordTimers(t.Context(), "workspace", now, 1, schedulerRuntimeScope()); err != nil || len(leases) != 0 {
		t.Fatalf("compare miss leases=%#v err=%v", leases, err)
	}
	repository.update = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
		return true, nil
	}
	if leases, err := recordTimerTestService(repository, now, true).ClaimDueRecordTimers(t.Context(), "workspace", now, 1, schedulerRuntimeScope()); err != nil || len(leases) != 1 {
		t.Fatalf("row claim leases=%#v err=%v", leases, err)
	}
	repository = newRepository()
	repository.commit = func(context.Context, string, []transactionmodel.RecordMutationCommit) error {
		candidate.Data["status"] = "leased"
		candidate.Data["lease_expires_at"] = now.Add(time.Minute).Format(time.RFC3339Nano)
		return errors.New("conflict")
	}
	if leases, err := recordTimerTestService(repository, now, true).ClaimDueRecordTimers(t.Context(), "workspace", now, 1, schedulerRuntimeScope()); err != nil || len(leases) != 0 {
		t.Fatalf("fallback ineligible leases=%#v err=%v", leases, err)
	}
}

func TestPrepareRecordTimerClaimCoversLeasedParseLiveExpiredAndConditions(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	for _, expires := range []string{"bad", now.Add(time.Minute).Format(time.RFC3339Nano)} {
		if _, _, _, eligible := prepareRecordTimerClaim(recordmodel.Record{Data: map[string]any{"status": "leased", "lease_expires_at": expires}}, "worker", now, time.Minute); eligible {
			t.Fatalf("lease %q eligible", expires)
		}
	}
	updated, lease, conditions, eligible := prepareRecordTimerClaim(recordmodel.Record{ID: "timer", Data: map[string]any{"status": "leased", "lease_expires_at": now.Add(-time.Minute).Format(time.RFC3339Nano), "fencing_token": 2}}, "worker", now, time.Minute)
	if !eligible || lease.Token != 3 || updated.Data["status"] != "leased" || conditions["lease_expires_at"] == nil {
		t.Fatalf("updated=%#v lease=%#v conditions=%#v eligible=%v", updated, lease, conditions, eligible)
	}
}
