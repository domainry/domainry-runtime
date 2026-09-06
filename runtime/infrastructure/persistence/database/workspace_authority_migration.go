package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-orm/query"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
)

// migrateLegacyWorkspaceAuthorities is the only Runtime V3 code allowed to
// read the retired registry, installation marker, opaque configuration, and
// V2 receipt tables. It validates their one-to-one relationship, copies their
// facts into Workspace-owned authorities, and is safe to resume after a
// process restart. The retired tables are intentionally left as source
// artifacts; normal Runtime paths never read or write them.
func (s *RuntimeStore) migrateLegacyWorkspaceAuthorities(ctx context.Context) error {
	legacyInstallation, err := s.runtimeTableExistsForMigration(ctx, "_tenant_installation")
	if err != nil {
		return err
	}
	legacyRegistry, err := s.runtimeTableExistsForMigration(ctx, "_tenant_registry")
	if err != nil {
		return err
	}
	legacyConfiguration, err := s.runtimeTableExistsForMigration(ctx, "_workspace_configuration")
	if err != nil {
		return err
	}
	legacyReceipts, err := s.runtimeTableExistsForMigration(ctx, "_workspace_provisioning_receipts")
	if err != nil {
		return err
	}
	if !legacyInstallation && !legacyRegistry && !legacyConfiguration && !legacyReceipts {
		return nil
	}
	if !legacyInstallation || !legacyRegistry || !legacyConfiguration {
		return fmt.Errorf("workspace authority migration: incomplete legacy authority set")
	}

	installationIdentity, err := s.InstallationIdentity(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin workspace authority migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	workspaces, err := s.loadLegacyMigrationWorkspaces(ctx, tx)
	if err != nil {
		return err
	}
	registries, err := s.loadAndValidateLegacyRegistries(ctx, tx, workspaces)
	if err != nil {
		return err
	}
	initialWorkspaceID, initializedAt, err := s.loadAndValidateLegacyInstallation(ctx, tx, workspaces, registries)
	if err != nil {
		return err
	}
	if err := s.markMigratedInitialWorkspace(ctx, tx, initialWorkspaceID, installationIdentity); err != nil {
		return err
	}
	if err := s.migrateLegacyCommercialConfigurations(ctx, tx, workspaces); err != nil {
		return err
	}
	if legacyReceipts {
		if err := s.migrateLegacyWorkspaceReceipts(ctx, tx, workspaces, initializedAt); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workspace authority migration: %w", err)
	}
	return nil
}

type legacyMigrationWorkspace struct {
	ID, Code, Name, Status, CreatedAt string
}

type legacyMigrationRegistry struct {
	ID, WorkspaceID, Code string
}

func (s *RuntimeStore) loadLegacyMigrationWorkspaces(ctx context.Context, tx *sql.Tx) (map[string]legacyMigrationWorkspace, error) {
	statement, arguments, err := query.NewSelectBuilder(s.RuntimeRenderer(), "_workspaces").Columns("id", "canonical_code", "name", "status", "created_at").Build()
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("workspace authority migration: load Workspaces: %w", err)
	}
	defer rows.Close()
	result := map[string]legacyMigrationWorkspace{}
	codes := map[string]bool{}
	for rows.Next() {
		var value legacyMigrationWorkspace
		if err := rows.Scan(&value.ID, &value.Code, &value.Name, &value.Status, &value.CreatedAt); err != nil {
			return nil, err
		}
		value.ID, value.Code = strings.TrimSpace(value.ID), strings.TrimSpace(value.Code)
		if value.ID == "" || value.Code == "" || result[value.ID].ID != "" || codes[value.Code] {
			return nil, fmt.Errorf("workspace authority migration: duplicate or invalid Workspace authority")
		}
		result[value.ID], codes[value.Code] = value, true
	}
	return result, rows.Err()
}

func (s *RuntimeStore) loadAndValidateLegacyRegistries(ctx context.Context, tx *sql.Tx, workspaces map[string]legacyMigrationWorkspace) (map[string]legacyMigrationRegistry, error) {
	statement, arguments, err := query.NewSelectBuilder(s.RuntimeRenderer(), "_tenant_registry").Columns("id", "workspace_id", "canonical_code").Build()
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("workspace authority migration: load retired registry: %w", err)
	}
	defer rows.Close()
	result, codes := map[string]legacyMigrationRegistry{}, map[string]bool{}
	for rows.Next() {
		var value legacyMigrationRegistry
		if err := rows.Scan(&value.ID, &value.WorkspaceID, &value.Code); err != nil {
			return nil, err
		}
		value.ID, value.WorkspaceID, value.Code = strings.TrimSpace(value.ID), strings.TrimSpace(value.WorkspaceID), strings.TrimSpace(value.Code)
		workspace, found := workspaces[value.WorkspaceID]
		if value.ID == "" || !found || value.Code != workspace.Code || result[value.WorkspaceID].ID != "" || codes[value.Code] {
			return nil, fmt.Errorf("workspace authority migration: conflicting retired registry mapping")
		}
		result[value.WorkspaceID], codes[value.Code] = value, true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(result) != len(workspaces) {
		return nil, fmt.Errorf("workspace authority migration: missing retired registry mapping")
	}
	return result, nil
}

func (s *RuntimeStore) loadAndValidateLegacyInstallation(ctx context.Context, tx *sql.Tx, workspaces map[string]legacyMigrationWorkspace, registries map[string]legacyMigrationRegistry) (string, string, error) {
	statement, arguments, err := query.NewSelectBuilder(s.RuntimeRenderer(), "_tenant_installation").Columns("installation_key", "tenant_registry_id", "workspace_id", "initialized_at").Build()
	if err != nil {
		return "", "", err
	}
	rows, err := tx.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return "", "", fmt.Errorf("workspace authority migration: load retired installation marker: %w", err)
	}
	defer rows.Close()
	count, workspaceID, initializedAt := 0, "", ""
	for rows.Next() {
		var key, registryID string
		if err := rows.Scan(&key, &registryID, &workspaceID, &initializedAt); err != nil {
			return "", "", err
		}
		count++
		registry, found := registries[strings.TrimSpace(workspaceID)]
		if strings.TrimSpace(key) != "primary" || !found || strings.TrimSpace(registryID) != registry.ID || workspaces[strings.TrimSpace(workspaceID)].ID == "" {
			return "", "", fmt.Errorf("workspace authority migration: conflicting retired installation marker")
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", err
	}
	if count != 1 {
		return "", "", fmt.Errorf("workspace authority migration: expected exactly one retired installation marker")
	}
	return strings.TrimSpace(workspaceID), strings.TrimSpace(initializedAt), nil
}

func (s *RuntimeStore) markMigratedInitialWorkspace(ctx context.Context, tx *sql.Tx, workspaceID, installationIdentity string) error {
	statement, arguments, err := query.NewUpdateBuilder(s.RuntimeRenderer(), "_workspaces").Set("initial_installation_identity", installationIdentity).
		Where(query.And(query.Equal("id", workspaceID), query.Or(query.IsNull("initial_installation_identity"), query.Equal("initial_installation_identity", installationIdentity)))).Build()
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return fmt.Errorf("workspace authority migration: mark initial Workspace: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("workspace authority migration: inspect initial Workspace marker: %w", err)
	}
	if affected == 0 {
		read, readArguments, buildErr := query.NewSelectBuilder(s.RuntimeRenderer(), "_workspaces").Columns("initial_installation_identity").Where(query.Equal("id", workspaceID)).Build()
		if buildErr != nil {
			return buildErr
		}
		var stored sql.NullString
		if scanErr := tx.QueryRowContext(ctx, read, readArguments...).Scan(&stored); scanErr != nil || !stored.Valid || stored.String != installationIdentity {
			return fmt.Errorf("workspace authority migration: initial Workspace marker conflict")
		}
	} else if affected != 1 {
		return fmt.Errorf("workspace authority migration: multiple initial Workspace markers changed")
	}
	return nil
}

func (s *RuntimeStore) migrateLegacyCommercialConfigurations(ctx context.Context, tx *sql.Tx, workspaces map[string]legacyMigrationWorkspace) error {
	statement, arguments, err := query.NewSelectBuilder(s.RuntimeRenderer(), "_workspace_configuration").Columns("workspace_id", "configuration_json", "created_at", "updated_at").Build()
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return fmt.Errorf("workspace authority migration: load retired configuration: %w", err)
	}
	type item struct{ workspaceID, raw, createdAt, updatedAt string }
	var items []item
	seen := map[string]bool{}
	for rows.Next() {
		var value item
		if err := rows.Scan(&value.workspaceID, &value.raw, &value.createdAt, &value.updatedAt); err != nil {
			_ = rows.Close()
			return err
		}
		value.workspaceID = strings.TrimSpace(value.workspaceID)
		if workspaces[value.workspaceID].ID == "" || seen[value.workspaceID] {
			_ = rows.Close()
			return fmt.Errorf("workspace authority migration: conflicting retired configuration")
		}
		seen[value.workspaceID] = true
		items = append(items, value)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(seen) != len(workspaces) {
		return fmt.Errorf("workspace authority migration: missing retired configuration")
	}
	for _, value := range items {
		configuration, err := decodeLegacyCommercialConfiguration(value.raw, value.createdAt)
		if err != nil {
			return fmt.Errorf("workspace authority migration: Workspace %q configuration: %w", value.workspaceID, err)
		}
		builder := query.NewInsertBuilder(s.RuntimeRenderer(), "_workspace_commercial_configuration").Columns(
			"workspace_id", "plan", "included_user_limit", "max_user_limit", "included_customer_limit", "max_customer_limit",
			"included_store_limit", "max_stores", "contract_date", "billing_day", "billing_contact_name", "billing_contact_phone",
			"billing_contact_email", "billing_contact_address", "billing_contact_notes", "revision", "created_at", "updated_at",
		).Values(
			value.workspaceID, configuration.Plan, configuration.IncludedUserLimit, configuration.MaxUserLimit,
			configuration.IncludedCustomerLimit, configuration.MaxCustomerLimit, configuration.IncludedStoreLimit, configuration.MaxStores,
			configuration.ContractDate, configuration.BillingDay, configuration.BillingContactName, configuration.BillingContactPhone,
			configuration.BillingContactEmail, configuration.BillingContactAddress, configuration.BillingContactNotes, 1, value.createdAt, value.updatedAt,
		)
		if err := executeMigrationInsert(ctx, tx, builder); err != nil && !s.workspaceCommercialConfigurationAlreadyMigrated(ctx, tx, value.workspaceID) {
			return fmt.Errorf("workspace authority migration: insert typed commercial configuration: %w", err)
		}
	}
	return nil
}

func decodeLegacyCommercialConfiguration(raw, createdAt string) (workspaceprovisionmodel.CommercialConfiguration, error) {
	_ = createdAt
	typed := struct {
		Plan                  *string `json:"plan"`
		IncludedUserLimit     *int    `json:"included_user_limit"`
		MaxUserLimit          *int    `json:"max_user_limit"`
		IncludedCustomerLimit *int    `json:"included_customer_limit"`
		MaxCustomerLimit      *int    `json:"max_customer_limit"`
		IncludedStoreLimit    *int    `json:"included_store_limit"`
		MaxStores             *int    `json:"max_stores"`
		ContractDate          *string `json:"contract_date"`
		BillingDay            *int    `json:"billing_day"`
		BillingContactName    *string `json:"billing_contact_name"`
		BillingContactPhone   *string `json:"billing_contact_phone"`
		BillingContactEmail   *string `json:"billing_contact_email"`
		BillingContactAddress *string `json:"billing_contact_address"`
		BillingContactNotes   *string `json:"billing_contact_notes"`
	}{}
	decoder := json.NewDecoder(bytes.NewBufferString(strings.TrimSpace(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&typed); err != nil {
		return workspaceprovisionmodel.CommercialConfiguration{}, err
	}
	if typed.Plan == nil || typed.IncludedUserLimit == nil || typed.MaxUserLimit == nil || typed.IncludedCustomerLimit == nil || typed.MaxCustomerLimit == nil ||
		typed.IncludedStoreLimit == nil || typed.MaxStores == nil || typed.ContractDate == nil || typed.BillingDay == nil || typed.BillingContactName == nil ||
		typed.BillingContactPhone == nil || typed.BillingContactEmail == nil || typed.BillingContactAddress == nil || typed.BillingContactNotes == nil {
		return workspaceprovisionmodel.CommercialConfiguration{}, fmt.Errorf("adjudication required: legacy configuration lacks the complete typed commercial contract")
	}
	result := workspaceprovisionmodel.CommercialConfiguration{
		Plan: *typed.Plan, IncludedUserLimit: *typed.IncludedUserLimit, MaxUserLimit: *typed.MaxUserLimit,
		IncludedCustomerLimit: *typed.IncludedCustomerLimit, MaxCustomerLimit: *typed.MaxCustomerLimit,
		IncludedStoreLimit: *typed.IncludedStoreLimit, MaxStores: *typed.MaxStores, ContractDate: *typed.ContractDate, BillingDay: *typed.BillingDay,
		BillingContactName: *typed.BillingContactName, BillingContactPhone: *typed.BillingContactPhone, BillingContactEmail: *typed.BillingContactEmail,
		BillingContactAddress: *typed.BillingContactAddress, BillingContactNotes: *typed.BillingContactNotes,
	}
	if strings.TrimSpace(result.Plan) == "" || result.IncludedUserLimit < 0 || result.MaxUserLimit < result.IncludedUserLimit ||
		result.IncludedCustomerLimit < 0 || result.MaxCustomerLimit < result.IncludedCustomerLimit || result.IncludedStoreLimit < 1 ||
		result.MaxStores < result.IncludedStoreLimit || result.BillingDay < 1 || result.BillingDay > 31 {
		return workspaceprovisionmodel.CommercialConfiguration{}, fmt.Errorf("adjudication required: invalid typed commercial values")
	}
	if _, err := time.Parse("2006-01-02", strings.TrimSpace(result.ContractDate)); err != nil {
		return workspaceprovisionmodel.CommercialConfiguration{}, fmt.Errorf("adjudication required: invalid contract_date")
	}
	return result, nil
}

func (s *RuntimeStore) migrateLegacyWorkspaceReceipts(ctx context.Context, tx *sql.Tx, workspaces map[string]legacyMigrationWorkspace, initializedAt string) error {
	statement, arguments, err := query.NewSelectBuilder(s.RuntimeRenderer(), "_workspace_provisioning_receipts").Columns(
		"request_id", "request_fingerprint", "workspace_id", "canonical_code", "admin_login_id", "must_change_password", "created_at",
	).Build()
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return fmt.Errorf("workspace authority migration: load retired receipts: %w", err)
	}
	type item struct {
		requestID, fingerprint, workspaceID, code, loginID, createdAt string
		mustChange                                                    bool
	}
	var items []item
	for rows.Next() {
		var value item
		if err := rows.Scan(&value.requestID, &value.fingerprint, &value.workspaceID, &value.code, &value.loginID, &value.mustChange, &value.createdAt); err != nil {
			_ = rows.Close()
			return err
		}
		workspace, found := workspaces[strings.TrimSpace(value.workspaceID)]
		if !found || strings.TrimSpace(value.code) != workspace.Code || strings.TrimSpace(value.requestID) == "" {
			_ = rows.Close()
			return fmt.Errorf("workspace authority migration: conflicting retired receipt")
		}
		items = append(items, value)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, value := range items {
		createdAt := strings.TrimSpace(value.createdAt)
		if createdAt == "" {
			createdAt = initializedAt
		}
		builder := query.NewInsertBuilder(s.RuntimeRenderer(), "_workspace_provisioning_receipts_v3").Columns(
			"request_id", "request_fingerprint", "workspace_id", "canonical_code", "admin_login_id", "must_change_password",
			"receipt_status", "identity_receipt_id", "identity_contract_version", "identity_contract_hash", "company_id", "first_store_id", "initial_admin_user_id", "created_at",
		).Values(
			value.requestID, value.fingerprint, value.workspaceID, value.code, value.loginID, value.mustChange,
			"identity_graph_adjudication_required", nil, nil, nil, nil, nil, nil, createdAt,
		)
		if err := executeMigrationInsert(ctx, tx, builder); err != nil && !s.workspaceReceiptAlreadyMigrated(ctx, tx, value.requestID, value.workspaceID, value.fingerprint) {
			return fmt.Errorf("workspace authority migration: insert V3 receipt: %w", err)
		}
	}
	return nil
}

func (s *RuntimeStore) workspaceCommercialConfigurationAlreadyMigrated(ctx context.Context, tx *sql.Tx, workspaceID string) bool {
	statement, arguments, err := query.NewSelectBuilder(s.RuntimeRenderer(), "_workspace_commercial_configuration").Columns("workspace_id").Where(query.Equal("workspace_id", workspaceID)).Build()
	if err != nil {
		return false
	}
	var stored string
	return tx.QueryRowContext(ctx, statement, arguments...).Scan(&stored) == nil && stored == workspaceID
}

func (s *RuntimeStore) workspaceReceiptAlreadyMigrated(ctx context.Context, tx *sql.Tx, requestID, workspaceID, fingerprint string) bool {
	statement, arguments, err := query.NewSelectBuilder(s.RuntimeRenderer(), "_workspace_provisioning_receipts_v3").Columns("workspace_id", "request_fingerprint").Where(query.Equal("request_id", requestID)).Build()
	if err != nil {
		return false
	}
	var storedWorkspace, storedFingerprint string
	return tx.QueryRowContext(ctx, statement, arguments...).Scan(&storedWorkspace, &storedFingerprint) == nil && storedWorkspace == workspaceID && storedFingerprint == fingerprint
}

func executeMigrationInsert(ctx context.Context, tx *sql.Tx, builder *query.InsertBuilder) error {
	statement, arguments, err := builder.Build()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, statement, arguments...)
	return err
}

func (s *RuntimeStore) runtimeTableExistsForMigration(ctx context.Context, table string) (bool, error) {
	exists, err := s.RuntimeTableExists(ctx, table)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("workspace authority migration: inspect %s: %w", table, err)
	}
	return exists, nil
}
