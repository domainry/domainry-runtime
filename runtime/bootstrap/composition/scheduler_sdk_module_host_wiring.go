package composition

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	"github.com/domainry/domainry-scheduler-sdk/modulehost"
)

// schedulerSDKModuleHost adapts Runtime-owned published metadata, durable
// records and downstream owners to the extracted Scheduler protocol.
type schedulerSDKModuleHost struct {
	scheduler    *schedulerapplication.SchedulerApplicationService
	integrations *integrationapplication.IntegrationApplicationService

	mu          sync.Mutex
	revision    int64
	reconciled  bool
	definitions map[string]recordmodel.Record
	claimed     map[string]schedulerSDKClaim
	completed   map[string]bool
}

type schedulerSDKClaim struct {
	definition recordmodel.Record
	run        recordmodel.Record
}

func NewSchedulerSDKModuleHost(scheduler *schedulerapplication.SchedulerApplicationService, integrations *integrationapplication.IntegrationApplicationService) modulehost.Host {
	if scheduler != nil {
		scheduler.UseRepositoryAuthoritativeClock(context.Background())
	}
	return &schedulerSDKModuleHost{scheduler: scheduler, integrations: integrations, definitions: map[string]recordmodel.Record{}, claimed: map[string]schedulerSDKClaim{}, completed: map[string]bool{}}
}

func (h *schedulerSDKModuleHost) Definitions() modulehost.DefinitionProvider         { return h }
func (h *schedulerSDKModuleHost) Runs() modulehost.RunStore                          { return h }
func (h *schedulerSDKModuleHost) Dispatcher() modulehost.Dispatcher                  { return h }
func (h *schedulerSDKModuleHost) HTTPConnections() modulehost.HTTPConnectionProvider { return h }

func (h *schedulerSDKModuleHost) Snapshot(ctx context.Context) (schedulersdk.DefinitionSnapshot, error) {
	if h.scheduler == nil {
		return schedulersdk.DefinitionSnapshot{}, fmt.Errorf("Runtime Scheduler application is unavailable")
	}
	records, err := h.scheduler.PublishedDefinitions(ctx, schedulerSDKSystemPrincipal("scheduler.definition.read"))
	if err != nil {
		return schedulersdk.DefinitionSnapshot{}, err
	}
	h.mu.Lock()
	h.revision++
	h.reconciled = false
	h.definitions = make(map[string]recordmodel.Record, len(records))
	definitions := make([]schedulersdk.Definition, 0, len(records))
	for _, record := range records {
		h.definitions[record.ID] = record
		definitions = append(definitions, schedulerSDKDefinition(record))
	}
	revision := h.revision
	h.mu.Unlock()
	return schedulersdk.DefinitionSnapshot{Revision: revision, Definitions: definitions}, nil
}

func schedulerSDKDefinition(record recordmodel.Record) schedulersdk.Definition {
	data := record.Data
	targetType := schedulerSDKString(data, "target_type")
	target := schedulersdk.TargetRef{Type: "runtime_operation", Owner: targetType, Operation: schedulerSDKString(data, "target_key"), Payload: json.RawMessage(schedulerSDKStringDefault(data, "payload_json", "{}"))}
	if targetType == "http" {
		// In Module topology Runtime's Connector layer owns connection secrets,
		// provider policy and private-network reachability. SaaS interprets the
		// published dispatch_mode on its own control plane.
		target = schedulersdk.TargetRef{Type: "http", ConnectionKey: schedulerSDKString(data, "connection_key"), Operation: schedulerSDKStringDefault(data, "operation", schedulerSDKString(data, "target_key")), DispatchMode: "runtime_callback", Payload: json.RawMessage(schedulerSDKStringDefault(data, "payload_json", "{}"))}
	}
	revision := strings.TrimSpace(record.UpdatedAt)
	if revision == "" {
		revision = "published"
	}
	return schedulersdk.Definition{
		Key: record.ID, Name: schedulerSDKString(data, "name"), Status: schedulerSDKString(data, "status"), Revision: revision,
		Schedule: schedulersdk.Schedule{Type: schedulerSDKString(data, "schedule_type"), Expression: schedulerSDKString(data, "schedule_expression"), Timezone: schedulerSDKString(data, "timezone"), IntervalSeconds: schedulerSDKInt(data["interval_seconds"]), TimeOfDay: schedulerSDKString(data, "time_of_day"), DayOfWeek: schedulerSDKString(data, "day_of_week"), DayOfMonth: schedulerSDKInt(data["day_of_month"])},
		Target:   target,
		Policy:   schedulersdk.Policy{Misfire: schedulerSDKString(data, "missed_window_policy"), MaxCatchupWindows: schedulerSDKInt(data["max_catchup_windows"]), Timeout: time.Duration(schedulerSDKInt(data["timeout_seconds"])) * time.Second, MaxAttempts: schedulerSDKInt(data["max_attempts"]), RetryInitial: time.Duration(schedulerSDKInt(data["retry_delay_seconds"])) * time.Second, RetryMax: time.Duration(schedulerSDKInt(data["retry_max_delay_seconds"])) * time.Second},
	}
}

func (h *schedulerSDKModuleHost) Reconcile(ctx context.Context, _ schedulersdk.Definition, now time.Time) error {
	h.mu.Lock()
	if h.reconciled {
		h.mu.Unlock()
		return nil
	}
	h.reconciled = true
	h.mu.Unlock()
	_, err := h.scheduler.ProvisionPublishedDefinitions(ctx, now, schedulerSDKScope("reconcile published Scheduler definitions"))
	return err
}

func (*schedulerSDKModuleHost) DisableMissing(context.Context, []string, int64) error { return nil }

func (h *schedulerSDKModuleHost) Due(ctx context.Context, now time.Time, limit int) ([]modulehost.DueTrigger, error) {
	records, err := h.scheduler.DueDefinitions(ctx, definitionmodel.ObjectSchema{}, now, schedulerSDKScope("read due Scheduler definitions"))
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}
	result := make([]modulehost.DueTrigger, 0, len(records))
	for _, record := range records {
		definition := schedulerSDKDefinition(record)
		scheduledFor := now.UTC()
		if parsed, err := time.Parse(time.RFC3339, schedulerSDKString(record.Data, "next_run_at")); err == nil {
			scheduledFor = parsed.UTC()
		}
		result = append(result, modulehost.DueTrigger{Definition: definition, ScheduledFor: scheduledFor})
	}
	return result, nil
}

func (h *schedulerSDKModuleHost) Claim(ctx context.Context, due modulehost.DueTrigger, _ time.Duration) (schedulersdk.Run, bool, error) {
	h.mu.Lock()
	definition, found := h.definitions[due.Definition.Key]
	h.mu.Unlock()
	if !found {
		return schedulersdk.Run{}, false, fmt.Errorf("Scheduler definition %q is not published", due.Definition.Key)
	}
	runRecord, claimed, err := h.scheduler.ClaimRun(ctx, definition, "scheduler", due.ScheduledFor, schedulerSDKScope("claim Scheduler run"))
	if err != nil || !claimed {
		return schedulerSDKRun(runRecord, due.Definition, due.ScheduledFor), claimed, err
	}
	run := schedulerSDKRun(runRecord, due.Definition, due.ScheduledFor)
	h.mu.Lock()
	h.claimed[run.Trigger.RunID] = schedulerSDKClaim{definition: definition, run: runRecord}
	delete(h.completed, run.Trigger.RunID)
	h.mu.Unlock()
	return run, true, nil
}

func schedulerSDKRun(record recordmodel.Record, definition schedulersdk.Definition, scheduledFor time.Time) schedulersdk.Run {
	if parsed, err := time.Parse(time.RFC3339, schedulerSDKString(record.Data, "scheduled_for")); err == nil {
		scheduledFor = parsed.UTC()
	}
	createdAt, _ := time.Parse(time.RFC3339, record.CreatedAt)
	updatedAt, _ := time.Parse(time.RFC3339, record.UpdatedAt)
	leaseExpiresAt, _ := time.Parse(time.RFC3339, schedulerSDKString(record.Data, "lease_expires_at"))
	return schedulersdk.Run{Trigger: schedulersdk.Trigger{RunID: record.ID, DefinitionKey: definition.Key, DefinitionRev: definition.Revision, ScheduledFor: scheduledFor, WindowKey: scheduledFor.UTC().Format(time.RFC3339), Target: definition.Target, IdempotencyKey: record.ID, Attempt: schedulerSDKInt(record.Data["attempt"])}, Lease: schedulersdk.Lease{Owner: schedulerSDKString(record.Data, "lease_owner"), Token: int64(schedulerSDKInt(record.Data["fencing_token"])), ExpiresAt: leaseExpiresAt}, Status: schedulerSDKString(record.Data, "status"), CreatedAt: createdAt, UpdatedAt: updatedAt}
}

func (h *schedulerSDKModuleHost) Renew(ctx context.Context, run schedulersdk.Run, ttl time.Duration) (schedulersdk.Run, bool, error) {
	h.mu.Lock()
	claim, found := h.claimed[run.Trigger.RunID]
	h.mu.Unlock()
	if !found || claim.run.ID == "" {
		return run, false, nil
	}
	if run.Lease.Owner != schedulerSDKString(claim.run.Data, "lease_owner") || run.Lease.Token != int64(schedulerSDKInt(claim.run.Data["fencing_token"])) {
		return run, false, nil
	}
	now := run.UpdatedAt.UTC()
	if err := h.scheduler.HeartbeatRun(ctx, claim.run, now, schedulerSDKScope("renew Scheduler run lease")); err != nil {
		if mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
			return run, false, nil
		}
		return run, false, err
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	run.Lease.ExpiresAt = now.Add(ttl)
	run.UpdatedAt = now
	return run, true, nil
}

func (h *schedulerSDKModuleHost) Accept(ctx context.Context, run schedulersdk.Run, _ schedulersdk.DownstreamReceipt) error {
	claim, completed, err := h.takeClaim(run.Trigger.RunID)
	if err != nil || completed {
		return err
	}
	return h.scheduler.FinishRun(ctx, claim.run, nil, nil, run.CreatedAt.UTC(), schedulerSDKScope("accept Scheduler downstream receipt"))
}

func (h *schedulerSDKModuleHost) Fail(ctx context.Context, run schedulersdk.Run, executionErr error, _ time.Time) error {
	claim, completed, err := h.takeClaim(run.Trigger.RunID)
	if err != nil || completed {
		return err
	}
	return h.scheduler.FinishRun(ctx, claim.run, nil, executionErr, run.CreatedAt.UTC(), schedulerSDKScope("fail Scheduler downstream dispatch"))
}

func (h *schedulerSDKModuleHost) takeClaim(runID string) (schedulerSDKClaim, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	claim, found := h.claimed[runID]
	if !found {
		return schedulerSDKClaim{}, false, fmt.Errorf("Scheduler run %q has no Runtime claim", runID)
	}
	completed := h.completed[runID]
	delete(h.claimed, runID)
	delete(h.completed, runID)
	return claim, completed, nil
}

func (h *schedulerSDKModuleHost) List(ctx context.Context, limit int) ([]schedulersdk.Run, error) {
	state, err := h.scheduler.OpsState(ctx, schedulerSDKSystemPrincipal("operations.read"))
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(state.Runs) > limit {
		state.Runs = state.Runs[:limit]
	}
	runs := make([]schedulersdk.Run, 0, len(state.Runs))
	for _, item := range state.Runs {
		createdAt, _ := time.Parse(time.RFC3339, item.CreatedAt)
		updatedAt, _ := time.Parse(time.RFC3339, item.UpdatedAt)
		scheduledFor, _ := time.Parse(time.RFC3339, item.ScheduledFor)
		runs = append(runs, schedulersdk.Run{Trigger: schedulersdk.Trigger{RunID: item.ID, DefinitionKey: item.DefinitionKey, ScheduledFor: scheduledFor, Attempt: item.Attempt}, Status: item.Status, LastError: item.ErrorMessage, CreatedAt: createdAt, UpdatedAt: updatedAt})
	}
	return runs, nil
}

func (h *schedulerSDKModuleHost) Dispatch(ctx context.Context, trigger schedulersdk.Trigger) (schedulersdk.DownstreamReceipt, error) {
	if trigger.Target.Type == "http" {
		return h.dispatchHTTPCallback(ctx, trigger)
	}
	h.mu.Lock()
	claim, found := h.claimed[trigger.RunID]
	h.mu.Unlock()
	if !found {
		return schedulersdk.DownstreamReceipt{}, fmt.Errorf("Scheduler run %q has no Runtime claim", trigger.RunID)
	}
	startedAt, _ := time.Parse(time.RFC3339, claim.run.CreatedAt)
	result, err := h.scheduler.ProcessClaimedRun(ctx, claim.definition, claim.run, 25, schedulerSDKSystemPrincipal("scheduler.command"), startedAt.UTC())
	if err != nil {
		return schedulersdk.DownstreamReceipt{}, err
	}
	h.mu.Lock()
	h.completed[trigger.RunID] = true
	h.mu.Unlock()
	receiptID := trigger.RunID
	if len(result.Executions) > 0 && strings.TrimSpace(result.Executions[0].ID) != "" {
		receiptID = result.Executions[0].ID
	}
	return schedulersdk.DownstreamReceipt{ID: receiptID, Owner: trigger.Target.Owner, Status: "accepted"}, nil
}

func (h *schedulerSDKModuleHost) dispatchHTTPCallback(ctx context.Context, trigger schedulersdk.Trigger) (schedulersdk.DownstreamReceipt, error) {
	if h.integrations == nil {
		return schedulersdk.DownstreamReceipt{}, fmt.Errorf("Runtime Integration application is unavailable")
	}
	principal := schedulerSDKWorkspacePrincipal("integration.invoke")
	connection, err := h.integrations.GetIntegrationConnection(ctx, trigger.Target.ConnectionKey, principal)
	if err != nil {
		return schedulersdk.DownstreamReceipt{}, err
	}
	payload := map[string]any{}
	if len(trigger.Target.Payload) > 0 {
		if err := json.Unmarshal(trigger.Target.Payload, &payload); err != nil {
			return schedulersdk.DownstreamReceipt{}, fmt.Errorf("decode Scheduler HTTP payload: %w", err)
		}
	}
	result, err := h.integrations.ExecuteIntegrationSyncCall(ctx, integrationapplication.SyncCallRequest{ConnectorKey: connection.ConnectorKey, ConnectionKey: connection.Key, Operation: trigger.Target.Operation, Request: payload, RequestRef: trigger.IdempotencyKey}, principal)
	if err != nil {
		return schedulersdk.DownstreamReceipt{}, err
	}
	receiptID := strings.TrimSpace(result.ActionInvocation.ID)
	if receiptID == "" {
		receiptID = trigger.RunID
	}
	return schedulersdk.DownstreamReceipt{ID: receiptID, Owner: connection.ConnectorKey, Status: "accepted"}, nil
}

func (*schedulerSDKModuleHost) ResolveHTTPConnection(context.Context, string) (modulehost.HTTPConnection, error) {
	return modulehost.HTTPConnection{}, fmt.Errorf("Runtime Scheduler HTTP targets use the Integration callback dispatcher")
}

func schedulerSDKScope(purpose string) principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, purpose)
}
func schedulerSDKSystemPrincipal(capabilities ...string) principalmodel.Principal {
	return principalmodel.NewSystemPrincipal("scheduler-worker", schedulerSDKScope("execute extracted Scheduler protocol"), capabilities...)
}
func schedulerSDKWorkspacePrincipal(capabilities ...string) principalmodel.Principal {
	principal := schedulerSDKSystemPrincipal(capabilities...)
	principal.WorkspaceID = principalmodel.InstallationWorkspaceID
	return principal
}

func schedulerSDKString(data map[string]any, key string) string {
	value := strings.TrimSpace(fmt.Sprint(data[key]))
	if value == "<nil>" {
		return ""
	}
	return value
}
func schedulerSDKStringDefault(data map[string]any, key, fallback string) string {
	if value := schedulerSDKString(data, key); value != "" {
		return value
	}
	return fallback
}
func schedulerSDKInt(value any) int {
	var out int
	_, _ = fmt.Sscan(strings.TrimSpace(fmt.Sprint(value)), &out)
	return out
}
