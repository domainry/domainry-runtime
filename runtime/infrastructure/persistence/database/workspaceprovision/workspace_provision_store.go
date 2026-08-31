package workspaceprovision

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

type WorkspaceProvisionStore struct {
	runtime  *database.RuntimeStore
	identity identitysdk.EmbeddedWorkspaceProvisioner
	manifest manifestmodel.ManifestSchema
	failures FailureInjector
	issued   sync.Map
}

func NewWorkspaceProvisionStore(store *database.RuntimeStore, binding identitysdk.Binding, manifest manifestmodel.ManifestSchema) *WorkspaceProvisionStore {
	provisioner, _ := binding.(identitysdk.EmbeddedWorkspaceProvisioner)
	return &WorkspaceProvisionStore{runtime: store, identity: provisioner, manifest: manifest}
}

func NewTenantInitializationStore(store *database.RuntimeStore, binding identitysdk.BootstrapBinding, manifest manifestmodel.ManifestSchema) *WorkspaceProvisionStore {
	return &WorkspaceProvisionStore{runtime: store, identity: binding, manifest: manifest}
}

func NewWorkspaceProvisionStoreWithFailureInjector(store *database.RuntimeStore, binding identitysdk.Binding, manifest manifestmodel.ManifestSchema, failures FailureInjector) *WorkspaceProvisionStore {
	result := NewWorkspaceProvisionStore(store, binding, manifest)
	result.failures = failures
	return result
}

var codePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

func (store *WorkspaceProvisionStore) Provision(ctx context.Context, request workspaceprovisionmodel.Request) (workspaceprovisionmodel.Result, error) {
	return store.provision(ctx, request, false)
}

func (store *WorkspaceProvisionStore) Initialize(ctx context.Context, request workspaceprovisionmodel.Request, initialPassword string) (workspaceprovisionmodel.Result, error) {
	request.InitialPassword = initialPassword
	return store.provision(ctx, request, true)
}

func (store *WorkspaceProvisionStore) provision(ctx context.Context, request workspaceprovisionmodel.Request, initialize bool) (workspaceprovisionmodel.Result, error) {
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.TenantCode = canonicalCode(request.TenantCode)
	request.TenantName = strings.TrimSpace(request.TenantName)
	request.AdminLoginID = strings.ToLower(strings.TrimSpace(request.AdminLoginID))
	request.AdminName = strings.TrimSpace(request.AdminName)
	if store == nil || store.runtime == nil || store.identity == nil {
		return workspaceprovisionmodel.Result{}, workspaceprovisionmodel.ErrIdentityUnavailable
	}
	if request.RequestID == "" || !codePattern.MatchString(request.TenantCode) || strings.EqualFold(request.TenantCode, "default") || request.TenantName == "" || request.AdminLoginID == "" || request.AdminName == "" {
		return workspaceprovisionmodel.Result{}, workspaceprovisionmodel.ErrInvalid
	}
	configuration, err := json.Marshal(request.StoreConfiguration)
	if err != nil {
		return workspaceprovisionmodel.Result{}, workspaceprovisionmodel.ErrInvalid
	}
	fingerprint := requestFingerprint(request, configuration)
	if replay, found, replayErr := store.receipt(ctx, request.RequestID, fingerprint); found || replayErr != nil {
		if replayErr != nil {
			return workspaceprovisionmodel.Result{}, replayErr
		}
		if password, ok := store.issued.Load(request.RequestID); ok {
			replay.InitialPassword, _ = password.(string)
		}
		replay.Replayed = true
		return replay, nil
	}
	installation, initialized, err := LoadInstallation(ctx, store.runtime)
	if err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	if initialize && initialized {
		return workspaceprovisionmodel.Result{}, workspaceprovisionmodel.ErrAlreadyInitialized
	}
	if !initialize && !initialized {
		return workspaceprovisionmodel.Result{}, workspaceprovisionmodel.ErrInitializationRequired
	}
	tenantRegistryID, err := randomID("tenant")
	if err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	workspaceID, err := randomID("workspace")
	if err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	result := workspaceprovisionmodel.Result{
		TenantRegistryID: tenantRegistryID, WorkspaceID: workspaceID, CanonicalCode: request.TenantCode,
		AdminLoginID: request.AdminLoginID, MustChangePassword: true,
	}
	result.ProjectionIDs = store.applicationProjectionIDs(result.WorkspaceID)
	tx, err := store.runtime.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := store.insertRuntimeWorkspace(ctx, tx, request, result, string(configuration)); err != nil {
		_ = tx.Rollback()
		return store.afterFailedInsert(ctx, request, fingerprint, err)
	}
	identityResult, err := store.identity.ProvisionWorkspaceIdentity(ctx, identitysdk.WorkspaceIdentityProvisionRequest{
		WorkspaceID: result.WorkspaceID, AdminLoginID: request.AdminLoginID, AdminName: request.AdminName, InitialPassword: request.InitialPassword,
	}, identitysdk.EmbeddedTransaction{Native: tx, WorkspaceProvisionFailures: store.identityFailureInjector()})
	if err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	result.AdminLoginID, result.InitialPassword, result.MustChangePassword = identityResult.AdminLoginID, identityResult.InitialPassword, identityResult.MustChangePassword
	headquartersWorkspaceID := installation.WorkspaceID
	if initialize {
		headquartersWorkspaceID = result.WorkspaceID
		if err := store.insertInstallation(ctx, tx, result); err != nil {
			return workspaceprovisionmodel.Result{}, err
		}
	}
	if err := store.insertConfigurationProjectionsAndReceipt(ctx, tx, request, result, headquartersWorkspaceID, fingerprint, string(configuration)); err != nil {
		_ = tx.Rollback()
		return store.afterFailedInsert(ctx, request, fingerprint, err)
	}
	if err := tx.Commit(); err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	store.issued.Store(request.RequestID, result.InitialPassword)
	return result, nil
}

func (store *WorkspaceProvisionStore) insertInstallation(ctx context.Context, tx *sql.Tx, result workspaceprovisionmodel.Result) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := insert(ctx, tx, query.NewInsertBuilder(store.runtime.RuntimeRenderer(), "_tenant_installation").
		Columns("installation_key", "tenant_registry_id", "workspace_id", "initialized_at").
		Values(installationKey, result.TenantRegistryID, result.WorkspaceID, now)); err != nil {
		return workspaceprovisionmodel.ErrAlreadyInitialized
	}
	return store.inject(FailureAfterInstallation)
}

func (store *WorkspaceProvisionStore) ReconcileWorkspaceRoles(ctx context.Context, workspaceID string) (workspaceprovisionmodel.RoleReconciliationResult, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if store == nil || store.runtime == nil || store.identity == nil || len(workspaceID) == 0 {
		return workspaceprovisionmodel.RoleReconciliationResult{}, workspaceprovisionmodel.ErrIdentityUnavailable
	}
	tx, err := store.runtime.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return workspaceprovisionmodel.RoleReconciliationResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	queryValue, arguments, err := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), "_workspaces").Columns("id").Where(query.Equal("id", workspaceID)).Build()
	if err != nil {
		return workspaceprovisionmodel.RoleReconciliationResult{}, err
	}
	var found string
	if err := tx.QueryRowContext(ctx, queryValue, arguments...).Scan(&found); errors.Is(err, sql.ErrNoRows) {
		return workspaceprovisionmodel.RoleReconciliationResult{}, workspaceprovisionmodel.ErrWorkspaceNotFound
	} else if err != nil {
		return workspaceprovisionmodel.RoleReconciliationResult{}, err
	}
	receipt, err := store.identity.ReconcileWorkspaceRoles(ctx, identitysdk.WorkspaceRoleReconcileRequest{WorkspaceID: workspaceID}, identitysdk.EmbeddedTransaction{Native: tx})
	if err != nil {
		return workspaceprovisionmodel.RoleReconciliationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return workspaceprovisionmodel.RoleReconciliationResult{}, err
	}
	return workspaceprovisionmodel.RoleReconciliationResult{WorkspaceID: workspaceID, ProvisionedRoles: receipt.ProvisionedRoles}, nil
}

func (store *WorkspaceProvisionStore) insertRuntimeWorkspace(ctx context.Context, tx *sql.Tx, request workspaceprovisionmodel.Request, result workspaceprovisionmodel.Result, configuration string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := insert(ctx, tx, query.NewInsertBuilder(store.runtime.RuntimeRenderer(), "_workspaces").Columns("id", "canonical_code", "name", "status", "created_at", "updated_at").Values(result.WorkspaceID, result.CanonicalCode, request.TenantName, "active", now, now)); err != nil {
		return err
	}
	if err := store.inject(FailureAfterWorkspace); err != nil {
		return err
	}
	if err := insert(ctx, tx, query.NewInsertBuilder(store.runtime.RuntimeRenderer(), "_tenant_registry").Columns("id", "workspace_id", "canonical_code", "created_at", "updated_at").Values(result.TenantRegistryID, result.WorkspaceID, result.CanonicalCode, now, now)); err != nil {
		return err
	}
	return store.inject(FailureAfterTenantRegistry)
}

func (store *WorkspaceProvisionStore) insertConfigurationProjectionsAndReceipt(ctx context.Context, tx *sql.Tx, request workspaceprovisionmodel.Request, result workspaceprovisionmodel.Result, headquartersWorkspaceID, fingerprint, configuration string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := insert(ctx, tx, query.NewInsertBuilder(store.runtime.RuntimeRenderer(), "_workspace_configuration").Columns("workspace_id", "configuration_json", "created_at", "updated_at").Values(result.WorkspaceID, configuration, now, now)); err != nil {
		return err
	}
	if err := store.inject(FailureAfterWorkspaceConfiguration); err != nil {
		return err
	}
	if err := store.insertApplicationProjections(ctx, tx, request, result, headquartersWorkspaceID, now); err != nil {
		return err
	}
	if err := store.inject(FailureAfterApplicationProjections); err != nil {
		return err
	}
	projectionIDs, err := json.Marshal(result.ProjectionIDs)
	if err != nil {
		return err
	}
	if err := insert(ctx, tx, query.NewInsertBuilder(store.runtime.RuntimeRenderer(), "_workspace_provisioning_receipts").Columns("request_id", "request_fingerprint", "tenant_registry_id", "workspace_id", "canonical_code", "admin_login_id", "must_change_password", "application_projection_ids_json", "created_at").Values(request.RequestID, fingerprint, result.TenantRegistryID, result.WorkspaceID, result.CanonicalCode, result.AdminLoginID, result.MustChangePassword, string(projectionIDs), now)); err != nil {
		return err
	}
	return store.inject(FailureAfterReceipt)
}

func (store *WorkspaceProvisionStore) insertApplicationProjections(ctx context.Context, tx *sql.Tx, request workspaceprovisionmodel.Request, result workspaceprovisionmodel.Result, headquartersWorkspaceID, now string) error {
	objects := map[string]definitionmodel.ObjectSchema{}
	for _, object := range store.manifest.Objects {
		objects[object.Key] = object
	}
	for _, projection := range store.manifest.WorkspaceProvisioning {
		object, found := objects[projection.ObjectKey]
		if !found {
			return fmt.Errorf("workspace provisioning projection object %q is unavailable", projection.ObjectKey)
		}
		workspaceID := result.WorkspaceID
		if projection.Scope == "headquarters" {
			workspaceID = headquartersWorkspaceID
		}
		columns := []string{"workspace_id", "id", "created_at", "updated_at"}
		values := []any{workspaceID, result.ProjectionIDs[projection.Key], now, now}
		fields := map[string]definitionmodel.FieldSchema{}
		for _, field := range object.Fields {
			fields[field.Key] = field
		}
		keys := make([]string, 0, len(projection.Data))
		for key := range projection.Data {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		normalized := map[string]any{}
		for _, key := range keys {
			value, err := projectionValue(projection.Data[key], request, result)
			if err != nil {
				return workspaceprovisionmodel.ErrInvalid
			}
			value, err = recordvalidation.RecordNormalizeFieldValue(fields[key], value)
			if err != nil {
				return workspaceprovisionmodel.ErrInvalid
			}
			normalized[key] = value
			columns, values = append(columns, key), append(values, recordpersistence.RecordDatabaseFieldValue(store.runtime.RuntimeProfile(), fields[key], value))
		}
		if err := recordvalidation.RecordValidateData(object, normalized, false); err != nil {
			return workspaceprovisionmodel.ErrInvalid
		}
		if err := insert(ctx, tx, query.NewInsertBuilder(store.runtime.RuntimeRenderer(), projection.ObjectKey).Columns(columns...).Values(values...)); err != nil {
			return fmt.Errorf("insert workspace provisioning projection %s: %w", projection.Key, err)
		}
		if err := store.inject(FailureAfterApplicationProjection + projection.Key); err != nil {
			return err
		}
	}
	return nil
}

func insert(ctx context.Context, tx *sql.Tx, builder *query.InsertBuilder) error {
	statement, arguments, err := builder.Build()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, statement, arguments...)
	return err
}

func (store *WorkspaceProvisionStore) receipt(ctx context.Context, requestID, fingerprint string) (workspaceprovisionmodel.Result, bool, error) {
	statement, arguments, err := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), "_workspace_provisioning_receipts").Columns("request_fingerprint", "tenant_registry_id", "workspace_id", "canonical_code", "admin_login_id", "must_change_password", "application_projection_ids_json").Where(query.Equal("request_id", requestID)).Build()
	if err != nil {
		return workspaceprovisionmodel.Result{}, false, err
	}
	var stored, projectionIDs string
	var result workspaceprovisionmodel.Result
	err = store.runtime.DB().QueryRowContext(ctx, statement, arguments...).Scan(&stored, &result.TenantRegistryID, &result.WorkspaceID, &result.CanonicalCode, &result.AdminLoginID, &result.MustChangePassword, &projectionIDs)
	if errors.Is(err, sql.ErrNoRows) {
		return workspaceprovisionmodel.Result{}, false, nil
	}
	if err != nil {
		return workspaceprovisionmodel.Result{}, false, err
	}
	if stored != fingerprint {
		return workspaceprovisionmodel.Result{}, true, workspaceprovisionmodel.ErrIdempotencyConflict
	}
	if err := json.Unmarshal([]byte(projectionIDs), &result.ProjectionIDs); err != nil {
		return workspaceprovisionmodel.Result{}, true, err
	}
	return result, true, nil
}

func (store *WorkspaceProvisionStore) afterFailedInsert(ctx context.Context, request workspaceprovisionmodel.Request, fingerprint string, insertErr error) (workspaceprovisionmodel.Result, error) {
	if replay, found, err := store.receipt(ctx, request.RequestID, fingerprint); found || err != nil {
		if err != nil {
			return workspaceprovisionmodel.Result{}, err
		}
		replay.Replayed = true
		return replay, nil
	}
	statement, arguments, err := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), "_workspaces").Columns("id").Where(query.Equal("canonical_code", request.TenantCode)).Build()
	if err == nil {
		var id string
		if scanErr := store.runtime.DB().QueryRowContext(ctx, statement, arguments...).Scan(&id); scanErr == nil && id != "" {
			return workspaceprovisionmodel.Result{}, workspaceprovisionmodel.ErrCodeConflict
		}
	}
	return workspaceprovisionmodel.Result{}, insertErr
}

func (store *WorkspaceProvisionStore) inject(point string) error {
	if store.failures == nil {
		return nil
	}
	return store.failures.Inject(point)
}

type identityFailureInjectorAdapter struct{ failures FailureInjector }

func (adapter identityFailureInjectorAdapter) InjectWorkspaceProvisionFailure(point string) error {
	return adapter.failures.Inject(point)
}

func (store *WorkspaceProvisionStore) identityFailureInjector() identitysdk.WorkspaceProvisionFailureInjector {
	if store == nil || store.failures == nil {
		return nil
	}
	return identityFailureInjectorAdapter{failures: store.failures}
}

func (store *WorkspaceProvisionStore) applicationProjectionIDs(workspaceID string) map[string]string {
	ids := map[string]string{}
	for _, projection := range store.manifest.WorkspaceProvisioning {
		sum := sha256.Sum256([]byte(workspaceID + "\x00" + projection.Key + "\x00" + projection.ObjectKey))
		ids[projection.Key] = projection.ObjectKey + "_" + hex.EncodeToString(sum[:12])
	}
	return ids
}

func projectionValue(value any, request workspaceprovisionmodel.Request, result workspaceprovisionmodel.Result) (any, error) {
	text, ok := value.(string)
	if !ok || !strings.HasPrefix(text, "$provision.") {
		return value, nil
	}
	switch text {
	case "$provision.canonical_code":
		return result.CanonicalCode, nil
	case "$provision.tenant_name":
		return request.TenantName, nil
	case "$provision.workspace_id":
		return result.WorkspaceID, nil
	case "$provision.admin_login_id":
		return result.AdminLoginID, nil
	case "$provision.configuration_json":
		payload, err := json.Marshal(request.StoreConfiguration)
		return string(payload), err
	}
	if key := strings.TrimPrefix(text, "$provision.configuration."); key != text && key != "" {
		value, found := request.StoreConfiguration[key]
		if !found {
			return nil, fmt.Errorf("configuration key %q is missing", key)
		}
		return value, nil
	}
	return nil, fmt.Errorf("unsupported provisioning expression %q", text)
}

func canonicalCode(value string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "_", "-")
}

func requestFingerprint(request workspaceprovisionmodel.Request, configuration []byte) string {
	payload := strings.Join([]string{request.TenantCode, request.TenantName, request.AdminLoginID, request.AdminName, string(configuration)}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func randomID(prefix string) (string, error) {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate workspace provisioning identity: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(buffer), nil
}
