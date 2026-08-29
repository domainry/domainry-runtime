package composition

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	"github.com/domainry/domainry-scheduler-sdk/modulehost"
)

// schedulerSDKModuleHost borrows Runtime infrastructure and downstream owner
// capabilities. Scheduler durable state and lease behavior live exclusively
// in the source-owned Scheduler module.
type schedulerSDKModuleHost struct {
	scheduler    *schedulerapplication.SchedulerApplicationService
	integrations *integrationapplication.IntegrationApplicationService
	dispatcher   *SchedulerCallbackDispatcher
	store        *persistence.RuntimeStore
	workerID     string
	mu           sync.RWMutex
	revision     int64
	definitions  map[string]recordmodel.Record
}

func NewSchedulerSDKModuleHost(scheduler *schedulerapplication.SchedulerApplicationService, integrations *integrationapplication.IntegrationApplicationService, store *persistence.RuntimeStore, workerID string) modulehost.ModuleHost {
	return &schedulerSDKModuleHost{scheduler: scheduler, integrations: integrations, dispatcher: NewSchedulerCallbackDispatcher(scheduler, integrations), store: store, workerID: strings.TrimSpace(workerID), definitions: map[string]recordmodel.Record{}}
}
func (h *schedulerSDKModuleHost) Definitions() modulehost.DefinitionProvider         { return h }
func (h *schedulerSDKModuleHost) Dispatcher() modulehost.Dispatcher                  { return h }
func (h *schedulerSDKModuleHost) HTTPConnections() modulehost.HTTPConnectionProvider { return h }
func (h *schedulerSDKModuleHost) Database() *sql.DB                                  { return h.store.DB() }
func (h *schedulerSDKModuleHost) Driver() string                                     { return h.store.Driver() }
func (h *schedulerSDKModuleHost) Schema() string                                     { return h.store.DatabaseSchema() }
func (h *schedulerSDKModuleHost) WorkerID() string                                   { return h.workerID }
func (h *schedulerSDKModuleHost) Migrations() modulehost.MigrationRegistrar {
	return schedulerSDKMigrationRegistrar{store: h.store}
}

type schedulerSDKMigrationRegistrar struct{ store *persistence.RuntimeStore }

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
	if h.scheduler == nil {
		return schedulersdk.DefinitionSnapshot{}, fmt.Errorf("Runtime Scheduler definition source is unavailable")
	}
	records, err := h.scheduler.PublishedDefinitions(ctx, schedulerSDKSystemPrincipal("scheduler.definition.read"))
	if err != nil {
		return schedulersdk.DefinitionSnapshot{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.revision++
	h.definitions = make(map[string]recordmodel.Record, len(records))
	definitions := make([]schedulersdk.Definition, 0, len(records))
	for _, record := range records {
		h.definitions[record.ID] = record
		definitions = append(definitions, schedulerSDKDefinition(record))
	}
	return schedulersdk.DefinitionSnapshot{Revision: h.revision, Definitions: definitions}, nil
}

func schedulerSDKDefinition(record recordmodel.Record) schedulersdk.Definition {
	data := record.Data
	targetType := schedulerSDKString(data, "target_type")
	target := schedulersdk.TargetRef{Type: "runtime_operation", Owner: targetType, Operation: schedulerSDKString(data, "target_key"), Payload: json.RawMessage(schedulerSDKStringDefault(data, "payload_json", "{}"))}
	if targetType == "http" {
		target = schedulersdk.TargetRef{Type: "http", ConnectionKey: schedulerSDKString(data, "connection_key"), Operation: schedulerSDKStringDefault(data, "operation", schedulerSDKString(data, "target_key")), DispatchMode: "runtime_callback", Payload: json.RawMessage(schedulerSDKStringDefault(data, "payload_json", "{}"))}
	}
	revision := strings.TrimSpace(record.UpdatedAt)
	if revision == "" {
		revision = "published"
	}
	initialNextRunAt, _ := time.Parse(time.RFC3339, schedulerSDKString(data, "next_run_at"))
	return schedulersdk.Definition{Key: record.ID, Name: schedulerSDKString(data, "name"), Status: schedulerSDKString(data, "status"), Revision: revision, InitialNextRunAt: initialNextRunAt, Schedule: schedulersdk.Schedule{Type: schedulerSDKString(data, "schedule_type"), Expression: schedulerSDKString(data, "schedule_expression"), Timezone: schedulerSDKString(data, "timezone"), IntervalSeconds: schedulerSDKInt(data["interval_seconds"]), TimeOfDay: schedulerSDKString(data, "time_of_day"), DayOfWeek: schedulerSDKString(data, "day_of_week"), DayOfMonth: schedulerSDKInt(data["day_of_month"])}, Target: target, Policy: schedulersdk.Policy{Misfire: schedulerSDKString(data, "missed_window_policy"), MaxCatchupWindows: schedulerSDKInt(data["max_catchup_windows"]), Timeout: time.Duration(schedulerSDKInt(data["timeout_seconds"])) * time.Second, MaxAttempts: schedulerSDKInt(data["max_attempts"]), RetryInitial: time.Duration(schedulerSDKInt(data["retry_delay_seconds"])) * time.Second, RetryMax: time.Duration(schedulerSDKInt(data["retry_max_delay_seconds"])) * time.Second}}
}

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
