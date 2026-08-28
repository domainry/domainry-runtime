package integrationtest

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	. "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"

	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"

	"github.com/domainry/domainry-foundation/mutation"
	schedulerbusiness "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"
)

func schedulerRuntimeSystemScope() principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test scheduler worker operation")
}

func TestSchedulerClaimRunAllowsOnlyOneWorkerForSameWindow(t *testing.T) {
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := schedulerRuntimeTestObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatalf("sync scheduler storage: %v", err)
	}
	now := time.Date(2026, 7, 9, 16, 30, 0, 0, time.UTC)
	definition := schedulerRuntimeTestDefinition(now)
	if err := recordLegacyStore(store).InsertRecord(t.Context(), "default", objects[0], definition); err != nil {
		t.Fatalf("insert scheduler definition: %v", err)
	}
	services := make([]*RuntimeServices, 100)
	for index := range services {
		services[index] = newSchedulerRuntimeTestService(t, store, objects)
	}
	start := make(chan struct{})
	results := make(chan bool, len(services))
	var wg sync.WaitGroup
	for _, records := range services {
		wg.Add(1)
		go func(records *RuntimeServices) {
			defer wg.Done()
			<-start
			_, claimed, err := records.Applications().Scheduler.ClaimRun(t.Context(), definition, "scheduler", now, schedulerRuntimeSystemScope())
			if err != nil {
				t.Errorf("claim scheduler run: %v", err)
			}
			results <- claimed
		}(records)
	}
	close(start)
	wg.Wait()
	close(results)
	claimedCount := 0
	for claimed := range results {
		if claimed {
			claimedCount++
		}
	}
	if claimedCount != 1 {
		t.Fatalf("expected one worker claim, got %d", claimedCount)
	}
	page, err := recordLegacyStore(store).ListRecords(t.Context(), "default", objects[1], recordmodel.RecordListQuery{Page: 1, PageSize: 10})
	if err != nil || page.Total != 1 {
		t.Fatalf("job runs = %d, err=%v; want one", page.Total, err)
	}
}

func TestSchedulerClaimRunReclaimsExpiredLease(t *testing.T) {
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := schedulerRuntimeTestObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatalf("sync scheduler storage: %v", err)
	}
	now := time.Date(2026, 7, 9, 16, 30, 0, 0, time.UTC)
	definition := schedulerRuntimeTestDefinition(now)
	if err := recordLegacyStore(store).InsertRecord(t.Context(), "default", objects[0], definition); err != nil {
		t.Fatalf("insert scheduler definition: %v", err)
	}
	scheduler := newSchedulerRuntimeTestService(t, store, objects).Applications().Scheduler
	scheduler.ConfigureWorker(schedulerbusiness.WorkerConfig{Enabled: true, PollInterval: time.Second, BatchSize: 25, LeaseTTL: 2 * time.Minute})
	run, claimed, err := scheduler.ClaimRun(t.Context(), definition, "scheduler", now, schedulerRuntimeSystemScope())
	if err != nil || !claimed {
		t.Fatalf("initial claim = %#v, %v, %v", run, claimed, err)
	}
	firstOwner := schedulerbusiness.ExistingStringBefore(run, "lease_owner")
	if err := scheduler.HeartbeatRun(t.Context(), run, now.Add(time.Minute), schedulerRuntimeSystemScope()); err != nil {
		t.Fatalf("heartbeat lease: %v", err)
	}
	if _, claimed, err = scheduler.ClaimRun(t.Context(), definition, "scheduler", now.Add(2*time.Minute), schedulerRuntimeSystemScope()); err != nil || claimed {
		t.Fatalf("claim before expiry = %v, %v", claimed, err)
	}
	reclaimed, claimed, err := scheduler.ClaimRun(t.Context(), definition, "scheduler", now.Add(4*time.Minute), schedulerRuntimeSystemScope())
	if err != nil || !claimed {
		t.Fatalf("claim after expiry = %#v, %v, %v", reclaimed, claimed, err)
	}
	if reclaimed.ID != run.ID || schedulerbusiness.ExistingStringBefore(reclaimed, "lease_owner") != firstOwner || schedulerpolicy.SchedulerInt(reclaimed.Data["attempt"], 0) != 1 || schedulerpolicy.SchedulerInt(reclaimed.Data["fencing_token"], 0) <= schedulerpolicy.SchedulerInt(run.Data["fencing_token"], 0) {
		t.Fatalf("unexpected reclaimed lease: %#v", reclaimed)
	}
	if err := scheduler.FinishRun(t.Context(), run, nil, nil, now.Add(4*time.Minute), schedulerRuntimeSystemScope()); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("stale scheduler owner completion error=%v", err)
	}
}

func TestSchedulerClaimRunHonorsRetryAndCompletedWindow(t *testing.T) {
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := schedulerRuntimeTestObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatalf("sync scheduler storage: %v", err)
	}
	now := time.Date(2026, 7, 9, 16, 30, 0, 0, time.UTC)
	definition := schedulerRuntimeTestDefinition(now)
	if err := recordLegacyStore(store).InsertRecord(t.Context(), "default", objects[0], definition); err != nil {
		t.Fatalf("insert scheduler definition: %v", err)
	}
	scheduler := newSchedulerRuntimeTestService(t, store, objects).Applications().Scheduler
	run, claimed, err := scheduler.ClaimRun(t.Context(), definition, "scheduler", now, schedulerRuntimeSystemScope())
	if err != nil || !claimed {
		t.Fatalf("initial claim = %#v, %v, %v", run, claimed, err)
	}
	run.Data["status"] = "retrying"
	run.Data["lease_owner"], run.Data["lease_expires_at"] = "", ""
	run.Data["next_retry_at"] = now.Add(5 * time.Minute).Format(time.RFC3339)
	run.UpdatedAt = now.Add(time.Second).Format(time.RFC3339)
	if err := recordLegacyStore(store).UpdateRecord(t.Context(), "default", objects[1], run); err != nil {
		t.Fatalf("mark retrying: %v", err)
	}
	if _, claimed, err = scheduler.ClaimRun(t.Context(), definition, "scheduler", now.Add(time.Minute), schedulerRuntimeSystemScope()); err != nil || claimed {
		t.Fatalf("claim before retry = %v, %v", claimed, err)
	}
	retried, claimed, err := scheduler.ClaimRun(t.Context(), definition, "scheduler", now.Add(6*time.Minute), schedulerRuntimeSystemScope())
	if err != nil || !claimed || schedulerpolicy.SchedulerInt(retried.Data["attempt"], 0) != 2 {
		t.Fatalf("retry claim = %#v, %v, %v", retried, claimed, err)
	}
	retried.Data["status"], retried.Data["lease_owner"], retried.Data["lease_expires_at"] = "succeeded", "", ""
	retried.UpdatedAt = now.Add(7 * time.Minute).Format(time.RFC3339)
	if err := recordLegacyStore(store).UpdateRecord(t.Context(), "default", objects[1], retried); err != nil {
		t.Fatalf("mark succeeded: %v", err)
	}
	existing, claimed, err := newSchedulerRuntimeTestService(t, store, objects).Applications().Scheduler.ClaimRun(t.Context(), definition, "scheduler", now.Add(10*time.Minute), schedulerRuntimeSystemScope())
	if err != nil || claimed || existing.ID != retried.ID || strings.TrimSpace(fmt.Sprint(existing.Data["status"])) != "succeeded" {
		t.Fatalf("restart claim = %#v, %v, %v", existing, claimed, err)
	}
}

func TestSchedulerDisabledWorkerStillAllowsManualRun(t *testing.T) {
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := schedulerRuntimeTestObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatalf("sync scheduler storage: %v", err)
	}
	now := time.Date(2026, 7, 9, 16, 30, 0, 0, time.UTC)
	definition := schedulerRuntimeTestDefinition(now)
	if err := recordLegacyStore(store).InsertRecord(t.Context(), "default", objects[0], definition); err != nil {
		t.Fatalf("insert scheduler definition: %v", err)
	}
	publishSchedulerDefinitionStoreFixture(t, store, definition.ID, definition.Data)
	records := runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{TemplateID: "scheduler-runtime-test", TemplateVersion: "1", Name: "Scheduler Runtime Test", Objects: objects, Views: nil, Actions: nil, Workflows: []definitionmodel.WorkflowSchema{{Key: "scheduled.noop", Trigger: map[string]any{"type": "scheduled:noop"}, Action: map[string]any{"type": "notify"}, Enabled: true}}, AutomationRules: nil, Dictionaries: nil, Integrations: integrationmodel.IntegrationSchema{}, Reports: nil, Entrypoints: nil, Skills: nil, Agents: nil, Store: store})
	records.Applications().Scheduler.ConfigureWorker(schedulerbusiness.WorkerConfig{Enabled: false, PollInterval: time.Millisecond, BatchSize: 25, LeaseTTL: time.Minute})
	ctx, cancel := context.WithCancel(context.Background())
	done := records.Applications().Scheduler.StartWorker(ctx, schedulerbusiness.WorkerConfig{Enabled: false, PollInterval: time.Millisecond, BatchSize: 25, LeaseTTL: time.Minute}, true)
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	runs, err := recordLegacyStore(store).ListRecords(t.Context(), "default", objects[1], recordmodel.RecordListQuery{Page: 1, PageSize: 10})
	if err != nil || runs.Total != 0 {
		t.Fatalf("disabled worker runs = %d, err=%v", runs.Total, err)
	}
	result, err := records.Applications().Scheduler.RunJob(t.Context(), definition.ID, "integration-manual-run", schedulerRuntimePrincipal())
	if err != nil || result.Run.ID == "" {
		t.Fatalf("manual run = %#v, %v", result, err)
	}
}
