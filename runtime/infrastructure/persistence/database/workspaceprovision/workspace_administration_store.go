package workspaceprovision

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	"github.com/domainry/domainry-orm/query"
	workspaceprovisionapplication "github.com/domainry/domainry-runtime/runtime/application/workspaceprovision"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	workspaceprovisionrepository "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/repository"
	workspaceprovisionvalidation "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/validation"
	runtimeauditmodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

const (
	workspaceAdministrationPurpose = "workspace_administration"
	workspaceAdministrationOwner   = "workspace"
	workspaceAdministrationKind    = "workspace.administration"
)

type WorkspaceAdministrationStore struct{ runtime *database.RuntimeStore }

func NewWorkspaceAdministrationStore(store *database.RuntimeStore) *WorkspaceAdministrationStore {
	return &WorkspaceAdministrationStore{runtime: store}
}

func (store *WorkspaceAdministrationStore) ListWorkspaceCatalog(ctx context.Context, after string, limit int) ([]workspaceprovisionmodel.CatalogEntry, bool, error) {
	if store == nil || store.runtime == nil || store.runtime.DB() == nil || limit < 1 || limit > 100 {
		return nil, false, workspaceprovisionmodel.ErrAdministrationUnavailable
	}
	predicate := query.NotEqualExpressions(query.QualifiedColumn("workspace", "canonical_code"), query.Value(""))
	if after = strings.TrimSpace(after); after != "" {
		predicate = query.GreaterThanExpressions(query.QualifiedColumn("workspace", "canonical_code"), query.Value(after))
	}
	statement, arguments, err := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), "_workspaces").Alias("workspace").
		Projections(workspaceCatalogProjections()...).
		Where(predicate).OrderBy(query.AscendingExpression(query.QualifiedColumn("workspace", "canonical_code"))).Limit(limit + 1).Build()
	if err != nil {
		return nil, false, fmt.Errorf("build Workspace administration catalog: %w", err)
	}
	rows, err := store.runtime.DB().QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("query Workspace administration catalog: %w", err)
	}
	defer rows.Close()
	items := make([]workspaceprovisionmodel.CatalogEntry, 0, limit+1)
	for rows.Next() {
		entry, scanErr := scanWorkspaceCatalogEntry(rows)
		if scanErr != nil {
			return nil, false, scanErr
		}
		if validateWorkspaceCatalogEntry(entry) != nil {
			return nil, false, workspaceprovisionmodel.ErrAdministrationUnavailable
		}
		items = append(items, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	return items, more, nil
}

func (store *WorkspaceAdministrationStore) SetWorkspaceStatus(ctx context.Context, actor workspaceprovisionmodel.AdministrationActor, canonicalCode string, expectedRevision int, status, idempotencyKey string) (workspaceprovisionmodel.LifecycleResult, error) {
	if store == nil || store.runtime == nil || store.runtime.DB() == nil || (status != workspaceprovisionmodel.WorkspaceStatusActive && status != workspaceprovisionmodel.WorkspaceStatusSuspended) {
		return workspaceprovisionmodel.LifecycleResult{}, workspaceprovisionmodel.ErrAdministrationUnavailable
	}
	actionKey := workspaceprovisionapplication.SuspendWorkspaceActionKey
	if status == workspaceprovisionmodel.WorkspaceStatusActive {
		actionKey = workspaceprovisionapplication.ReactivateWorkspaceActionKey
	}
	fingerprint := workspaceAdministrationFingerprint(actionKey, canonicalCode, expectedRevision, status)
	receiptID := workspaceAdministrationReceiptID(actor, actionKey, idempotencyKey)
	if replay, found, err := store.lifecycleReceipt(ctx, store.runtime.DB(), receiptID, fingerprint); found || err != nil {
		return replay, err
	}
	tx, err := store.beginAdministrationTransaction(ctx)
	if err != nil {
		return workspaceprovisionmodel.LifecycleResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if replay, found, err := store.lifecycleReceipt(ctx, tx, receiptID, fingerprint); found || err != nil {
		return replay, err
	}
	workspaceID, initial, before, err := store.workspaceForUpdate(ctx, tx, canonicalCode)
	if err != nil {
		return workspaceprovisionmodel.LifecycleResult{}, err
	}
	if status == workspaceprovisionmodel.WorkspaceStatusSuspended && initial {
		return workspaceprovisionmodel.LifecycleResult{}, workspaceprovisionmodel.ErrInitialWorkspaceSuspension
	}
	if before.Revision != expectedRevision || before.Status == status {
		return workspaceprovisionmodel.LifecycleResult{}, workspaceprovisionmodel.ErrRevisionConflict
	}
	if before.Status != workspaceprovisionmodel.WorkspaceStatusActive && before.Status != workspaceprovisionmodel.WorkspaceStatusSuspended {
		return workspaceprovisionmodel.LifecycleResult{}, workspaceprovisionmodel.ErrRevisionConflict
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	update, arguments, err := query.NewUpdateBuilder(store.runtime.RuntimeRenderer(), "_workspaces").
		Set("status", status).SetExpression("revision", query.Add(query.Column("revision"), query.Value(1))).Set("updated_at", now).
		Where(query.And(query.Equal("id", workspaceID), query.Equal("revision", expectedRevision), query.Equal("status", before.Status))).Build()
	if err != nil {
		return workspaceprovisionmodel.LifecycleResult{}, err
	}
	result, err := tx.ExecContext(ctx, update, arguments...)
	if err != nil {
		return workspaceprovisionmodel.LifecycleResult{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return workspaceprovisionmodel.LifecycleResult{}, workspaceprovisionmodel.ErrRevisionConflict
	}
	revoked := 0
	if status == workspaceprovisionmodel.WorkspaceStatusSuspended {
		revoked, err = store.revokeWorkspaceSessions(ctx, tx, workspaceID, now)
		if err != nil {
			return workspaceprovisionmodel.LifecycleResult{}, err
		}
	}
	after, err := store.workspaceEntryByID(ctx, tx, workspaceID)
	if err != nil {
		return workspaceprovisionmodel.LifecycleResult{}, err
	}
	response := workspaceprovisionmodel.LifecycleResult{Workspace: after, RevokedSessions: revoked}
	if err := store.appendAdministrationAudit(ctx, tx, receiptID, workspaceID, actor, actionKey, canonicalCode,
		map[string]any{"status": before.Status, "revision": before.Revision},
		map[string]any{"status": after.Status, "revision": after.Revision},
		map[string]any{"revoked_sessions": revoked}); err != nil {
		return workspaceprovisionmodel.LifecycleResult{}, err
	}
	if err := store.insertReceipt(ctx, tx, actor, receiptID, fingerprint, actionKey, workspaceID, response, now); err != nil {
		return workspaceprovisionmodel.LifecycleResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return workspaceprovisionmodel.LifecycleResult{}, err
	}
	return response, nil
}

func (store *WorkspaceAdministrationStore) UpdateWorkspaceCommercialConfiguration(ctx context.Context, actor workspaceprovisionmodel.AdministrationActor, canonicalCode string, request workspaceprovisionmodel.CommercialConfigurationUpdateRequest, idempotencyKey string) (workspaceprovisionmodel.CommercialConfigurationUpdateResult, error) {
	if store == nil || store.runtime == nil || store.runtime.DB() == nil {
		return workspaceprovisionmodel.CommercialConfigurationUpdateResult{}, workspaceprovisionmodel.ErrAdministrationUnavailable
	}
	actionKey := workspaceprovisionapplication.UpdateWorkspaceCommercialConfigurationActionKey
	fingerprint := workspaceAdministrationFingerprint(actionKey, canonicalCode, request.ExpectedRevision, request.Configuration)
	receiptID := workspaceAdministrationReceiptID(actor, actionKey, idempotencyKey)
	if replay, found, err := store.commercialReceipt(ctx, store.runtime.DB(), receiptID, fingerprint); found || err != nil {
		return replay, err
	}
	tx, err := store.beginAdministrationTransaction(ctx)
	if err != nil {
		return workspaceprovisionmodel.CommercialConfigurationUpdateResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if replay, found, err := store.commercialReceipt(ctx, tx, receiptID, fingerprint); found || err != nil {
		return replay, err
	}
	workspaceID, _, before, err := store.workspaceForUpdate(ctx, tx, canonicalCode)
	if err != nil {
		return workspaceprovisionmodel.CommercialConfigurationUpdateResult{}, err
	}
	if before.Revision != request.ExpectedRevision {
		return workspaceprovisionmodel.CommercialConfigurationUpdateResult{}, workspaceprovisionmodel.ErrRevisionConflict
	}
	configuration := request.Configuration
	now := time.Now().UTC().Format(time.RFC3339Nano)
	update, arguments, err := query.NewUpdateBuilder(store.runtime.RuntimeRenderer(), "_workspaces").
		Set("plan", configuration.Plan).
		Set("included_user_limit", configuration.IncludedUserLimit).Set("max_user_limit", configuration.MaxUserLimit).
		Set("included_customer_limit", configuration.IncludedCustomerLimit).Set("max_customer_limit", configuration.MaxCustomerLimit).
		Set("included_store_limit", configuration.IncludedStoreLimit).Set("max_stores", configuration.MaxStores).
		Set("contract_date", configuration.ContractDate).Set("billing_day", configuration.BillingDay).
		Set("billing_contact_name", configuration.BillingContactName).Set("billing_contact_phone", configuration.BillingContactPhone).
		Set("billing_contact_email", configuration.BillingContactEmail).Set("billing_contact_address", configuration.BillingContactAddress).
		Set("billing_contact_notes", configuration.BillingContactNotes).
		SetExpression("commercial_revision", query.Add(query.Column("commercial_revision"), query.Value(1))).
		SetExpression("revision", query.Add(query.Column("revision"), query.Value(1))).Set("updated_at", now).
		Where(query.And(
			query.Equal("id", workspaceID),
			query.Equal("revision", request.ExpectedRevision),
			query.Equal("commercial_revision", before.CommercialConfiguration.Revision),
		)).Build()
	if err != nil {
		return workspaceprovisionmodel.CommercialConfigurationUpdateResult{}, err
	}
	result, err := tx.ExecContext(ctx, update, arguments...)
	if err != nil {
		return workspaceprovisionmodel.CommercialConfigurationUpdateResult{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return workspaceprovisionmodel.CommercialConfigurationUpdateResult{}, workspaceprovisionmodel.ErrRevisionConflict
	}
	after, err := store.workspaceEntryByID(ctx, tx, workspaceID)
	if err != nil {
		return workspaceprovisionmodel.CommercialConfigurationUpdateResult{}, err
	}
	response := workspaceprovisionmodel.CommercialConfigurationUpdateResult{Workspace: after}
	if err := store.appendAdministrationAudit(ctx, tx, receiptID, workspaceID, actor, actionKey, canonicalCode,
		map[string]any{"revision": before.Revision},
		map[string]any{"revision": after.Revision},
		map[string]any{"updated_fields": []string{"plan", "included_user_limit", "max_user_limit", "included_customer_limit", "max_customer_limit", "included_store_limit", "max_stores", "contract_date", "billing_day", "billing_contact"}}); err != nil {
		return workspaceprovisionmodel.CommercialConfigurationUpdateResult{}, err
	}
	if err := store.insertReceipt(ctx, tx, actor, receiptID, fingerprint, actionKey, workspaceID, response, now); err != nil {
		return workspaceprovisionmodel.CommercialConfigurationUpdateResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return workspaceprovisionmodel.CommercialConfigurationUpdateResult{}, err
	}
	return response, nil
}

func (store *WorkspaceAdministrationStore) WorkspaceActive(ctx context.Context, workspaceID string) (bool, error) {
	if store == nil || store.runtime == nil || store.runtime.DB() == nil || strings.TrimSpace(workspaceID) == "" {
		return false, workspaceprovisionmodel.ErrAdministrationUnavailable
	}
	statement, arguments, err := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), "_workspaces").Columns("status").Where(query.Equal("id", strings.TrimSpace(workspaceID))).Limit(1).Build()
	if err != nil {
		return false, err
	}
	var status string
	if err := store.runtime.DB().QueryRowContext(ctx, statement, arguments...).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		countStatement, countArguments, buildErr := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), "_workspaces").Projections(query.Project(query.CountAll())).Build()
		if buildErr != nil {
			return false, buildErr
		}
		var count int
		if countErr := store.runtime.DB().QueryRowContext(ctx, countStatement, countArguments...).Scan(&count); countErr != nil {
			return false, countErr
		}
		// An uninitialized development/test installation has no Workspace
		// authority to consult yet. Once the catalog exists, unknown physical
		// Workspace identities fail closed.
		return count == 0, nil
	} else if err != nil {
		return false, err
	}
	return strings.TrimSpace(status) == workspaceprovisionmodel.WorkspaceStatusActive, nil
}

func (store *WorkspaceAdministrationStore) beginAdministrationTransaction(ctx context.Context) (interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	Commit(context.Context) error
	Rollback(context.Context) error
}, error) {
	if store == nil || store.runtime == nil || store.runtime.DB() == nil {
		return nil, workspaceprovisionmodel.ErrAdministrationUnavailable
	}
	return store.runtime.RuntimeProfile().BeginWrite(ctx, store.runtime.DB())
}

type workspaceAdministrationTx interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (store *WorkspaceAdministrationStore) workspaceForUpdate(ctx context.Context, tx workspaceAdministrationTx, canonicalCode string) (string, bool, workspaceprovisionmodel.CatalogEntry, error) {
	builder := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), "_workspaces").Columns("id", "initial_installation_identity").Where(query.Equal("canonical_code", canonicalCode)).Limit(1)
	builder, err := store.runtime.RuntimeProfile().ApplyClaimLock(builder, false)
	if err != nil {
		return "", false, workspaceprovisionmodel.CatalogEntry{}, err
	}
	statement, arguments, err := builder.Build()
	if err != nil {
		return "", false, workspaceprovisionmodel.CatalogEntry{}, err
	}
	var workspaceID string
	var initial sql.NullString
	if err := tx.QueryRowContext(ctx, statement, arguments...).Scan(&workspaceID, &initial); errors.Is(err, sql.ErrNoRows) {
		return "", false, workspaceprovisionmodel.CatalogEntry{}, workspaceprovisionmodel.ErrWorkspaceNotFound
	} else if err != nil {
		return "", false, workspaceprovisionmodel.CatalogEntry{}, err
	}
	entry, err := store.workspaceEntryByID(ctx, tx, workspaceID)
	return workspaceID, initial.Valid && strings.TrimSpace(initial.String) != "", entry, err
}

func (store *WorkspaceAdministrationStore) workspaceEntryByID(ctx context.Context, tx workspaceAdministrationTx, workspaceID string) (workspaceprovisionmodel.CatalogEntry, error) {
	statement, arguments, err := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), "_workspaces").Alias("workspace").
		Projections(workspaceCatalogProjections()...).
		Where(query.EqualExpressions(query.QualifiedColumn("workspace", "id"), query.Value(workspaceID))).Limit(1).Build()
	if err != nil {
		return workspaceprovisionmodel.CatalogEntry{}, err
	}
	entry, err := scanWorkspaceCatalogEntry(tx.QueryRowContext(ctx, statement, arguments...))
	if errors.Is(err, sql.ErrNoRows) {
		return workspaceprovisionmodel.CatalogEntry{}, workspaceprovisionmodel.ErrWorkspaceNotFound
	}
	if err != nil {
		return workspaceprovisionmodel.CatalogEntry{}, err
	}
	if validateWorkspaceCatalogEntry(entry) != nil {
		return workspaceprovisionmodel.CatalogEntry{}, workspaceprovisionmodel.ErrAdministrationUnavailable
	}
	return entry, nil
}

type workspaceCatalogScanner interface{ Scan(...any) error }

func scanWorkspaceCatalogEntry(scanner workspaceCatalogScanner) (workspaceprovisionmodel.CatalogEntry, error) {
	var entry workspaceprovisionmodel.CatalogEntry
	configuration := &entry.CommercialConfiguration
	err := scanner.Scan(
		&entry.CanonicalCode, &entry.DisplayName, &entry.Status, &entry.Revision,
		&configuration.Plan, &configuration.IncludedUserLimit, &configuration.MaxUserLimit,
		&configuration.IncludedCustomerLimit, &configuration.MaxCustomerLimit, &configuration.IncludedStoreLimit, &configuration.MaxStores,
		&configuration.ContractDate, &configuration.BillingDay, &configuration.BillingContactName, &configuration.BillingContactPhone,
		&configuration.BillingContactEmail, &configuration.BillingContactAddress, &configuration.BillingContactNotes, &configuration.Revision,
	)
	return entry, err
}

func workspaceCatalogProjections() []query.Projection {
	columns := []string{"canonical_code", "name", "status", "revision"}
	result := make([]query.Projection, 0, 19)
	for _, column := range columns {
		result = append(result, query.Project(query.QualifiedColumn("workspace", column)))
	}
	for _, column := range []string{"plan", "included_user_limit", "max_user_limit", "included_customer_limit", "max_customer_limit", "included_store_limit", "max_stores", "contract_date", "billing_day", "billing_contact_name", "billing_contact_phone", "billing_contact_email", "billing_contact_address", "billing_contact_notes", "commercial_revision"} {
		result = append(result, query.Project(query.QualifiedColumn("workspace", column)))
	}
	return result
}

func validateWorkspaceCatalogEntry(entry workspaceprovisionmodel.CatalogEntry) error {
	configuration := entry.CommercialConfiguration
	if workspaceprovisionvalidation.ValidateCanonicalCode(entry.CanonicalCode) != nil || strings.TrimSpace(entry.DisplayName) == "" || entry.Revision < 1 || configuration.Revision < 1 ||
		(entry.Status != workspaceprovisionmodel.WorkspaceStatusActive && entry.Status != workspaceprovisionmodel.WorkspaceStatusSuspended) ||
		strings.TrimSpace(configuration.Plan) == "" || configuration.IncludedUserLimit < 0 || configuration.MaxUserLimit < configuration.IncludedUserLimit ||
		configuration.IncludedCustomerLimit < 0 || configuration.MaxCustomerLimit < configuration.IncludedCustomerLimit ||
		configuration.IncludedStoreLimit < 1 || configuration.MaxStores < configuration.IncludedStoreLimit || configuration.BillingDay < 1 || configuration.BillingDay > 31 {
		return workspaceprovisionmodel.ErrAdministrationUnavailable
	}
	if _, err := time.Parse("2006-01-02", configuration.ContractDate); err != nil {
		return workspaceprovisionmodel.ErrAdministrationUnavailable
	}
	return nil
}

func (store *WorkspaceAdministrationStore) revokeWorkspaceSessions(ctx context.Context, tx workspaceAdministrationTx, workspaceID, now string) (int, error) {
	statement, arguments, err := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), "_identity_auth_refresh_tokens").Columns("session_id", "expires_at").
		Where(query.And(query.Equal("workspace_id", workspaceID), query.IsNull("revoked_at"))).Build()
	if err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return 0, err
	}
	active := map[string]bool{}
	revokedAt, err := time.Parse(time.RFC3339Nano, now)
	if err != nil {
		rows.Close()
		return 0, err
	}
	for rows.Next() {
		var sessionID, expiresAt string
		if err := rows.Scan(&sessionID, &expiresAt); err != nil {
			rows.Close()
			return 0, err
		}
		expires, err := time.Parse(time.RFC3339Nano, expiresAt)
		if err != nil {
			rows.Close()
			return 0, fmt.Errorf("parse Workspace session expiry: %w", err)
		}
		if expires.After(revokedAt) {
			active[strings.TrimSpace(sessionID)] = true
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	update, arguments, err := query.NewUpdateBuilder(store.runtime.RuntimeRenderer(), "_identity_auth_refresh_tokens").
		Set("revoked_at", now).Set("last_used_at", now).Set("updated_at", now).
		Where(query.And(query.Equal("workspace_id", workspaceID), query.IsNull("revoked_at"))).Build()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, update, arguments...); err != nil {
		return 0, err
	}
	return len(active), nil
}

func (store *WorkspaceAdministrationStore) appendAdministrationAudit(ctx context.Context, tx workspaceAdministrationTx, id, workspaceID string, actor workspaceprovisionmodel.AdministrationActor, actionKey, canonicalCode string, before, after, metadata map[string]any) error {
	metadata["action_key"] = actionKey
	metadata["request_id"] = actor.RequestID
	metadata["authorization_revision"] = actor.AuthorizationRevision
	event := auditmodel.AuditEvent{
		ID: id, WorkspaceID: workspaceID, Event: actionKey, ObjectKey: "workspace", RecordID: canonicalCode,
		OperationID: id, CausationID: strings.TrimSpace(actor.CausationID),
		Family:  auditmodel.EventFamilyRuntimeWorkspace,
		ActorID: actor.UserID, RoleKey: actor.RoleKey, Summary: "Governed Workspace administration",
		Before: before, After: after, Metadata: metadata, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	return store.runtime.AppendPreparedAuditWithin(ctx, runtimeauditmodule.NewTransaction(tx), event)
}

func (store *WorkspaceAdministrationStore) lifecycleReceipt(ctx context.Context, executor workspaceAdministrationTx, id, fingerprint string) (workspaceprovisionmodel.LifecycleResult, bool, error) {
	var raw string
	found, err := store.receiptJSON(ctx, executor, id, fingerprint, &raw)
	if !found || err != nil {
		return workspaceprovisionmodel.LifecycleResult{}, found, err
	}
	var result workspaceprovisionmodel.LifecycleResult
	if json.Unmarshal([]byte(raw), &result) != nil {
		return workspaceprovisionmodel.LifecycleResult{}, true, workspaceprovisionmodel.ErrAdministrationUnavailable
	}
	result.Replayed = true
	return result, true, nil
}

func (store *WorkspaceAdministrationStore) commercialReceipt(ctx context.Context, executor workspaceAdministrationTx, id, fingerprint string) (workspaceprovisionmodel.CommercialConfigurationUpdateResult, bool, error) {
	var raw string
	found, err := store.receiptJSON(ctx, executor, id, fingerprint, &raw)
	if !found || err != nil {
		return workspaceprovisionmodel.CommercialConfigurationUpdateResult{}, found, err
	}
	var result workspaceprovisionmodel.CommercialConfigurationUpdateResult
	if json.Unmarshal([]byte(raw), &result) != nil {
		return workspaceprovisionmodel.CommercialConfigurationUpdateResult{}, true, workspaceprovisionmodel.ErrAdministrationUnavailable
	}
	result.Replayed = true
	return result, true, nil
}

func (store *WorkspaceAdministrationStore) receiptJSON(ctx context.Context, executor workspaceAdministrationTx, id, fingerprint string, result *string) (bool, error) {
	if store == nil || store.runtime == nil || executor == nil {
		return false, workspaceprovisionmodel.ErrAdministrationUnavailable
	}
	record, found, err := sharedoperation.NewSQLStore(store.runtime.DB(), store.runtime.RuntimeRenderer()).GetRecord(
		sharedoperation.WithExecutor(ctx, executor),
		sharedoperation.RecordFilter{SystemPurpose: workspaceAdministrationPurpose, ID: id, Owner: workspaceAdministrationOwner, Kind: workspaceAdministrationKind},
	)
	if err != nil || !found {
		return false, err
	}
	if record.RequestFingerprint != fingerprint {
		return true, workspaceprovisionmodel.ErrIdempotencyConflict
	}
	*result = string(record.ResultJSON)
	return true, nil
}

func (store *WorkspaceAdministrationStore) insertReceipt(ctx context.Context, tx workspaceAdministrationTx, actor workspaceprovisionmodel.AdministrationActor, id, fingerprint, actionKey, workspaceID string, result any, now string) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(map[string]string{"role_key": actor.RoleKey, "authorization_revision": actor.AuthorizationRevision})
	if err != nil {
		return err
	}
	related, _ := json.Marshal([]string{workspaceID})
	evidence, _ := json.Marshal([]string{id})
	inserted, err := sharedoperation.NewSQLStore(store.runtime.DB(), store.runtime.RuntimeRenderer()).InsertRecord(
		sharedoperation.WithExecutor(ctx, tx),
		sharedoperation.Record{
			ID: id, SystemPurpose: workspaceAdministrationPurpose, Owner: workspaceAdministrationOwner, Kind: workspaceAdministrationKind,
			ActionKey: actionKey, ResourceType: "workspace", ResourceID: workspaceID, IdempotencyKey: id,
			RequestFingerprint: fingerprint, RequestedBy: actor.UserID, Reason: "Governed Workspace administration", Reference: actor.RequestID,
			Status: "succeeded", StatusURL: "/operations/" + id, ResultJSON: payload, MetadataJSON: metadata,
			RelatedIDsJSON: related, Correlation: actor.RequestID, EvidenceJSON: evidence,
			CreatedAt: now, StartedAt: now, FinishedAt: now, UpdatedAt: now,
		},
	)
	if err != nil {
		return err
	}
	if !inserted {
		return fmt.Errorf("workspace administration receipt was not inserted")
	}
	return nil
}

func workspaceAdministrationReceiptID(actor workspaceprovisionmodel.AdministrationActor, actionKey, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{"workspace-administration-v1", actor.WorkspaceID, actor.UserID, actionKey, strings.TrimSpace(idempotencyKey)}, "\x00")))
	return "workspace_admin_" + hex.EncodeToString(digest[:16])
}

func workspaceAdministrationFingerprint(values ...any) string {
	payload, _ := json.Marshal(values)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

var _ workspaceprovisionrepository.WorkspaceAdministrationRepository = (*WorkspaceAdministrationStore)(nil)
