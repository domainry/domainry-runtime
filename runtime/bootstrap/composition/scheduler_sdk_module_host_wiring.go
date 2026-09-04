package composition

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	reportsdk "github.com/domainry/domainry-report-sdk"
	dispatchapplication "github.com/domainry/domainry-runtime/runtime/application/dispatch"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	"github.com/domainry/domainry-scheduler-sdk/modulehost"
)

// schedulerSDKModuleHost borrows Runtime infrastructure and downstream owner
// capabilities. Scheduler durable state and lease behavior live exclusively
// in the source-owned Scheduler module.
type schedulerSDKModuleHost struct {
	definitionsSource SchedulerDefinitionSource
	publications      schedulerPublicationAcceptor
	dispatcher        *TargetExecutionDispatcher
	store             *persistence.RuntimeStore
	workerID          string
	mu                sync.RWMutex
	revision          int64
	authored          []map[string]any
	authoredSet       bool
}

func NewSchedulerSDKModuleHost(definitions SchedulerDefinitionSource, executions *dispatchapplication.TargetExecutionApplicationService, publications schedulerPublicationAcceptor, requirements []integrationsdk.ConnectionRequirement, store *persistence.RuntimeStore, workerID string, authoredDefinitions ...[]map[string]any) modulehost.ModuleHost {
	host := &schedulerSDKModuleHost{definitionsSource: definitions, publications: publications, store: store, workerID: strings.TrimSpace(workerID)}
	if len(authoredDefinitions) > 0 {
		host.authoredSet = true
		host.authored = cloneSchedulerDefinitionMaps(authoredDefinitions[0])
	}
	host.dispatcher = NewTargetExecutionDispatcher(executions, publications, requirements)
	return host
}
func (h *schedulerSDKModuleHost) Definitions() modulehost.DefinitionProvider         { return h }
func (h *schedulerSDKModuleHost) Dispatcher() modulehost.Dispatcher                  { return h }
func (h *schedulerSDKModuleHost) HTTPConnections() modulehost.HTTPConnectionProvider { return h }
func (h *schedulerSDKModuleHost) Database() modulehost.Database                      { return h.store.DB() }
func (h *schedulerSDKModuleHost) Dialect() modulehost.Dialect                        { return h.store.SQLRenderer }
func (h *schedulerSDKModuleHost) WorkerID() string                                   { return h.workerID }
func (h *schedulerSDKModuleHost) Migrations() modulehost.MigrationRegistrar {
	return schedulerSDKMigrationRegistrar{store: h.store}
}

type schedulerSDKMigrationRegistrar struct{ store *persistence.RuntimeStore }

func (r schedulerSDKMigrationRegistrar) Driver() string { return r.store.Driver() }
func (r schedulerSDKMigrationRegistrar) Schema() string { return r.store.DatabaseSchema() }

func (r schedulerSDKMigrationRegistrar) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []modulehost.SchemaMigration) error {
	values := make([]notificationmodulehost.SchemaMigration, len(migrations))
	for i, migration := range migrations {
		values[i] = notificationmodulehost.SchemaMigration{Version: migration.Version, Name: migration.Name, Statements: append([]string(nil), migration.Statements...)}
		if migration.Baseline == nil {
			continue
		}
		baseline := notificationmodulehost.SchemaBaseline{Tables: make([]notificationmodulehost.SchemaTable, len(migration.Baseline.Tables))}
		for ti, table := range migration.Baseline.Tables {
			baseline.Tables[ti] = notificationmodulehost.SchemaTable{Name: table.Name, Columns: make([]notificationmodulehost.SchemaColumn, len(table.Columns)), Indexes: make([]notificationmodulehost.SchemaIndex, len(table.Indexes))}
			for ci, column := range table.Columns {
				baseline.Tables[ti].Columns[ci] = notificationmodulehost.SchemaColumn{Name: column.Name, Type: column.Type, Nullable: column.Nullable, PrimaryKey: column.PrimaryKey}
			}
			for ii, index := range table.Indexes {
				baseline.Tables[ti].Indexes[ii] = notificationmodulehost.SchemaIndex{Name: index.Name, Unique: index.Unique, Columns: append([]string(nil), index.Columns...)}
			}
		}
		values[i].Baseline = &baseline
	}
	return r.store.ApplyOwnedMigrations(ctx, owner, values)
}

func (h *schedulerSDKModuleHost) Snapshot(ctx context.Context) (schedulersdk.DefinitionSnapshot, error) {
	var published []SchedulerPublishedDefinition
	if h.authoredSet {
		published = make([]SchedulerPublishedDefinition, 0, len(h.authored))
		for _, definition := range h.authored {
			key := schedulerSDKString(definition, "key")
			if key == "" {
				return schedulersdk.DefinitionSnapshot{}, fmt.Errorf("Scheduler manifest definition key is required")
			}
			published = append(published, SchedulerPublishedDefinition{Key: key, Data: cloneSchedulerDefinitionMap(definition), UpdatedAt: schedulerSDKStringDefault(definition, "revision", "published")})
		}
	} else {
		if h.definitionsSource == nil {
			return schedulersdk.DefinitionSnapshot{}, fmt.Errorf("Runtime Scheduler definition source is unavailable")
		}
		var err error
		published, err = h.definitionsSource.ListSchedulerDefinitions(ctx)
		if err != nil {
			return schedulersdk.DefinitionSnapshot{}, err
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.revision++
	definitions := make([]schedulersdk.Definition, 0, len(published))
	for _, definition := range published {
		definitions = append(definitions, schedulerSDKDefinition(definition))
	}
	return schedulersdk.DefinitionSnapshot{Revision: h.revision, Definitions: definitions}, nil
}

func cloneSchedulerDefinitionMaps(values []map[string]any) []map[string]any {
	result := make([]map[string]any, len(values))
	for index, value := range values {
		result[index] = cloneSchedulerDefinitionMap(value)
	}
	return result
}

func cloneSchedulerDefinitionMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func schedulerSDKDefinition(definition SchedulerPublishedDefinition) schedulersdk.Definition {
	data := definition.Data
	targetType := schedulerSDKString(data, "target_type")
	target := schedulersdk.TargetRef{Type: "runtime_operation", Owner: targetType, Operation: schedulerSDKString(data, "target_key"), Payload: json.RawMessage(schedulerSDKStringDefault(data, "payload_json", "{}"))}
	if targetType == "http" {
		target = schedulersdk.TargetRef{Type: "http", ConnectionKey: schedulerSDKString(data, "connection_key"), Operation: schedulerSDKStringDefault(data, "operation", schedulerSDKString(data, "target_key")), DispatchMode: "runtime_callback", Payload: json.RawMessage(schedulerSDKStringDefault(data, "payload_json", "{}"))}
	}
	revision := strings.TrimSpace(definition.UpdatedAt)
	if revision == "" {
		revision = "published"
	}
	initialNextRunAt, _ := time.Parse(time.RFC3339, schedulerSDKString(data, "next_run_at"))
	return schedulersdk.Definition{Key: definition.Key, Name: schedulerSDKString(data, "name"), Status: schedulerSDKString(data, "status"), Revision: revision, InitialNextRunAt: initialNextRunAt, Schedule: schedulersdk.Schedule{Type: schedulerSDKString(data, "schedule_type"), Expression: schedulerSDKString(data, "schedule_expression"), Timezone: schedulerSDKString(data, "timezone"), IntervalSeconds: schedulerSDKInt(data["interval_seconds"]), TimeOfDay: schedulerSDKString(data, "time_of_day"), DayOfWeek: schedulerSDKString(data, "day_of_week"), DayOfMonth: schedulerSDKInt(data["day_of_month"])}, Target: target, Policy: schedulersdk.Policy{Misfire: schedulerSDKString(data, "missed_window_policy"), MaxCatchupWindows: schedulerSDKInt(data["max_catchup_windows"]), Timeout: time.Duration(schedulerSDKInt(data["timeout_seconds"])) * time.Second, MaxAttempts: schedulerSDKInt(data["max_attempts"]), RetryInitial: time.Duration(schedulerSDKInt(data["retry_delay_seconds"])) * time.Second, RetryMax: time.Duration(schedulerSDKInt(data["retry_max_delay_seconds"])) * time.Second}}
}

// TargetExecutionDispatcher is Runtime's schedule-agnostic target router. The
// Scheduler SDK adapter below maps its source-owned Trigger into this generic
// port; the executor never reads definitions or mutates Scheduler run state.
type TargetExecutionDispatcher struct {
	executions   *dispatchapplication.TargetExecutionApplicationService
	publications schedulerPublicationAcceptor
	connectors   map[string]string
}

type schedulerPublicationAcceptor interface {
	Accept(context.Context, integrationsdk.DeliveryRequest, string) (integrationsdk.DeliveryReceipt, error)
}

func NewTargetExecutionDispatcher(executions *dispatchapplication.TargetExecutionApplicationService, publications schedulerPublicationAcceptor, requirements []integrationsdk.ConnectionRequirement) *TargetExecutionDispatcher {
	connectors := make(map[string]string, len(requirements))
	for _, requirement := range requirements {
		connectors[strings.TrimSpace(requirement.Key)] = strings.TrimSpace(requirement.ConnectorKey)
	}
	return &TargetExecutionDispatcher{executions: executions, publications: publications, connectors: connectors}
}

func (d *TargetExecutionDispatcher) Dispatch(ctx context.Context, trigger schedulersdk.Trigger) (schedulersdk.DownstreamReceipt, error) {
	receipt, err := d.Execute(ctx, dispatchapplication.ExecutionRequest{
		ExecutionID: trigger.RunID, IdempotencyKey: trigger.IdempotencyKey, DueAt: trigger.ScheduledFor,
		Target:    dispatchapplication.Target{Type: trigger.Target.Type, Owner: trigger.Target.Owner, Operation: trigger.Target.Operation, ConnectionKey: trigger.Target.ConnectionKey, Payload: append([]byte(nil), trigger.Target.Payload...)},
		Principal: targetExecutionSystemPrincipal(),
	})
	return schedulersdk.DownstreamReceipt{ID: receipt.ID, Owner: receipt.Owner, Status: receipt.Status}, err
}

func (d *TargetExecutionDispatcher) Execute(ctx context.Context, request dispatchapplication.ExecutionRequest) (dispatchapplication.ExecutionReceipt, error) {
	if !request.Principal.SystemScope.Valid() && strings.TrimSpace(request.Principal.WorkspaceID) == "" {
		request.Principal = targetExecutionSystemPrincipal()
	}
	if d != nil && request.Principal.SystemScope.Valid() && strings.TrimSpace(request.Target.Owner) == "report_snapshot_refresh" {
		request.Principal = request.Principal.WithExactSystemCapabilities(reportsdk.ActionReportSnapshotsRefresh)
	}
	if strings.TrimSpace(request.Target.Type) != "http" {
		if d == nil || d.executions == nil {
			return dispatchapplication.ExecutionReceipt{}, fmt.Errorf("Runtime target executor is unavailable")
		}
		return d.executions.Execute(ctx, request)
	}
	if d == nil || d.publications == nil {
		return dispatchapplication.ExecutionReceipt{}, fmt.Errorf("Integration delivery boundary is unavailable")
	}
	connectorKey := strings.TrimSpace(d.connectors[strings.TrimSpace(request.Target.ConnectionKey)])
	if connectorKey == "" {
		return dispatchapplication.ExecutionReceipt{}, fmt.Errorf("HTTP connection %q is not declared by the application", request.Target.ConnectionKey)
	}
	payload := json.RawMessage(request.Target.Payload)
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	if !json.Valid(payload) {
		return dispatchapplication.ExecutionReceipt{}, fmt.Errorf("decode HTTP target payload: invalid JSON")
	}
	receipt, err := d.publications.Accept(ctx, integrationsdk.DeliveryRequest{MessageID: request.ExecutionID, DeduplicationKey: request.IdempotencyKey, WorkspaceID: principalmodel.InstallationWorkspaceID, ConnectorKey: connectorKey, ConnectionKey: request.Target.ConnectionKey, Operation: request.Target.Operation, Payload: payload}, "dispatch")
	if err != nil {
		return dispatchapplication.ExecutionReceipt{}, err
	}
	receiptID := strings.TrimSpace(receipt.InvocationID)
	if receiptID == "" {
		receiptID = request.ExecutionID
	}
	return dispatchapplication.ExecutionReceipt{ID: receiptID, Owner: connectorKey, Status: "accepted"}, nil
}

var _ modulehost.Dispatcher = (*TargetExecutionDispatcher)(nil)

func (h *schedulerSDKModuleHost) Dispatch(ctx context.Context, trigger schedulersdk.Trigger) (schedulersdk.DownstreamReceipt, error) {
	return h.dispatcher.Dispatch(ctx, trigger)
}
func (*schedulerSDKModuleHost) ResolveHTTPConnection(context.Context, string) (modulehost.HTTPConnection, error) {
	return modulehost.HTTPConnection{}, fmt.Errorf("Runtime Scheduler HTTP targets use the Integration callback dispatcher")
}

func targetExecutionScope(purpose string) principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, purpose)
}
func targetExecutionSystemPrincipal(capabilities ...string) principalmodel.Principal {
	return principalmodel.NewSystemPrincipal("runtime-target-executor", targetExecutionScope("execute authenticated target request"), capabilities...)
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
