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
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	"github.com/domainry/domainry-scheduler-sdk/modulehost"
)

// schedulerSDKModuleHost borrows Runtime infrastructure and downstream owner
// capabilities. Scheduler durable state and lease behavior live exclusively
// in the source-owned Scheduler module.
type schedulerSDKModuleHost struct {
	scheduler    *schedulerapplication.SchedulerApplicationService
	publications schedulerPublicationAcceptor
	dispatcher   *SchedulerCallbackDispatcher
	store        *persistence.RuntimeStore
	workerID     string
	mu           sync.RWMutex
	revision     int64
	definitions  map[string]schedulerapplication.PublishedDefinition
	authored     []map[string]any
	authoredSet  bool
}

func NewSchedulerSDKModuleHost(scheduler *schedulerapplication.SchedulerApplicationService, publications schedulerPublicationAcceptor, requirements []integrationsdk.ConnectionRequirement, store *persistence.RuntimeStore, workerID string, authoredDefinitions ...[]map[string]any) modulehost.ModuleHost {
	host := &schedulerSDKModuleHost{scheduler: scheduler, publications: publications, store: store, workerID: strings.TrimSpace(workerID), definitions: map[string]schedulerapplication.PublishedDefinition{}}
	if len(authoredDefinitions) > 0 {
		host.authoredSet = true
		host.authored = cloneSchedulerDefinitionMaps(authoredDefinitions[0])
	}
	host.dispatcher = NewSchedulerCallbackDispatcher(scheduler, publications, requirements)
	host.dispatcher.definition = host.definition
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
	var published []schedulerapplication.PublishedDefinition
	if h.authoredSet {
		published = make([]schedulerapplication.PublishedDefinition, 0, len(h.authored))
		for _, definition := range h.authored {
			key := schedulerSDKString(definition, "key")
			if key == "" {
				return schedulersdk.DefinitionSnapshot{}, fmt.Errorf("Scheduler manifest definition key is required")
			}
			published = append(published, schedulerapplication.PublishedDefinition{Key: key, Data: cloneSchedulerDefinitionMap(definition), UpdatedAt: schedulerSDKStringDefault(definition, "revision", "published")})
		}
	} else {
		if h.scheduler == nil {
			return schedulersdk.DefinitionSnapshot{}, fmt.Errorf("Runtime Scheduler definition source is unavailable")
		}
		var err error
		published, err = h.scheduler.PublishedDefinitions(ctx, schedulerSDKSystemPrincipal("scheduler.definition.read"))
		if err != nil {
			return schedulersdk.DefinitionSnapshot{}, err
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.revision++
	h.definitions = make(map[string]schedulerapplication.PublishedDefinition, len(published))
	definitions := make([]schedulersdk.Definition, 0, len(published))
	for _, definition := range published {
		h.definitions[definition.Key] = definition
		definitions = append(definitions, schedulerSDKDefinition(definition))
	}
	return schedulersdk.DefinitionSnapshot{Revision: h.revision, Definitions: definitions}, nil
}

func (h *schedulerSDKModuleHost) definition(_ context.Context, key string) (schedulerapplication.PublishedDefinition, bool, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	record, found := h.definitions[strings.TrimSpace(key)]
	return record, found, nil
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

func schedulerSDKDefinition(definition schedulerapplication.PublishedDefinition) schedulersdk.Definition {
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

// SchedulerCallbackDispatcher is the Runtime-owned execution boundary shared
// by in-process Module dispatch and authenticated Scheduler SaaS callbacks. It
// never owns or mutates Scheduler run lifecycle state.
type SchedulerCallbackDispatcher struct {
	scheduler    *schedulerapplication.SchedulerApplicationService
	publications schedulerPublicationAcceptor
	connectors   map[string]string
	definition   func(context.Context, string) (schedulerapplication.PublishedDefinition, bool, error)
}

type schedulerPublicationAcceptor interface {
	Accept(context.Context, integrationsdk.DeliveryRequest, string) (integrationsdk.DeliveryReceipt, error)
}

func NewSchedulerCallbackDispatcher(scheduler *schedulerapplication.SchedulerApplicationService, publications schedulerPublicationAcceptor, requirements []integrationsdk.ConnectionRequirement) *SchedulerCallbackDispatcher {
	connectors := make(map[string]string, len(requirements))
	for _, requirement := range requirements {
		connectors[strings.TrimSpace(requirement.Key)] = strings.TrimSpace(requirement.ConnectorKey)
	}
	return &SchedulerCallbackDispatcher{scheduler: scheduler, publications: publications, connectors: connectors}
}

func (d *SchedulerCallbackDispatcher) Dispatch(ctx context.Context, trigger schedulersdk.Trigger) (schedulersdk.DownstreamReceipt, error) {
	if trigger.Target.Type == "http" {
		return d.dispatchHTTPCallback(ctx, trigger)
	}
	definition, err := d.publishedDefinition(ctx, trigger.DefinitionKey)
	if err != nil {
		return schedulersdk.DownstreamReceipt{}, err
	}
	receiptID, err := d.scheduler.DispatchOwnedTrigger(ctx, definition, trigger.RunID, trigger.ScheduledFor, 25, schedulerSDKSystemPrincipal("scheduler.command"))
	if err != nil {
		return schedulersdk.DownstreamReceipt{}, err
	}
	return schedulersdk.DownstreamReceipt{ID: receiptID, Owner: trigger.Target.Owner, Status: "accepted"}, nil
}

func (d *SchedulerCallbackDispatcher) publishedDefinition(ctx context.Context, key string) (schedulerapplication.PublishedDefinition, error) {
	if d != nil && d.definition != nil {
		if record, found, err := d.definition(ctx, key); err != nil {
			return schedulerapplication.PublishedDefinition{}, err
		} else if found {
			return record, nil
		}
	}
	if d == nil || d.scheduler == nil {
		return schedulerapplication.PublishedDefinition{}, fmt.Errorf("Runtime Scheduler definition source is unavailable")
	}
	records, err := d.scheduler.PublishedDefinitions(ctx, schedulerSDKSystemPrincipal("scheduler.definition.read"))
	if err != nil {
		return schedulerapplication.PublishedDefinition{}, err
	}
	for _, record := range records {
		if record.Key == strings.TrimSpace(key) {
			return record, nil
		}
	}
	return schedulerapplication.PublishedDefinition{}, fmt.Errorf("Scheduler definition %q is not published", key)
}

func (d *SchedulerCallbackDispatcher) dispatchHTTPCallback(ctx context.Context, trigger schedulersdk.Trigger) (schedulersdk.DownstreamReceipt, error) {
	if d == nil || d.publications == nil {
		return schedulersdk.DownstreamReceipt{}, fmt.Errorf("Integration delivery boundary is unavailable")
	}
	connectorKey := strings.TrimSpace(d.connectors[strings.TrimSpace(trigger.Target.ConnectionKey)])
	if connectorKey == "" {
		return schedulersdk.DownstreamReceipt{}, fmt.Errorf("Scheduler HTTP connection %q is not declared by the application", trigger.Target.ConnectionKey)
	}
	payload := trigger.Target.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	if !json.Valid(payload) {
		return schedulersdk.DownstreamReceipt{}, fmt.Errorf("decode Scheduler HTTP payload: invalid JSON")
	}
	receipt, err := d.publications.Accept(ctx, integrationsdk.DeliveryRequest{MessageID: trigger.RunID, DeduplicationKey: trigger.IdempotencyKey, WorkspaceID: principalmodel.InstallationWorkspaceID, ConnectorKey: connectorKey, ConnectionKey: trigger.Target.ConnectionKey, Operation: trigger.Target.Operation, Payload: payload}, "scheduler")
	if err != nil {
		return schedulersdk.DownstreamReceipt{}, err
	}
	receiptID := strings.TrimSpace(receipt.InvocationID)
	if receiptID == "" {
		receiptID = trigger.RunID
	}
	return schedulersdk.DownstreamReceipt{ID: receiptID, Owner: connectorKey, Status: "accepted"}, nil
}

var _ modulehost.Dispatcher = (*SchedulerCallbackDispatcher)(nil)

func (h *schedulerSDKModuleHost) Dispatch(ctx context.Context, trigger schedulersdk.Trigger) (schedulersdk.DownstreamReceipt, error) {
	return h.dispatcher.Dispatch(ctx, trigger)
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
