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
	"sort"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	workspaceprovisionvalidation "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/validation"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

const workspaceProvisioningReceiptTable = "_workspace_provisioning_receipts_v3"

type WorkspaceProvisionStore struct {
	runtime     *database.RuntimeStore
	bootstrap   identitysdk.WorkspaceIdentityBootstrap
	manifest    manifestmodel.ManifestSchema
	rolePolicy  WorkspaceBootstrapRolePolicyEvidence
	participant runtimeext.WorkspaceBootstrapParticipant
	failures    FailureInjector
}

// WorkspaceBootstrapRolePolicyEvidence is the host-side expectation for the
// role-policy evidence returned by Identity's bootstrap receipt. It is
// derived from the exact catalog sent to Identity through the SDK's shared
// canonicalization function.
type WorkspaceBootstrapRolePolicyEvidence struct {
	RoleCatalogSHA256                    string
	NavigationCatalogSHA256              string
	InitialWorkspaceAdministratorRoleKey string
}

func NewWorkspaceBootstrapRolePolicyEvidence(catalog identitysdk.ProjectRoleCatalog, navigation ...identitysdk.ProjectNavigationCatalog) (WorkspaceBootstrapRolePolicyEvidence, error) {
	digest, err := identitysdk.WorkspaceBootstrapProjectRoleCatalogSHA256(catalog)
	if err != nil {
		return WorkspaceBootstrapRolePolicyEvidence{}, err
	}
	administratorRoleKey := strings.TrimSpace(catalog.InitialWorkspaceAdministratorRoleKey)
	if administratorRoleKey == "" {
		return WorkspaceBootstrapRolePolicyEvidence{}, fmt.Errorf("Workspace bootstrap initial administrator role is required")
	}
	navigationCatalog := identitysdk.ProjectNavigationCatalog{ContractVersion: identitysdk.ProjectNavigationContractVersion, Menus: []identitysdk.ProjectMenuDefinition{}}
	if len(navigation) > 0 {
		navigationCatalog = navigation[0]
	}
	navigationDigest, err := identitysdk.ProjectNavigationCatalogSHA256(navigationCatalog)
	if err != nil {
		return WorkspaceBootstrapRolePolicyEvidence{}, err
	}
	return WorkspaceBootstrapRolePolicyEvidence{
		RoleCatalogSHA256: digest, NavigationCatalogSHA256: navigationDigest,
		InitialWorkspaceAdministratorRoleKey: administratorRoleKey,
	}, nil
}

// NewWorkspaceProvisionStore accepts an ordinary initialized Binding, but the
// provisioning path requires its single current bootstrap capability.
func NewWorkspaceProvisionStore(store *database.RuntimeStore, binding identitysdk.Binding, manifest manifestmodel.ManifestSchema, rolePolicy ...WorkspaceBootstrapRolePolicyEvidence) *WorkspaceProvisionStore {
	bootstrap, _ := binding.(identitysdk.WorkspaceIdentityBootstrap)
	return &WorkspaceProvisionStore{runtime: store, bootstrap: bootstrap, manifest: manifest, rolePolicy: firstWorkspaceBootstrapRolePolicy(rolePolicy)}
}

func NewWorkspaceProvisionStoreWithParticipant(store *database.RuntimeStore, binding identitysdk.Binding, manifest manifestmodel.ManifestSchema, participant runtimeext.WorkspaceBootstrapParticipant, rolePolicy ...WorkspaceBootstrapRolePolicyEvidence) *WorkspaceProvisionStore {
	result := NewWorkspaceProvisionStore(store, binding, manifest, rolePolicy...)
	result.participant = participant
	return result
}

func NewWorkspaceInitializationStore(store *database.RuntimeStore, binding identitysdk.BootstrapBinding, manifest manifestmodel.ManifestSchema, rolePolicy ...WorkspaceBootstrapRolePolicyEvidence) *WorkspaceProvisionStore {
	return &WorkspaceProvisionStore{runtime: store, bootstrap: binding, manifest: manifest, rolePolicy: firstWorkspaceBootstrapRolePolicy(rolePolicy)}
}

func NewWorkspaceInitializationStoreWithParticipant(store *database.RuntimeStore, binding identitysdk.BootstrapBinding, manifest manifestmodel.ManifestSchema, participant runtimeext.WorkspaceBootstrapParticipant, rolePolicy ...WorkspaceBootstrapRolePolicyEvidence) *WorkspaceProvisionStore {
	result := NewWorkspaceInitializationStore(store, binding, manifest, rolePolicy...)
	result.participant = participant
	return result
}

func NewWorkspaceProvisionStoreWithFailureInjector(store *database.RuntimeStore, binding identitysdk.Binding, manifest manifestmodel.ManifestSchema, participant runtimeext.WorkspaceBootstrapParticipant, failures FailureInjector, rolePolicy ...WorkspaceBootstrapRolePolicyEvidence) *WorkspaceProvisionStore {
	result := NewWorkspaceProvisionStoreWithParticipant(store, binding, manifest, participant, rolePolicy...)
	result.failures = failures
	return result
}

func firstWorkspaceBootstrapRolePolicy(policies []WorkspaceBootstrapRolePolicyEvidence) WorkspaceBootstrapRolePolicyEvidence {
	if len(policies) == 0 {
		return WorkspaceBootstrapRolePolicyEvidence{}
	}
	return policies[0]
}

func (store *WorkspaceProvisionStore) Provision(ctx context.Context, request workspaceprovisionmodel.Request) (workspaceprovisionmodel.Result, error) {
	return store.provision(ctx, request, false)
}

func (store *WorkspaceProvisionStore) Initialize(ctx context.Context, request workspaceprovisionmodel.Request) (workspaceprovisionmodel.Result, error) {
	return store.provision(ctx, request, true)
}

func (store *WorkspaceProvisionStore) provision(ctx context.Context, request workspaceprovisionmodel.Request, initialize bool) (result workspaceprovisionmodel.Result, err error) {
	request = workspaceprovisionvalidation.NormalizeRequest(request)
	if store == nil || store.runtime == nil || store.bootstrap == nil {
		return workspaceprovisionmodel.Result{}, workspaceprovisionmodel.ErrIdentityUnavailable
	}
	if err := workspaceprovisionvalidation.ValidateRequest(request); err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	if err := store.validateApplicationBootstrapRequest(request.ApplicationBootstrap); err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	configuration, err := json.Marshal(request.CommercialConfiguration)
	if err != nil {
		return workspaceprovisionmodel.Result{}, workspaceprovisionmodel.ErrInvalid
	}
	applicationInput, err := json.Marshal(request.ApplicationBootstrap)
	if err != nil {
		return workspaceprovisionmodel.Result{}, workspaceprovisionmodel.ErrInvalid
	}
	fingerprint := requestFingerprint(request, configuration, applicationInput)
	if replay, found, replayErr := store.receipt(ctx, request.RequestID, fingerprint); found || replayErr != nil {
		if replayErr != nil {
			return workspaceprovisionmodel.Result{}, replayErr
		}
		replay.Replayed = true
		replay.CredentialDelivery = workspaceprovisionmodel.CredentialUnavailableResetRequired
		return replay, nil
	}

	_, initialized, err := LoadInstallation(ctx, store.runtime)
	if err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	if initialize && initialized {
		return workspaceprovisionmodel.Result{}, workspaceprovisionmodel.ErrAlreadyInitialized
	}
	if !initialize && !initialized {
		return workspaceprovisionmodel.Result{}, workspaceprovisionmodel.ErrInitializationRequired
	}

	workspaceID, err := randomID("workspace")
	if err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	companyID, err := randomID("company")
	if err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	firstStoreID, err := randomID("store")
	if err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	adminUserID, err := randomID("user")
	if err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	result = workspaceprovisionmodel.Result{
		WorkspaceID: workspaceID, CompanyID: companyID, FirstStoreID: firstStoreID,
		InitialAdminUserID: adminUserID, CanonicalCode: request.WorkspaceCode,
		AdminLoginID: request.AdminLoginID, MustChangePassword: true,
		CredentialDelivery: workspaceprovisionmodel.CredentialUnavailableResetRequired,
	}

	tx, err := store.runtime.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	var identityReceipt identitysdk.WorkspaceIdentityBootstrapReceipt
	committed := false
	completionAttempted := false
	defer func() {
		if committed {
			return
		}
		_ = tx.Rollback()
		if identityReceipt.ReceiptID == "" || completionAttempted {
			return
		}
		completionAttempted = true
		completionErr := store.bootstrap.CompleteWorkspaceIdentityBootstrap(context.WithoutCancel(ctx), identitysdk.WorkspaceIdentityBootstrapCompletion{
			WorkspaceID: identityReceipt.WorkspaceID, ReceiptID: identityReceipt.ReceiptID,
			Outcome: identitysdk.WorkspaceIdentityBootstrapTransactionRolledBack,
		})
		if completionErr != nil && err == nil {
			err = completionErr
			result = workspaceprovisionmodel.Result{}
		}
	}()

	installationIdentity := ""
	if initialize {
		installationIdentity, err = store.runtime.InstallationIdentity(ctx)
		if err != nil {
			return workspaceprovisionmodel.Result{}, err
		}
	}
	if err = store.insertRuntimeWorkspace(ctx, tx, request, result, installationIdentity); err != nil {
		return store.afterFailedInsert(ctx, request, fingerprint, err)
	}

	identityReceipt, err = store.bootstrap.BootstrapWorkspaceIdentity(ctx, identitysdk.WorkspaceIdentityBootstrapRequest{
		ContractVersion: identitysdk.WorkspaceIdentityBootstrapContractVersion,
		ContractHash:    identitysdk.WorkspaceIdentityBootstrapContractHash,
		InvocationID:    request.RequestID, WorkspaceID: result.WorkspaceID,
		CompanyID: result.CompanyID, CompanyCode: request.WorkspaceCode + "-company", CompanyName: request.WorkspaceName,
		FirstStoreID: result.FirstStoreID, FirstStoreCode: request.FirstStoreCode, FirstStoreName: request.FirstStoreName,
		InitialAdminUserID: result.InitialAdminUserID, InitialAdminLoginID: request.AdminLoginID, InitialAdminName: request.AdminName,
	}, identitysdk.EmbeddedTransaction{Executor: tx, WorkspaceProvisionFailures: store.identityFailureInjector()})
	if err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	if err = validateIdentityReceipt(result, request.RequestID, store.rolePolicy, identityReceipt); err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	result.AdminLoginID = identityReceipt.InitialAdminLoginID
	if err = store.insertApplicationBootstrap(ctx, tx, request.ApplicationBootstrap, result); err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	if err = store.insertConfigurationAndReceipt(ctx, tx, request, result, identityReceipt, fingerprint); err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	if err = tx.Commit(); err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	committed = true
	completionAttempted = true
	if err = store.bootstrap.CompleteWorkspaceIdentityBootstrap(context.WithoutCancel(ctx), identitysdk.WorkspaceIdentityBootstrapCompletion{
		WorkspaceID: identityReceipt.WorkspaceID, ReceiptID: identityReceipt.ReceiptID,
		Outcome: identitysdk.WorkspaceIdentityBootstrapTransactionCommitted,
	}); err != nil {
		return result, nil
	}
	credential, err := store.bootstrap.ClaimWorkspaceIdentityBootstrapCredential(ctx, identitysdk.WorkspaceIdentityBootstrapCredentialClaim{
		WorkspaceID: identityReceipt.WorkspaceID, ReceiptID: identityReceipt.ReceiptID,
	})
	if err != nil {
		return result, nil
	}
	result.AdminLoginID = credential.LoginID
	result.InitialPassword = credential.InitialPassword
	result.MustChangePassword = credential.MustChangePassword
	result.CredentialDelivery = workspaceprovisionmodel.CredentialDelivered
	return result, nil
}

func validateIdentityReceipt(result workspaceprovisionmodel.Result, invocationID string, rolePolicy WorkspaceBootstrapRolePolicyEvidence, receipt identitysdk.WorkspaceIdentityBootstrapReceipt) error {
	if receipt.ContractVersion != identitysdk.WorkspaceIdentityBootstrapContractVersion || receipt.ContractHash != identitysdk.WorkspaceIdentityBootstrapContractHash {
		return fmt.Errorf("Identity workspace bootstrap returned an invalid contract receipt")
	}
	if receipt.InvocationID != invocationID || receipt.WorkspaceID != result.WorkspaceID || receipt.CompanyID != result.CompanyID || receipt.FirstStoreID != result.FirstStoreID || receipt.InitialAdminUserID != result.InitialAdminUserID || strings.TrimSpace(receipt.ReceiptID) == "" || strings.TrimSpace(receipt.InitialAdminLoginID) == "" {
		return fmt.Errorf("Identity workspace bootstrap returned an invalid authority receipt")
	}
	if rolePolicy.RoleCatalogSHA256 == "" || receipt.RoleCatalogSHA256 != rolePolicy.RoleCatalogSHA256 {
		return fmt.Errorf("Identity workspace bootstrap returned a role catalog digest that does not match the catalog sent by Runtime")
	}
	if rolePolicy.NavigationCatalogSHA256 == "" || receipt.NavigationCatalogSHA256 != rolePolicy.NavigationCatalogSHA256 {
		return fmt.Errorf("Identity workspace bootstrap returned a navigation catalog digest that does not match the template sent by Runtime")
	}
	if rolePolicy.InitialWorkspaceAdministratorRoleKey == "" || receipt.InitialWorkspaceAdministratorRoleKey != rolePolicy.InitialWorkspaceAdministratorRoleKey {
		return fmt.Errorf("Identity workspace bootstrap returned an initial administrator role that does not match the catalog sent by Runtime")
	}
	return nil
}

func (store *WorkspaceProvisionStore) validateApplicationBootstrapRequest(input map[string]any) error {
	if store.participant == nil {
		if len(input) == 0 {
			return nil
		}
		return workspaceprovisionmodel.ErrInvalid
	}
	descriptor := store.participant.Descriptor()
	if err := descriptor.Validate(); err != nil {
		return fmt.Errorf("workspace bootstrap participant: %w", err)
	}
	_, err := normalizeApplicationBootstrapInput(descriptor, input)
	if err != nil {
		return workspaceprovisionmodel.ErrInvalid
	}
	return nil
}

func (store *WorkspaceProvisionStore) insertApplicationBootstrap(ctx context.Context, tx *sql.Tx, input map[string]any, result workspaceprovisionmodel.Result) error {
	if store.participant == nil {
		return nil
	}
	descriptor := store.participant.Descriptor()
	normalizedInput, err := normalizeApplicationBootstrapInput(descriptor, input)
	if err != nil {
		return workspaceprovisionmodel.ErrInvalid
	}
	records, err := store.participant.BuildWorkspaceBootstrap(ctx, runtimeext.WorkspaceBootstrapContext{
		WorkspaceCode: result.CanonicalCode, CompanyOrganizationID: result.CompanyID,
		FirstStoreOrganizationID: result.FirstStoreID, InitialAdministratorUserID: result.InitialAdminUserID,
	}, normalizedInput)
	if err != nil {
		return fmt.Errorf("build application Workspace bootstrap: %w", err)
	}
	capabilities := make(map[string]runtimeext.WorkspaceBootstrapRecordCapability, len(descriptor.Records))
	for _, capability := range descriptor.Records {
		capabilities[strings.TrimSpace(capability.Key)] = capability
	}
	objects := make(map[string]definitionmodel.ObjectSchema, len(store.manifest.Objects))
	for _, object := range store.manifest.Objects {
		objects[strings.TrimSpace(object.Key)] = object
	}
	if len(records) != len(capabilities) {
		return fmt.Errorf("application Workspace bootstrap must return exactly one record per capability")
	}
	seen := map[string]bool{}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, record := range records {
		capabilityKey := strings.TrimSpace(record.CapabilityKey)
		capability, found := capabilities[capabilityKey]
		if !found || seen[capabilityKey] {
			return fmt.Errorf("application Workspace bootstrap returned an undeclared record capability")
		}
		seen[capabilityKey] = true
		object, found := objects[strings.TrimSpace(capability.ObjectKey)]
		if !found {
			return fmt.Errorf("application Workspace bootstrap object %q is unavailable", capability.ObjectKey)
		}
		allowed := map[string]bool{}
		for _, field := range capability.Fields {
			allowed[strings.TrimSpace(field)] = true
		}
		fields := map[string]definitionmodel.FieldSchema{}
		for _, field := range object.Fields {
			fields[field.Key] = field
		}
		columns := []string{"workspace_id", "id", "owner_org_id", "created_at", "updated_at"}
		values := []any{result.WorkspaceID, stableApplicationBootstrapRecordID(result.WorkspaceID, capabilityKey, object.Key), result.FirstStoreID, now, now}
		rawData := make(map[string]any, len(record.Data))
		keys := make([]string, 0, len(record.Data))
		for key := range record.Data {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			_, fieldFound := fields[key]
			if !allowed[key] || !fieldFound {
				return fmt.Errorf("application Workspace bootstrap returned an undeclared field")
			}
			rawData[key] = record.Data[key]
		}
		// Object defaults are manifest-owned static facts, not Handler-selected
		// fields. Apply them inside the provisioning transaction before the same
		// normalization and validation used for ordinary record creation.
		recordpolicy.RecordApplyFieldDefaults(object, rawData)
		keys = keys[:0]
		for key := range rawData {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		normalizedData := map[string]any{}
		for _, key := range keys {
			field, fieldFound := fields[key]
			if !fieldFound {
				return fmt.Errorf("application Workspace bootstrap defaulted an unknown field")
			}
			value, normalizeErr := recordvalidation.RecordNormalizeFieldValue(field, rawData[key])
			if normalizeErr != nil {
				return workspaceprovisionmodel.ErrInvalid
			}
			normalizedData[key] = value
			columns, values = append(columns, key), append(values, recordpersistence.RecordDatabaseFieldValue(store.runtime.RuntimeProfile(), field, value))
		}
		if err := recordvalidation.RecordValidateData(object, normalizedData, false); err != nil {
			return workspaceprovisionmodel.ErrInvalid
		}
		if err := insert(ctx, tx, query.NewInsertBuilder(store.runtime.RuntimeRenderer(), object.Key).Columns(columns...).Values(values...)); err != nil {
			return fmt.Errorf("insert application Workspace bootstrap record %q: %w", capabilityKey, err)
		}
		if err := store.inject(FailureAfterApplicationBootstrapRecord + capabilityKey); err != nil {
			return err
		}
	}
	return store.inject(FailureAfterApplicationBootstrap)
}

func normalizeApplicationBootstrapInput(descriptor runtimeext.WorkspaceBootstrapDescriptor, input map[string]any) (map[string]any, error) {
	fields := make(map[string]runtimeext.WorkspaceBootstrapInputField, len(descriptor.InputFields))
	result := make(map[string]any, len(descriptor.InputFields))
	for _, field := range descriptor.InputFields {
		key := strings.TrimSpace(field.Key)
		fields[key] = field
		if field.Default != nil {
			normalized, err := runtimeext.NormalizeWorkspaceBootstrapInputValue(field, field.Default)
			if err != nil {
				return nil, fmt.Errorf("application bootstrap input default is invalid")
			}
			result[key] = normalized
		}
	}
	for key, value := range input {
		field, found := fields[key]
		if !found {
			return nil, fmt.Errorf("invalid application bootstrap input")
		}
		normalized, err := runtimeext.NormalizeWorkspaceBootstrapInputValue(field, value)
		if err != nil {
			return nil, fmt.Errorf("invalid application bootstrap input")
		}
		result[key] = normalized
	}
	for key, field := range fields {
		value, found := result[key]
		if field.Required && (!found || value == nil) {
			return nil, fmt.Errorf("required application bootstrap input is missing")
		}
	}
	return result, nil
}

func stableApplicationBootstrapRecordID(workspaceID, capabilityKey, objectKey string) string {
	digest := sha256.Sum256([]byte("domainry-runtime/application-workspace-bootstrap/v1\x00" + workspaceID + "\x00" + capabilityKey + "\x00" + objectKey))
	return objectKey + "_" + hex.EncodeToString(digest[:12])
}

func (store *WorkspaceProvisionStore) insertRuntimeWorkspace(ctx context.Context, tx *sql.Tx, request workspaceprovisionmodel.Request, result workspaceprovisionmodel.Result, installationIdentity string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var initialIdentity any
	if installationIdentity != "" {
		initialIdentity = installationIdentity
	}
	if err := insert(ctx, tx, query.NewInsertBuilder(store.runtime.RuntimeRenderer(), "_workspaces").
		Columns("id", "canonical_code", "name", "status", "initial_installation_identity", "revision", "created_at", "updated_at").
		Values(result.WorkspaceID, result.CanonicalCode, request.WorkspaceName, "active", initialIdentity, 1, now, now)); err != nil {
		return err
	}
	return store.inject(FailureAfterWorkspace)
}

func (store *WorkspaceProvisionStore) insertConfigurationAndReceipt(ctx context.Context, tx *sql.Tx, request workspaceprovisionmodel.Request, result workspaceprovisionmodel.Result, identityReceipt identitysdk.WorkspaceIdentityBootstrapReceipt, fingerprint string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	configuration := request.CommercialConfiguration
	if err := insert(ctx, tx, query.NewInsertBuilder(store.runtime.RuntimeRenderer(), "_workspace_commercial_configuration").Columns(
		"workspace_id", "plan", "included_user_limit", "max_user_limit", "included_customer_limit", "max_customer_limit",
		"included_store_limit", "max_stores", "contract_date", "billing_day", "billing_contact_name", "billing_contact_phone",
		"billing_contact_email", "billing_contact_address", "billing_contact_notes", "revision", "created_at", "updated_at",
	).Values(
		result.WorkspaceID, configuration.Plan, configuration.IncludedUserLimit, configuration.MaxUserLimit,
		configuration.IncludedCustomerLimit, configuration.MaxCustomerLimit, configuration.IncludedStoreLimit, configuration.MaxStores,
		configuration.ContractDate, configuration.BillingDay, configuration.BillingContactName, configuration.BillingContactPhone,
		configuration.BillingContactEmail, configuration.BillingContactAddress, configuration.BillingContactNotes, 1, now, now,
	)); err != nil {
		return err
	}
	if err := store.inject(FailureAfterWorkspaceConfiguration); err != nil {
		return err
	}
	if err := insert(ctx, tx, query.NewInsertBuilder(store.runtime.RuntimeRenderer(), workspaceProvisioningReceiptTable).Columns(
		"request_id", "request_fingerprint", "workspace_id", "canonical_code", "admin_login_id", "must_change_password",
		"receipt_status", "identity_receipt_id", "identity_contract_version", "identity_contract_hash", "company_id", "first_store_id", "initial_admin_user_id", "created_at",
	).Values(
		request.RequestID, fingerprint, result.WorkspaceID, result.CanonicalCode, result.AdminLoginID, result.MustChangePassword,
		"committed", identityReceipt.ReceiptID, identityReceipt.ContractVersion, identityReceipt.ContractHash, result.CompanyID, result.FirstStoreID, result.InitialAdminUserID, now,
	)); err != nil {
		return err
	}
	return store.inject(FailureAfterReceipt)
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
	statement, arguments, err := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), workspaceProvisioningReceiptTable).Columns(
		"request_fingerprint", "receipt_status", "workspace_id", "canonical_code", "admin_login_id", "must_change_password", "company_id", "first_store_id", "initial_admin_user_id",
	).Where(query.Equal("request_id", requestID)).Build()
	if err != nil {
		return workspaceprovisionmodel.Result{}, false, err
	}
	var stored, status string
	var companyID, firstStoreID, adminUserID sql.NullString
	var result workspaceprovisionmodel.Result
	err = store.runtime.DB().QueryRowContext(ctx, statement, arguments...).Scan(
		&stored, &status, &result.WorkspaceID, &result.CanonicalCode, &result.AdminLoginID, &result.MustChangePassword,
		&companyID, &firstStoreID, &adminUserID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return workspaceprovisionmodel.Result{}, false, nil
	}
	if err != nil {
		return workspaceprovisionmodel.Result{}, false, err
	}
	if status != "committed" {
		return workspaceprovisionmodel.Result{}, true, workspaceprovisionmodel.ErrLegacyAdjudicationRequired
	}
	if stored != fingerprint {
		return workspaceprovisionmodel.Result{}, true, workspaceprovisionmodel.ErrIdempotencyConflict
	}
	result.CompanyID, result.FirstStoreID, result.InitialAdminUserID = companyID.String, firstStoreID.String, adminUserID.String
	return result, true, nil
}

func (store *WorkspaceProvisionStore) afterFailedInsert(ctx context.Context, request workspaceprovisionmodel.Request, fingerprint string, insertErr error) (workspaceprovisionmodel.Result, error) {
	if replay, found, err := store.receipt(ctx, request.RequestID, fingerprint); found || err != nil {
		if err != nil {
			return workspaceprovisionmodel.Result{}, err
		}
		replay.Replayed = true
		replay.CredentialDelivery = workspaceprovisionmodel.CredentialUnavailableResetRequired
		return replay, nil
	}
	statement, arguments, err := query.NewSelectBuilder(store.runtime.RuntimeRenderer(), "_workspaces").Columns("id").Where(query.Equal("canonical_code", request.WorkspaceCode)).Build()
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

func requestFingerprint(request workspaceprovisionmodel.Request, configuration, applicationInput []byte) string {
	payload := strings.Join([]string{request.WorkspaceCode, request.WorkspaceName, request.FirstStoreCode, request.FirstStoreName, request.AdminLoginID, request.AdminName, string(configuration), string(applicationInput)}, "\x00")
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
