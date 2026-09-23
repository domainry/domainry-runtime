package operations

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/requestcontext"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationspolicy "github.com/domainry/domainry-runtime/runtime/domain/operations/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	runtimeauditmodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/datamigration"
)

var _ operationscontract.DatabaseRetirementExecutor = DatabaseRetirementSQLExecutor{}

const databaseRetirementCompletedAuditEvent = "database_retirement_completed"

type DatabaseRetirementSQLExecutor struct {
	store   *database.RuntimeStore
	db      *sql.DB
	inspect func(context.Context, *sql.DB, datamigration.Engine, string) (datamigration.Inventory, error)
	now     func() time.Time
	newID   func() string
}

func (e DatabaseRetirementSQLExecutor) inspectInventory(ctx context.Context, engine datamigration.Engine, schema string) (datamigration.Inventory, error) {
	if e.inspect != nil {
		return e.inspect(ctx, e.database(), engine, schema)
	}
	return datamigration.Inspect(ctx, e.database(), engine, schema)
}

func (e DatabaseRetirementSQLExecutor) database() *sql.DB {
	if e.db != nil {
		return e.db
	}
	if e.store == nil {
		return nil
	}
	return e.store.DB()
}

func NewDatabaseRetirementSQLExecutor(store *database.RuntimeStore, now func() time.Time, newID func() string) DatabaseRetirementSQLExecutor {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if newID == nil {
		newID = requestcontext.NewRequestID
	}
	return DatabaseRetirementSQLExecutor{store: store, now: now, newID: newID}
}

func (e DatabaseRetirementSQLExecutor) ApplyDatabaseRetirementTransition(ctx context.Context, current, next operationsmodel.DatabaseRetirement) (operationsmodel.DatabaseRetirement, error) {
	if e.database() == nil {
		return next, fmt.Errorf("database retirement executor unavailable")
	}
	if current.Object != next.Object {
		return next, fmt.Errorf("database retirement transition identity mismatch")
	}
	engine, err := datamigration.ParseEngine(current.Object.Engine)
	if err != nil {
		return next, err
	}
	if !sameDatabaseEngine(e.store.Driver(), engine) {
		return next, fmt.Errorf("database retirement engine does not match Runtime connection")
	}
	var statements []string
	switch next.State {
	case operationsmodel.DatabaseRetirementWritesDisabled:
		statements, err = databaseWriteProtectionStatements(engine, current.Object, current.ID)
	case operationsmodel.DatabaseRetirementQuarantined:
		if strings.TrimSpace(next.Evidence.QuarantineObjectName) == "" {
			next.Evidence.QuarantineObjectName = databaseQuarantineObjectName(current.Object.Name, current.ID)
		}
		statements, err = databaseQuarantineStatements(engine, current.Object, next.Evidence.QuarantineObjectName)
	case operationsmodel.DatabaseRetirementObservationComplete:
		if current.State == operationsmodel.DatabaseRetirementQuarantined {
			statements, err = databaseRestoreStatements(engine, current.Object, current.Evidence.QuarantineObjectName)
		} else {
			return next, nil
		}
	default:
		return next, nil
	}
	if err != nil {
		return next, err
	}
	tx, err := e.database().BeginTx(ctx, nil)
	if err != nil {
		return next, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := applyRetirementLockTimeout(ctx, tx, engine, 5*time.Second); err != nil {
		return next, err
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return next, err
		}
	}
	if err := tx.Commit(); err != nil {
		return next, err
	}
	return next, nil
}

func (e DatabaseRetirementSQLExecutor) PreviewDatabaseRetirement(ctx context.Context, retirement operationsmodel.DatabaseRetirement) (operationsmodel.DatabaseDropPlan, error) {
	if e.database() == nil {
		return operationsmodel.DatabaseDropPlan{}, fmt.Errorf("database retirement executor unavailable")
	}
	engine, err := datamigration.ParseEngine(retirement.Object.Engine)
	if err != nil {
		return operationsmodel.DatabaseDropPlan{}, err
	}
	if !sameDatabaseEngine(e.store.Driver(), engine) {
		return operationsmodel.DatabaseDropPlan{}, fmt.Errorf("database retirement engine does not match Runtime connection")
	}
	if retirement.Object.Kind == "database" || retirement.Object.Kind == "schema" {
		return operationsmodel.DatabaseDropPlan{}, fmt.Errorf("external database and schema retirement requires a separately scoped executor")
	}
	effectiveObject := effectiveDatabaseRetirementObject(retirement)
	inventory, err := e.inspectInventory(ctx, engine, effectiveObject.Schema)
	if err != nil {
		return operationsmodel.DatabaseDropPlan{}, err
	}
	dependencies, estimatedBytes, err := retirementDependencies(inventory, effectiveObject)
	if err != nil {
		return operationsmodel.DatabaseDropPlan{}, err
	}
	if len(dependencies) > 0 {
		return operationsmodel.DatabaseDropPlan{}, fmt.Errorf("database retirement has explicit dependencies: %v", databaseObjectNames(dependencies))
	}
	statement, err := databaseDropStatement(engine, effectiveObject)
	if err != nil {
		return operationsmodel.DatabaseDropPlan{}, err
	}
	lockTimeout := 5 * time.Second
	return operationsmodel.DatabaseDropPlan{
		RetirementID: retirement.ID, Object: retirement.Object, Dependencies: dependencies, Statements: []string{statement},
		EstimatedLock: time.Second, EstimatedReclaimBytes: estimatedBytes, LockTimeout: lockTimeout,
		Rollback: retirement.Evidence.Rollback, ApprovalID: retirement.Evidence.ApprovalID,
	}, nil
}

func effectiveDatabaseRetirementObject(retirement operationsmodel.DatabaseRetirement) operationsmodel.DatabaseObjectIdentity {
	object := retirement.Object
	if retirement.State == operationsmodel.DatabaseRetirementQuarantined && strings.TrimSpace(retirement.Evidence.QuarantineObjectName) != "" {
		object.Name = strings.TrimSpace(retirement.Evidence.QuarantineObjectName)
		if object.Kind != "table" && object.Kind != "view" {
			object.ParentName = strings.TrimSpace(retirement.Evidence.QuarantineObjectName)
		}
	}
	return object
}

func databaseQuarantineObjectName(name, retirementID string) string {
	suffix := strings.NewReplacer("-", "_", ":", "_", ".", "_").Replace(strings.TrimSpace(retirementID))
	if len(suffix) > 24 {
		suffix = suffix[len(suffix)-24:]
	}
	result := "retired_" + strings.TrimSpace(name) + "_" + suffix
	if len(result) > 63 {
		result = result[:63]
	}
	return result
}

func databaseWriteProtectionStatements(engine datamigration.Engine, object operationsmodel.DatabaseObjectIdentity, retirementID string) ([]string, error) {
	if object.Kind != "table" || !ormdialect.ValidIdentifier(object.Name) {
		return nil, fmt.Errorf("write protection currently requires a typed table retirement")
	}
	table := qualifyRetirementIdentifier(engine, object.Schema, object.Name)
	base := databaseQuarantineObjectName("guard", retirementID)
	switch engine {
	case datamigration.EngineSQLite:
		return []string{
			fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s BEFORE INSERT ON %s BEGIN SELECT RAISE(ABORT, 'retired object is read only'); END`, quoteRetirementIdentifier(engine, base+"_insert"), table),
			fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s BEFORE UPDATE ON %s BEGIN SELECT RAISE(ABORT, 'retired object is read only'); END`, quoteRetirementIdentifier(engine, base+"_update"), table),
			fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s BEFORE DELETE ON %s BEGIN SELECT RAISE(ABORT, 'retired object is read only'); END`, quoteRetirementIdentifier(engine, base+"_delete"), table),
		}, nil
	case datamigration.EnginePostgres:
		functionName := quoteRetirementIdentifier(engine, base+"_reject")
		triggerName := quoteRetirementIdentifier(engine, base+"_write")
		return []string{
			fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'retired object is read only'; END $$`, functionName),
			fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, triggerName, table),
			fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT OR UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, table, functionName),
		}, nil
	case datamigration.EngineMySQL:
		return []string{
			fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON %s FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'retired object is read only'`, quoteRetirementIdentifier(engine, base+"_insert"), table),
			fmt.Sprintf(`CREATE TRIGGER %s BEFORE UPDATE ON %s FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'retired object is read only'`, quoteRetirementIdentifier(engine, base+"_update"), table),
			fmt.Sprintf(`CREATE TRIGGER %s BEFORE DELETE ON %s FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'retired object is read only'`, quoteRetirementIdentifier(engine, base+"_delete"), table),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported database retirement engine %q", engine)
	}
}

func databaseQuarantineStatements(engine datamigration.Engine, object operationsmodel.DatabaseObjectIdentity, quarantineName string) ([]string, error) {
	if object.Kind != "table" || !ormdialect.ValidIdentifier(object.Name) || !ormdialect.ValidIdentifier(quarantineName) {
		return nil, fmt.Errorf("quarantine currently requires a typed table retirement")
	}
	oldName, newName := qualifyRetirementIdentifier(engine, object.Schema, object.Name), quoteRetirementIdentifier(engine, quarantineName)
	if engine == datamigration.EngineMySQL {
		return []string{"RENAME TABLE " + oldName + " TO " + newName}, nil
	}
	return []string{"ALTER TABLE " + oldName + " RENAME TO " + newName}, nil
}

func databaseRestoreStatements(engine datamigration.Engine, object operationsmodel.DatabaseObjectIdentity, quarantineName string) ([]string, error) {
	if object.Kind != "table" || !ormdialect.ValidIdentifier(object.Name) || !ormdialect.ValidIdentifier(quarantineName) {
		return nil, fmt.Errorf("restore currently requires a typed quarantined table")
	}
	quarantined := qualifyRetirementIdentifier(engine, object.Schema, quarantineName)
	original := quoteRetirementIdentifier(engine, object.Name)
	if engine == datamigration.EngineMySQL {
		return []string{"RENAME TABLE " + quarantined + " TO " + original}, nil
	}
	return []string{"ALTER TABLE " + quarantined + " RENAME TO " + original}, nil
}

func quoteRetirementIdentifier(engine datamigration.Engine, value string) string {
	if engine == datamigration.EngineMySQL {
		return "`" + strings.ReplaceAll(value, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func qualifyRetirementIdentifier(engine datamigration.Engine, schema, name string) string {
	if strings.TrimSpace(schema) == "" {
		return quoteRetirementIdentifier(engine, name)
	}
	return quoteRetirementIdentifier(engine, schema) + "." + quoteRetirementIdentifier(engine, name)
}

func (e DatabaseRetirementSQLExecutor) ExecuteDatabaseRetirement(ctx context.Context, retirement operationsmodel.DatabaseRetirement, plan operationsmodel.DatabaseDropPlan) (result operationscontract.DatabaseRetirementExecutionResult, err error) {
	newID := e.newID
	if newID == nil {
		newID = requestcontext.NewRequestID
	}
	result.AuditEventID = "database-retirement:" + retirement.ID + ":" + strings.TrimSpace(newID())
	if e.database() == nil {
		return result, fmt.Errorf("database retirement executor unavailable")
	}
	if err := operationspolicy.OperationsValidateDatabaseDropPlan(plan); err != nil {
		return result, err
	}
	now := e.now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	completedAt := now().UTC()
	if err := operationspolicy.OperationsValidateDatabaseDropEvidence(retirement.Evidence, completedAt); err != nil {
		return result, err
	}
	if plan.RetirementID != retirement.ID || plan.Object != retirement.Object {
		return result, fmt.Errorf("database retirement plan identity mismatch")
	}
	engine, err := datamigration.ParseEngine(retirement.Object.Engine)
	if err != nil {
		return result, err
	}
	before, err := e.inspectInventory(ctx, engine, retirement.Object.Schema)
	if err != nil {
		return result, err
	}
	tx, err := e.database().BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
			result.Dirty = engine == datamigration.EngineMySQL || result.ExecutedStatements > 0
			result.BlockedReason = err.Error()
		}
	}()
	if err = applyRetirementLockTimeout(ctx, tx, engine, plan.LockTimeout); err != nil {
		return result, err
	}
	for _, statement := range plan.Statements {
		if _, err = tx.ExecContext(ctx, statement); err != nil {
			return result, err
		}
		result.ExecutedStatements++
	}
	if engine == datamigration.EngineSQLite && retirement.Object.Kind == "column" {
		if err = verifySQLiteColumnDropPreservesSchema(ctx, tx, before, retirement.Object); err != nil {
			return result, err
		}
	}
	if err = appendDatabaseRetirementCompletionAudit(ctx, e.store, tx, retirement, plan, result, completedAt); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func appendDatabaseRetirementCompletionAudit(ctx context.Context, store *database.RuntimeStore, tx *sql.Tx, retirement operationsmodel.DatabaseRetirement, plan operationsmodel.DatabaseDropPlan, result operationscontract.DatabaseRetirementExecutionResult, completedAt time.Time) error {
	if store == nil || tx == nil {
		return fmt.Errorf("database retirement audit transaction unavailable")
	}
	workspaceID, err := databaseRetirementAuditWorkspaceID()
	if err != nil {
		return err
	}
	event := auditmodel.AuditEvent{
		ID: result.AuditEventID, WorkspaceID: workspaceID,
		OperationID: requestcontext.OwnerExecutionID(ctx), OwnerRunID: retirement.ID,
		Family: auditmodel.EventFamilyRuntimeOperations,
		Event:  databaseRetirementCompletedAuditEvent, ObjectKey: "database_object", RecordID: retirement.ID,
		ActorID: strings.TrimSpace(retirement.Evidence.Owner), RoleKey: "operations",
		Summary: "Database object retirement completed",
		Before:  map[string]any{"state": retirement.State, "object": retirement.Object},
		After:   map[string]any{"state": operationsmodel.DatabaseRetirementDropped, "executed_statements": result.ExecutedStatements},
		Metadata: map[string]any{
			"approval_id": retirement.Evidence.ApprovalID, "backup_id": retirement.Evidence.BackupID,
			"backup_checksum": retirement.Evidence.BackupChecksum, "change_plan_id": retirement.Evidence.ChangePlanID,
			"disposition": retirement.Evidence.Disposition, "rollback": plan.Rollback,
		},
		CreatedAt: completedAt.Format(time.RFC3339Nano),
	}
	if err := store.AppendPreparedAuditWithin(ctx, runtimeauditmodule.NewTransaction(tx), event); err != nil {
		return fmt.Errorf("append database retirement completion audit: %w", err)
	}
	return nil
}

func (e DatabaseRetirementSQLExecutor) VerifyDatabaseRetirementCompletionAudit(ctx context.Context, retirement operationsmodel.DatabaseRetirement, auditEventID string) error {
	if e.store == nil {
		return fmt.Errorf("database retirement Audit verifier unavailable")
	}
	auditEventID = strings.TrimSpace(auditEventID)
	if auditEventID == "" {
		return fmt.Errorf("database retirement completion Audit event ID is required")
	}
	workspaceID, err := databaseRetirementAuditWorkspaceID()
	if err != nil {
		return err
	}
	binding := e.store.Audit()
	if binding == nil {
		return fmt.Errorf("Audit Binding is unavailable for database retirement evidence")
	}
	events, err := binding.Reader().List(ctx, workspaceID, auditmodel.Query{
		Event:      databaseRetirementCompletedAuditEvent,
		ObjectKey:  "database_object",
		RecordID:   retirement.ID,
		OwnerRunID: retirement.ID,
		Limit:      100,
	})
	if err != nil {
		return fmt.Errorf("read database retirement completion Audit evidence: %w", err)
	}
	for _, event := range events {
		if strings.TrimSpace(event.ID) != auditEventID {
			continue
		}
		if event.WorkspaceID != workspaceID || event.Family != auditmodel.EventFamilyRuntimeOperations || event.Event != databaseRetirementCompletedAuditEvent || event.ObjectKey != "database_object" || event.RecordID != retirement.ID || event.OwnerRunID != retirement.ID || event.ActorID != strings.TrimSpace(retirement.Evidence.Owner) {
			return fmt.Errorf("database retirement completion Audit evidence identity mismatch")
		}
		if databaseRetirementAuditValue(event.Before, "state") != string(operationsmodel.DatabaseRetirementQuarantined) || databaseRetirementAuditValue(event.After, "state") != string(operationsmodel.DatabaseRetirementDropped) {
			return fmt.Errorf("database retirement completion Audit evidence state mismatch")
		}
		expectedMetadata := map[string]string{
			"approval_id": retirement.Evidence.ApprovalID, "backup_id": retirement.Evidence.BackupID,
			"backup_checksum": retirement.Evidence.BackupChecksum, "change_plan_id": retirement.Evidence.ChangePlanID,
			"disposition": retirement.Evidence.Disposition, "rollback": retirement.Evidence.Rollback,
		}
		for key, expected := range expectedMetadata {
			if databaseRetirementAuditValue(event.Metadata, key) != expected {
				return fmt.Errorf("database retirement completion Audit evidence %s mismatch", key)
			}
		}
		if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(event.CreatedAt)); err != nil {
			return fmt.Errorf("database retirement completion Audit evidence timestamp is invalid: %w", err)
		}
		return nil
	}
	return fmt.Errorf("database retirement completion Audit event %q was not found", auditEventID)
}

func databaseRetirementAuditValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(values[key]))
}

func databaseRetirementAuditWorkspaceID() (string, error) {
	workspaceID, err := principalmodel.NewWorkspaceID(principalmodel.InstallationWorkspaceID)
	if err != nil {
		return "", fmt.Errorf("database retirement installation Workspace is unavailable: %w", err)
	}
	return workspaceID.String(), nil
}

func applyRetirementLockTimeout(ctx context.Context, tx *sql.Tx, engine datamigration.Engine, timeout time.Duration) error {
	milliseconds := timeout.Milliseconds()
	switch engine {
	case datamigration.EngineSQLite:
		_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", milliseconds))
		return err
	case datamigration.EnginePostgres:
		_, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL lock_timeout = '%dms'", milliseconds))
		return err
	case datamigration.EngineMySQL:
		seconds := int64(timeout / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		_, err := tx.ExecContext(ctx, fmt.Sprintf("SET SESSION lock_wait_timeout = %d", seconds))
		return err
	default:
		return fmt.Errorf("unsupported database retirement engine %q", engine)
	}
}

func retirementDependencies(inventory datamigration.Inventory, object operationsmodel.DatabaseObjectIdentity) ([]operationsmodel.DatabaseObjectIdentity, int64, error) {
	dependencies := []operationsmodel.DatabaseObjectIdentity{}
	table, found := retirementTable(inventory.Tables, object)
	switch object.Kind {
	case "table":
		if !found {
			return nil, 0, fmt.Errorf("retirement table %s does not exist", object.Name)
		}
		for _, candidate := range inventory.Tables {
			for _, foreign := range candidate.ForeignKeys {
				if foreign.ReferencedTable == object.Name && candidate.Name != object.Name {
					dependencies = append(dependencies, operationsmodel.DatabaseObjectIdentity{Engine: string(inventory.Engine), Database: object.Database, Schema: object.Schema, Kind: "foreign_key", Name: candidate.Name + "->" + object.Name, ParentName: candidate.Name})
				}
			}
		}
		for _, view := range inventory.Views {
			if strings.Contains(strings.ToLower(view.Definition), strings.ToLower(object.Name)) {
				dependencies = append(dependencies, operationsmodel.DatabaseObjectIdentity{Engine: string(inventory.Engine), Database: object.Database, Schema: object.Schema, Kind: "view", Name: view.Name})
			}
		}
		return dependencies, table.EstimatedBytes, nil
	case "column":
		if !found || !retirementColumnExists(table.Columns, object.Name) {
			return nil, 0, fmt.Errorf("retirement column %s.%s does not exist", object.ParentName, object.Name)
		}
		for _, index := range table.Indexes {
			if stringSliceContains(index.Columns, object.Name) {
				dependencies = append(dependencies, operationsmodel.DatabaseObjectIdentity{Engine: string(inventory.Engine), Database: object.Database, Schema: object.Schema, Kind: "index", Name: index.Name, ParentName: table.Name})
			}
		}
		for _, foreign := range table.ForeignKeys {
			if stringSliceContains(foreign.Columns, object.Name) {
				dependencies = append(dependencies, operationsmodel.DatabaseObjectIdentity{Engine: string(inventory.Engine), Database: object.Database, Schema: object.Schema, Kind: "foreign_key", Name: strings.Join(foreign.Columns, ","), ParentName: table.Name})
			}
		}
		return dependencies, 0, nil
	case "index":
		if !found || !retirementIndexExists(table.Indexes, object.Name) {
			return nil, 0, fmt.Errorf("retirement index %s does not exist", object.Name)
		}
		return nil, 0, nil
	case "view":
		for _, view := range inventory.Views {
			if view.Name == object.Name {
				return nil, 0, nil
			}
		}
		return nil, 0, fmt.Errorf("retirement view %s does not exist", object.Name)
	case "trigger":
		for _, trigger := range inventory.Triggers {
			if trigger.Name == object.Name {
				return nil, 0, nil
			}
		}
		return nil, 0, fmt.Errorf("retirement trigger %s does not exist", object.Name)
	default:
		return nil, 0, fmt.Errorf("unsupported database retirement object kind %q", object.Kind)
	}
}

func databaseDropStatement(engine datamigration.Engine, object operationsmodel.DatabaseObjectIdentity) (string, error) {
	if !ormdialect.ValidIdentifier(object.Name) || (object.ParentName != "" && !ormdialect.ValidIdentifier(object.ParentName)) || (object.Schema != "" && !ormdialect.ValidIdentifier(object.Schema)) {
		return "", fmt.Errorf("unsafe database retirement identifier")
	}
	quote := func(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }
	if engine == datamigration.EngineMySQL {
		quote = func(value string) string { return "`" + strings.ReplaceAll(value, "`", "``") + "`" }
	}
	qualified := func(name string) string {
		if object.Schema == "" {
			return quote(name)
		}
		return quote(object.Schema) + "." + quote(name)
	}
	switch object.Kind {
	case "table":
		return "DROP TABLE " + qualified(object.Name), nil
	case "view":
		return "DROP VIEW " + qualified(object.Name), nil
	case "column":
		statement := "ALTER TABLE " + qualified(object.ParentName) + " DROP COLUMN " + quote(object.Name)
		if engine == datamigration.EngineMySQL {
			statement += ", ALGORITHM=INPLACE, LOCK=NONE"
		}
		return statement, nil
	case "index":
		if engine == datamigration.EngineMySQL {
			return "ALTER TABLE " + qualified(object.ParentName) + " DROP INDEX " + quote(object.Name) + ", ALGORITHM=INPLACE, LOCK=NONE", nil
		}
		return "DROP INDEX " + qualified(object.Name), nil
	case "trigger":
		if engine == datamigration.EnginePostgres {
			return "DROP TRIGGER " + quote(object.Name) + " ON " + qualified(object.ParentName), nil
		}
		return "DROP TRIGGER " + qualified(object.Name), nil
	default:
		return "", fmt.Errorf("unsupported database retirement object kind %q", object.Kind)
	}
}
