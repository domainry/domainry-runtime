package appschema

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-orm/query"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
)

const (
	metadataUpgradeReceiptStarted   = "started"
	metadataUpgradeReceiptCompleted = "completed"
	metadataUpgradeReceiptFailed    = "failed"
)

// metadataUpgradeExecution carries the version pair and backup evidence that
// every receipt written by one physical upgrade run refers to.
type metadataUpgradeExecution struct {
	fromVersion     string
	toVersion       string
	backupID        string
	receiptStatuses map[string]string
}

func metadataUpgradeStepKey(operation, objectKey, columnKey, toVersion string) string {
	return operation + ":" + strings.TrimSpace(objectKey) + "." + strings.TrimSpace(columnKey) + "@" + strings.TrimSpace(toVersion)
}

func metadataUpgradeReceiptID(stepKey string) string {
	sum := sha256.Sum256([]byte(stepKey))
	return hex.EncodeToString(sum[:])
}

// LoadPreviousManifest returns the manifest projected by the previous Runtime
// start, or nil when the database has never materialized a projection.
func (r ApplicationSchemaStore) LoadPreviousManifest(ctx context.Context, scope principalmodel.SystemScope) (*manifestmodel.ManifestSchema, error) {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return nil, err
	}
	statement, args, err := query.NewSelectBuilder(r.store.SQLRenderer, "_application_schema_projection").
		Columns("template_id", "artifact_version").Where(query.Equal("id", "current")).Build()
	if err != nil {
		return nil, fmt.Errorf("build previous manifest lookup: %w", err)
	}
	var templateID, artifactVersion string
	err = r.database().QueryRowContext(ctx, statement, args...).Scan(&templateID, &artifactVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load previous manifest projection: %w", err)
	}
	if strings.TrimSpace(templateID) == "" || strings.TrimSpace(artifactVersion) == "" {
		// SnapshotRevision may create a header row before any manifest was
		// projected; that row carries no definitions.
		return nil, nil
	}
	previous, err := r.loadProjectedManifest(ctx)
	if err != nil {
		return nil, err
	}
	return &previous, nil
}

// ApplyUpgrade executes a non-blocking upgrade plan: it secures the
// pre-upgrade backup when physical steps are pending, then materializes the
// next manifest while writing one receipt per column addition and backfill.
// The returned plan carries any diagnostics raised during execution.
func (r ApplicationSchemaStore) ApplyUpgrade(ctx context.Context, scope principalmodel.SystemScope, plan appschemamodel.ApplicationSchemaUpgradePlan, next manifestmodel.ManifestSchema) (appschemamodel.ApplicationSchemaUpgradePlan, error) {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return plan, err
	}
	if plan.Blocking {
		return plan, fmt.Errorf("definition upgrade plan is blocking and cannot be applied")
	}
	execution := metadataUpgradeExecution{fromVersion: plan.FromVersion, toVersion: strings.TrimSpace(next.Version)}
	if execution.toVersion == "" {
		execution.toVersion = plan.ToVersion
	}
	if len(plan.PendingSteps()) > 0 {
		backupID, err := r.store.EnsureMigrationBackup(ctx)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return plan, contextErr
			}
			plan.Diagnostics = append(plan.Diagnostics, appschemamodel.ApplicationSchemaUpgradeDiagnostic{
				Code:   appschemamodel.ApplicationSchemaUpgradeBackupUnavailableCode,
				Params: map[string]string{"reason": err.Error()},
			})
		} else {
			execution.backupID = backupID
		}
	}
	if err := r.syncManifestForUpgrade(ctx, next, execution); err != nil {
		return plan, err
	}
	return plan, nil
}

func (r ApplicationSchemaStore) upgradeReceiptStatus(ctx context.Context, stepKey string) (string, error) {
	statement, args, err := query.NewSelectBuilder(r.store.SQLRenderer, runtimeschema.DefinitionUpgradeReceiptsTable).
		Columns("status").Where(query.Equal("id", metadataUpgradeReceiptID(stepKey))).Build()
	if err != nil {
		return "", fmt.Errorf("build upgrade receipt lookup: %w", err)
	}
	var status string
	err = r.schemaDatabase().QueryRowContext(ctx, statement, args...).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load upgrade receipt %s: %w", stepKey, err)
	}
	return strings.TrimSpace(status), nil
}

func (r ApplicationSchemaStore) upgradeReceiptCompleted(ctx context.Context, stepKey string) (bool, error) {
	status, err := r.upgradeReceiptStatus(ctx, stepKey)
	if err != nil {
		return false, err
	}
	return status == metadataUpgradeReceiptCompleted, nil
}

func (r ApplicationSchemaStore) loadUpgradeReceiptStatuses(ctx context.Context, toVersion string) (map[string]string, error) {
	builder := query.NewSelectBuilder(r.store.SQLRenderer, runtimeschema.DefinitionUpgradeReceiptsTable).Columns("step_key", "status")
	if toVersion = strings.TrimSpace(toVersion); toVersion != "" {
		builder = builder.Where(query.Equal("to_version", toVersion))
	}
	statement, args, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("build upgrade receipt snapshot: %w", err)
	}
	rows, err := r.schemaDatabase().QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("load upgrade receipt snapshot: %w", err)
	}
	defer rows.Close()
	statuses := map[string]string{}
	for rows.Next() {
		var stepKey, status string
		if err := rows.Scan(&stepKey, &status); err != nil {
			return nil, fmt.Errorf("scan upgrade receipt snapshot: %w", err)
		}
		statuses[strings.TrimSpace(stepKey)] = strings.TrimSpace(status)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate upgrade receipt snapshot: %w", err)
	}
	return statuses, nil
}

func (r ApplicationSchemaStore) executionUpgradeReceiptStatus(ctx context.Context, execution metadataUpgradeExecution, stepKey string) (string, error) {
	if execution.receiptStatuses != nil {
		return execution.receiptStatuses[stepKey], nil
	}
	return r.upgradeReceiptStatus(ctx, stepKey)
}

// writeUpgradeReceipt upserts the receipt for one step. A started receipt
// left behind by a crash is overwritten by the rerun that completes the step.
func (r ApplicationSchemaStore) writeUpgradeReceipt(ctx context.Context, execution metadataUpgradeExecution, objectKey, columnKey, stepKey, status, errorCode string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	completedAt := ""
	if status == metadataUpgradeReceiptCompleted || status == metadataUpgradeReceiptFailed {
		completedAt = now
	}
	existing, err := r.executionUpgradeReceiptStatus(ctx, execution, stepKey)
	if err != nil {
		return err
	}
	id := metadataUpgradeReceiptID(stepKey)
	var statement string
	var args []any
	if existing == "" {
		statement, args, err = query.NewInsertBuilder(r.store.SQLRenderer, runtimeschema.DefinitionUpgradeReceiptsTable).
			Columns("id", "from_version", "to_version", "object_key", "column_key", "step_key", "status", "error_code", "backup_id", "started_at", "completed_at").
			Values(id, execution.fromVersion, execution.toVersion, strings.TrimSpace(objectKey), strings.TrimSpace(columnKey), stepKey, status, errorCode, execution.backupID, now, completedAt).Build()
	} else {
		statement, args, err = query.NewUpdateBuilder(r.store.SQLRenderer, runtimeschema.DefinitionUpgradeReceiptsTable).
			Set("status", status).Set("error_code", errorCode).Set("backup_id", execution.backupID).Set("completed_at", completedAt).
			Where(query.Equal("id", id)).Build()
	}
	if err != nil {
		return fmt.Errorf("build upgrade receipt %s: %w", stepKey, err)
	}
	if _, err := r.schemaDatabase().ExecContext(ctx, statement, args...); err != nil {
		return fmt.Errorf("record upgrade receipt %s: %w", stepKey, err)
	}
	if execution.receiptStatuses != nil {
		execution.receiptStatuses[stepKey] = status
	}
	return nil
}

// runReceiptedUpgradeStep executes one step unless a completed receipt already
// exists, surrounding it with started/completed (or failed) receipts.
func (r ApplicationSchemaStore) runReceiptedUpgradeStep(ctx context.Context, execution metadataUpgradeExecution, objectKey, columnKey, stepKey string, step func() error) error {
	status, err := r.executionUpgradeReceiptStatus(ctx, execution, stepKey)
	if err != nil || status == metadataUpgradeReceiptCompleted {
		return err
	}
	if err := r.writeUpgradeReceipt(ctx, execution, objectKey, columnKey, stepKey, metadataUpgradeReceiptStarted, ""); err != nil {
		return err
	}
	if err := step(); err != nil {
		_ = r.writeUpgradeReceipt(context.WithoutCancel(ctx), execution, objectKey, columnKey, stepKey, metadataUpgradeReceiptFailed, "backend.metadata.upgrade_step_failed")
		return err
	}
	return r.writeUpgradeReceipt(ctx, execution, objectKey, columnKey, stepKey, metadataUpgradeReceiptCompleted, "")
}
