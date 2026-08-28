package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/mutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

func schedulerPersistenceService(repository *schedulerRepositoryFake, now time.Time) *SchedulerApplicationService {
	service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), nil, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
	return service
}

func TestSchedulerClaimExistingRunStateMatrix(t *testing.T) {
	now := time.Date(2026, 7, 19, 16, 0, 0, 0, time.UTC)
	definition := recordmodel.Record{ID: "definition-1", Data: map[string]any{"key": "nightly", "schedule_type": "interval", "interval_seconds": 60, "max_attempts": 3}}
	tests := []struct {
		name        string
		data        map[string]any
		updateMatch bool
		wantClaimed bool
		wantUpdate  int
		wantEvents  int
	}{
		{name: "live lease", data: map[string]any{"status": "leased", "lease_expires_at": now.Add(time.Minute).Format(time.RFC3339)}, wantUpdate: 0},
		{name: "retry not due", data: map[string]any{"status": "retrying", "next_retry_at": now.Add(time.Minute).Format(time.RFC3339)}, wantUpdate: 0},
		{name: "terminal", data: map[string]any{"status": "succeeded"}, wantUpdate: 0},
		{name: "queued compare miss", data: map[string]any{"status": "queued", "attempt": 0, "fencing_token": 1}, updateMatch: false, wantUpdate: 1},
		{name: "queued claimed", data: map[string]any{"status": "queued", "attempt": 0, "fencing_token": 1}, updateMatch: true, wantClaimed: true, wantUpdate: 1, wantEvents: 1},
		{name: "expired lease claimed", data: map[string]any{"status": "leased", "attempt": 1, "fencing_token": 2, "lease_expires_at": now.Add(-time.Minute).Format(time.RFC3339)}, updateMatch: true, wantClaimed: true, wantUpdate: 1, wantEvents: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			existing := recordmodel.Record{ID: RunIDForDefinition(definition, "scheduler", now), UpdatedAt: "old", Data: test.data}
			repository := &schedulerRepositoryFake{
				get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
					return existing, true, nil
				},
			}
			updates := 0
			repository.update = func(_ context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record, conditions map[string]any) (bool, error) {
				updates++
				if workspaceID != "workspace-a" || object.Key != "job_run" || conditions["status"] == nil {
					t.Fatalf("update workspace=%q object=%q conditions=%+v", workspaceID, object.Key, conditions)
				}
				return test.updateMatch, nil
			}
			service := schedulerPersistenceService(repository, now)
			events := 0
			service.insertRecord = func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ recordmodel.Record, _ string) error {
				if object.Key == "job_run_event" {
					events++
				}
				return nil
			}
			got, claimed, err := service.claimRun(t.Context(), "workspace-a", definition, "scheduler", now)
			if err != nil || claimed != test.wantClaimed || got.ID != existing.ID || updates != test.wantUpdate || events != test.wantEvents {
				t.Fatalf("claim = %+v claimed=%v err=%v updates=%d events=%d", got, claimed, err, updates, events)
			}
		})
	}
}

func TestSchedulerClaimNewRunAndInsertRace(t *testing.T) {
	now := time.Date(2026, 7, 19, 17, 0, 0, 0, time.UTC)
	definition := recordmodel.Record{ID: "definition-1", Data: map[string]any{"key": "nightly", "schedule_type": "interval", "interval_seconds": 60}}

	t.Run("new claim", func(t *testing.T) {
		repository := &schedulerRepositoryFake{}
		service := schedulerPersistenceService(repository, now)
		run, claimed, err := service.claimRunWithKey(t.Context(), "workspace-a", definition, "manual_run", "request-1", now)
		if err != nil || !claimed || len(repository.committed) != 1 || len(repository.committed[0]) != 3 || repository.committed[0][0].Record.ID != run.ID || run.Data["idempotency_scope"] != "scheduler.manual" {
			t.Fatalf("new claim = %+v claimed=%v err=%v commits=%+v", run, claimed, err, repository.committed)
		}
	})

	t.Run("concurrent insert replay", func(t *testing.T) {
		wantErr := errors.New("unique conflict")
		getCalls := 0
		existing := recordmodel.Record{ID: "winner", Data: map[string]any{"status": "leased"}}
		repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			getCalls++
			if getCalls == 1 {
				return recordmodel.Record{}, false, nil
			}
			return existing, true, nil
		}}
		repository.commit = func(context.Context, string, []transactionmodel.RecordMutationCommit) error {
			return wantErr
		}
		service := schedulerPersistenceService(repository, now)
		got, claimed, err := service.claimRun(t.Context(), "workspace-a", definition, "scheduler", now)
		if err != nil || claimed || got.ID != existing.ID {
			t.Fatalf("insert race = %+v claimed=%v err=%v", got, claimed, err)
		}
	})

	t.Run("insert error", func(t *testing.T) {
		wantErr := errors.New("insert failed")
		repository := &schedulerRepositoryFake{commit: func(context.Context, string, []transactionmodel.RecordMutationCommit) error { return wantErr }}
		service := schedulerPersistenceService(repository, now)
		if _, _, err := service.claimRun(t.Context(), "workspace-a", definition, "scheduler", now); !errors.Is(err, wantErr) {
			t.Fatalf("insert error = %v", err)
		}
	})
}

func TestSchedulerHeartbeatAndConditionalUpdate(t *testing.T) {
	now := time.Date(2026, 7, 19, 18, 0, 0, 0, time.UTC)
	run := recordmodel.Record{ID: "run-1", Data: map[string]any{"status": "leased", "lease_owner": "worker-a", "fencing_token": 2}}
	service := schedulerPersistenceService(&schedulerRepositoryFake{}, now)
	if err := service.HeartbeatRun(t.Context(), run, now, principalmodel.SystemScope{}); apperror.CodeOf(err) != "backend.system_scope_required" {
		t.Fatalf("scope error = %v", err)
	}
	for _, invalid := range []recordmodel.Record{{ID: "run"}, {ID: "run", Data: map[string]any{"lease_owner": "worker"}}} {
		if err := service.heartbeatRun(t.Context(), "workspace-a", invalid, now); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
			t.Fatalf("invalid lease error = %v", err)
		}
	}
	if err := service.heartbeatRun(t.Context(), "workspace-a", run, now); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("missing run error = %v", err)
	}
	wantReadErr := errors.New("read failed")
	service.repository = &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, wantReadErr
	}}
	if err := service.heartbeatRun(t.Context(), "workspace-a", run, now); !errors.Is(err, wantReadErr) {
		t.Fatalf("heartbeat read error = %v", err)
	}

	repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return run, true, nil
	}}
	service.repository = repository
	repository.update = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
		return false, nil
	}
	if err := service.heartbeatRun(t.Context(), "workspace-a", run, now); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("lost compare error = %v", err)
	}
	repository.update = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
		return true, nil
	}
	if err := service.HeartbeatRun(t.Context(), run, now, schedulerRuntimeScope()); err != nil {
		t.Fatalf("heartbeat = %v", err)
	}

	wantErr := errors.New("update failed")
	repository.update = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
		return false, wantErr
	}
	if err := service.heartbeatRun(t.Context(), "workspace-a", run, now); !errors.Is(err, wantErr) || apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("update error = %v", err)
	}
}

func TestSchedulerPersistenceValueAndMutationPolicyHelpers(t *testing.T) {
	if ExistingStringBefore(recordmodel.Record{Data: map[string]any{}}, "missing") != "" || ExistingStringBefore(recordmodel.Record{Data: map[string]any{"key": " value "}}, "key") != "value" {
		t.Fatal("ExistingStringBefore mismatch")
	}
	if firstNonNil(nil, nil, "value", "later") != "value" || firstNonNil(nil) != nil {
		t.Fatal("firstNonNil mismatch")
	}
	for _, key := range []string{"scheduler_cursor", "job_run", "job_run_event", "job_dead_letter", "report_query_run", "report_export_audit", "download_task", "report_definition"} {
		if err := validateSchedulerMutationObject(definitionmodel.ObjectSchema{Key: key}); err != nil {
			t.Fatalf("allowed object %q rejected: %v", key, err)
		}
	}
	if err := validateSchedulerMutationObject(definitionmodel.ObjectSchema{Key: "customer"}); apperror.CodeOf(err) != "backend.record.internal_mutation_policy_denied" {
		t.Fatalf("denied object error = %v", err)
	}
	service := schedulerPersistenceService(&schedulerRepositoryFake{}, time.Now())
	if updated, err := service.updateRunIfCurrent(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "customer"}, recordmodel.Record{}, nil); updated || apperror.CodeOf(err) != "backend.record.internal_mutation_policy_denied" {
		t.Fatalf("denied update = %v, %v", updated, err)
	}
}

func TestSchedulerClaimDeadLetterAndPublicScope(t *testing.T) {
	now := time.Date(2026, 7, 20, 7, 0, 0, 0, time.UTC)
	definition := recordmodel.Record{ID: "definition-1", Data: map[string]any{"key": "nightly", "max_attempts": 3}}
	runID := RunIDForDefinition(definition, "scheduler", now)
	existing := recordmodel.Record{ID: runID, Data: map[string]any{"scheduler_definition_key": definition.ID, "status": "failed", "attempt": 3, "max_attempts": 3, "error_message": "failed"}}
	repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
		if object.Key == "job_run" {
			return existing, true, nil
		}
		return recordmodel.Record{}, false, nil
	}}
	service := schedulerPersistenceService(repository, now)
	inserted, updated := 0, 0
	service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
		inserted++
		return nil
	}
	service.updateRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
		updated++
		return nil
	}
	got, claimed, err := service.claimRun(t.Context(), "workspace-a", definition, "scheduler", now)
	if err != nil || claimed || got.Data["status"] != "dead_letter" || inserted != 2 || updated != 1 {
		t.Fatalf("dead-letter claim = %+v claimed=%v err=%v inserted=%d updated=%d", got, claimed, err, inserted, updated)
	}

	if _, _, err := service.ClaimRun(t.Context(), definition, "scheduler", now, principalmodel.SystemScope{}); apperror.CodeOf(err) != "backend.system_scope_required" {
		t.Fatalf("claim scope error = %v", err)
	}
	repository.get = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return existing, true, nil
	}
	got, claimed, err = service.ClaimRun(t.Context(), definition, "scheduler", now, schedulerRuntimeScope())
	if err != nil || claimed || got.ID != existing.ID {
		t.Fatalf("public claim = %+v claimed=%v err=%v", got, claimed, err)
	}
}

func TestSchedulerClaimAndPersistenceFailureWindows(t *testing.T) {
	now := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	definition := recordmodel.Record{ID: "definition-1", Data: map[string]any{"key": "nightly", "max_attempts": 3}}
	wantErr := errors.New("persistence failed")

	t.Run("existing read", func(t *testing.T) {
		repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			return recordmodel.Record{}, false, wantErr
		}}
		service := schedulerPersistenceService(repository, now)
		if _, _, err := service.claimRun(t.Context(), "workspace-a", definition, "scheduler", now); !errors.Is(err, wantErr) || apperror.CodeOf(err) != "backend.internal" {
			t.Fatalf("claim get error = %v", err)
		}
	})

	t.Run("conditional update and event", func(t *testing.T) {
		existing := recordmodel.Record{ID: RunIDForDefinition(definition, "scheduler", now), Data: map[string]any{"status": "retrying", "next_retry_at": now.Add(-time.Second).Format(time.RFC3339), "attempt": 1, "fencing_token": 1}}
		repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			return existing, true, nil
		}}
		repository.update = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
			return false, wantErr
		}
		service := schedulerPersistenceService(repository, now)
		if _, _, err := service.claimRun(t.Context(), "workspace-a", definition, "scheduler", now); !errors.Is(err, wantErr) {
			t.Fatalf("claim update error = %v", err)
		}
		repository.update = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
			return true, nil
		}
		existing = recordmodel.Record{ID: RunIDForDefinition(definition, "scheduler", now), Data: map[string]any{"status": "retrying", "next_retry_at": now.Add(-time.Second).Format(time.RFC3339), "attempt": 1, "fencing_token": 1}}
		service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			return wantErr
		}
		if _, _, err := service.claimRun(t.Context(), "workspace-a", definition, "scheduler", now); !errors.Is(err, wantErr) {
			t.Fatalf("claim event error = %v", err)
		}
	})

	t.Run("new run and events commit atomically", func(t *testing.T) {
		repository := &schedulerRepositoryFake{commit: func(_ context.Context, _ string, commits []transactionmodel.RecordMutationCommit) error {
			if len(commits) != 3 || commits[0].Object.Key != "job_run" || commits[1].Object.Key != "job_run_event" || commits[2].Object.Key != "job_run_event" {
				t.Fatalf("claim commits = %+v", commits)
			}
			return wantErr
		}}
		service := schedulerPersistenceService(repository, now)
		if _, _, err := service.claimRun(t.Context(), "workspace-a", definition, "scheduler", now); !errors.Is(err, wantErr) {
			t.Fatalf("atomic claim error = %v", err)
		}
	})
}
