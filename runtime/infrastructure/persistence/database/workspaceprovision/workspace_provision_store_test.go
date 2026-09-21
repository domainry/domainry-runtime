package workspaceprovision

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type identityBootstrapProbe struct {
	db            *sql.DB
	rolePolicy    WorkspaceBootstrapRolePolicyEvidence
	request       identitysdk.WorkspaceIdentityBootstrapRequest
	receipt       identitysdk.WorkspaceIdentityBootstrapReceipt
	completions   []identitysdk.WorkspaceIdentityBootstrapCompletion
	credential    identitysdk.WorkspaceIdentityBootstrapOneTimeCredential
	fixtureCalls  int
	pending       bool
	claimed       bool
	completionErr error
	claimErr      error
}

func (*identityBootstrapProbe) BindBootstrapProjectRoleCatalog(context.Context, identitysdk.ProjectRoleCatalog) error {
	return nil
}

func (*identityBootstrapProbe) BindBootstrapProjectNavigationCatalog(context.Context, identitysdk.ProjectNavigationCatalog) error {
	return nil
}

func (probe *identityBootstrapProbe) BootstrapWorkspaceIdentity(ctx context.Context, request identitysdk.WorkspaceIdentityBootstrapRequest, transaction identitysdk.EmbeddedTransaction) (identitysdk.WorkspaceIdentityBootstrapReceipt, error) {
	tx, ok := transaction.Executor.(*sql.Tx)
	if !ok {
		return identitysdk.WorkspaceIdentityBootstrapReceipt{}, errors.New("host transaction unavailable")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO "identity_provision_probe" ("workspace_id", "admin_login_id") VALUES (?, ?)`, request.WorkspaceID, request.InitialAdminLoginID); err != nil {
		return identitysdk.WorkspaceIdentityBootstrapReceipt{}, err
	}
	probe.request = request
	probe.pending = true
	probe.receipt = identitysdk.WorkspaceIdentityBootstrapReceipt{
		ContractVersion: request.ContractVersion, ContractHash: request.ContractHash,
		ReceiptID: "identity-receipt-" + request.InvocationID, InvocationID: request.InvocationID,
		WorkspaceID: request.WorkspaceID, CompanyID: request.CompanyID, FirstStoreID: request.FirstStoreID,
		InitialAdminUserID: request.InitialAdminUserID, InitialAdminLoginID: request.InitialAdminLoginID,
		RoleCatalogSHA256:                    probe.rolePolicy.RoleCatalogSHA256,
		NavigationCatalogSHA256:              probe.rolePolicy.NavigationCatalogSHA256,
		InitialWorkspaceAdministratorRoleKey: probe.rolePolicy.InitialWorkspaceAdministratorRoleKey,
	}
	return probe.receipt, nil
}

func (probe *identityBootstrapProbe) CompleteWorkspaceIdentityBootstrap(ctx context.Context, completion identitysdk.WorkspaceIdentityBootstrapCompletion) error {
	probe.completions = append(probe.completions, completion)
	if completion.Outcome == identitysdk.WorkspaceIdentityBootstrapTransactionRolledBack {
		probe.pending = false
		return nil
	}
	if probe.completionErr != nil {
		return probe.completionErr
	}
	var count int
	if err := probe.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM "identity_provision_probe" WHERE "workspace_id" = ?`, completion.WorkspaceID).Scan(&count); err != nil || count != 1 {
		return errors.New("commit was not visible before completion")
	}
	return nil
}

func (probe *identityBootstrapProbe) ClaimWorkspaceIdentityBootstrapCredential(context.Context, identitysdk.WorkspaceIdentityBootstrapCredentialClaim) (identitysdk.WorkspaceIdentityBootstrapOneTimeCredential, error) {
	if probe.claimErr != nil {
		return identitysdk.WorkspaceIdentityBootstrapOneTimeCredential{}, probe.claimErr
	}
	if len(probe.completions) == 0 || probe.completions[len(probe.completions)-1].Outcome != identitysdk.WorkspaceIdentityBootstrapTransactionCommitted {
		return identitysdk.WorkspaceIdentityBootstrapOneTimeCredential{}, errors.New("completion required")
	}
	if !probe.pending || probe.claimed {
		return identitysdk.WorkspaceIdentityBootstrapOneTimeCredential{}, errors.New("credential unavailable")
	}
	probe.claimed = true
	probe.pending = false
	return probe.credential, nil
}

func TestWorkspaceInitializationPostCommitFailuresReturnCanonicalResetRequiredResult(t *testing.T) {
	for _, test := range []struct {
		name       string
		completion error
		claim      error
	}{
		{name: "completion", completion: errors.New("completion unavailable")},
		{name: "claim", claim: errors.New("claim unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, probe := newWorkspaceProvisionTestStore(t)
			probe.completionErr, probe.claimErr = test.completion, test.claim
			repository := NewWorkspaceInitializationStore(store, probe, manifestmodel.ManifestSchema{}, probe.rolePolicy)
			request := validWorkspaceRequest("postcommit-" + test.name)
			result, err := repository.Initialize(t.Context(), request)
			if err != nil || result.WorkspaceID == "" || result.CanonicalCode != request.WorkspaceCode || result.InitialPassword != "" || result.CredentialDelivery != workspaceprovisionmodel.CredentialUnavailableResetRequired {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if count := len(probe.completions); count != 1 {
				t.Fatalf("completion calls=%d", count)
			}
			replay, err := repository.Initialize(t.Context(), request)
			if err != nil || !replay.Replayed || replay.InitialPassword != "" || replay.CredentialDelivery != workspaceprovisionmodel.CredentialUnavailableResetRequired || len(probe.completions) != 1 {
				t.Fatalf("replay=%+v completions=%d error=%v", replay, len(probe.completions), err)
			}
		})
	}
}

func (probe *identityBootstrapProbe) WorkspaceAcceptanceFixtureProvisioner() identitysdk.EmbeddedWorkspaceAcceptanceFixtureProvisioner {
	return probe
}

func (probe *identityBootstrapProbe) ProvisionWorkspaceAcceptanceFixtures(context.Context, identitysdk.WorkspaceAcceptanceFixtureRequest, identitysdk.EmbeddedTransaction) error {
	probe.fixtureCalls++
	return nil
}

func (*identityBootstrapProbe) Close(context.Context) error { return nil }

var _ identitysdk.BootstrapBinding = (*identityBootstrapProbe)(nil)
var _ identitysdk.EmbeddedWorkspaceAcceptanceFixtureProvisionerBinding = (*identityBootstrapProbe)(nil)

type failureInjectorFunc func(string) error

func (inject failureInjectorFunc) Inject(point string) error { return inject(point) }

type workspaceBootstrapParticipantProbe struct {
	context runtimeext.WorkspaceBootstrapContext
}

func (*workspaceBootstrapParticipantProbe) Descriptor() runtimeext.WorkspaceBootstrapDescriptor {
	descriptor := runtimeext.WorkspaceBootstrapDescriptor{
		Key: "store_configuration", InputType: "example.bootstrap.StoreInput",
		ParticipantRevision: "revision-1",
		InputFields:         []runtimeext.WorkspaceBootstrapInputField{{Key: "currency", Type: runtimeext.WorkspaceBootstrapInputString, Required: true}},
		Records:             []runtimeext.WorkspaceBootstrapRecordCapability{{Key: "first_store_configuration", ObjectKey: "store_configuration", Fields: []string{"currency"}}},
	}
	descriptor.InputContractSHA256 = descriptor.ComputedInputContractSHA256()
	return descriptor
}

func (probe *workspaceBootstrapParticipantProbe) BuildWorkspaceBootstrap(_ context.Context, context runtimeext.WorkspaceBootstrapContext, input map[string]any) ([]runtimeext.WorkspaceBootstrapRecord, error) {
	probe.context = context
	return []runtimeext.WorkspaceBootstrapRecord{{CapabilityKey: "first_store_configuration", Data: map[string]any{"currency": input["currency"]}}}, nil
}

func TestWorkspaceInitializationCompletesThenClaimsOnceAndReplayHasNoSecret(t *testing.T) {
	store, probe := newWorkspaceProvisionTestStore(t)
	manifest := manifestmodel.ManifestSchema{InitialWorkspaceAdministratorPassword: "domainry!123"}
	repository := NewWorkspaceInitializationStore(store, probe, manifest, probe.rolePolicy)
	request := validWorkspaceRequest("bootstrap")
	result, err := repository.Initialize(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.InitialPassword != probe.credential.InitialPassword || !result.MustChangePassword || result.CredentialDelivery != workspaceprovisionmodel.CredentialDelivered || !probe.claimed {
		t.Fatalf("result=%#v claimed=%t", result, probe.claimed)
	}
	if probe.fixtureCalls != 0 {
		t.Fatalf("Workspace bootstrap reached acceptance fixture provisioner %d times", probe.fixtureCalls)
	}
	if probe.request.ContractVersion != identitysdk.WorkspaceIdentityBootstrapContractVersion || probe.request.ContractHash != identitysdk.WorkspaceIdentityBootstrapContractHash {
		t.Fatalf("bootstrap contract=%q hash=%q", probe.request.ContractVersion, probe.request.ContractHash)
	}
	if probe.request.InitialAdminPassword != manifest.InitialWorkspaceAdministratorPassword {
		t.Fatal("bootstrap password was not sourced from the compiler-owned Runtime manifest")
	}
	if len(probe.completions) != 1 || probe.completions[0].Outcome != identitysdk.WorkspaceIdentityBootstrapTransactionCommitted {
		t.Fatalf("completions=%#v", probe.completions)
	}

	replay, err := repository.Initialize(t.Context(), request)
	if err != nil || !replay.Replayed || replay.InitialPassword != "" || replay.CredentialDelivery != workspaceprovisionmodel.CredentialUnavailableResetRequired || replay.WorkspaceID != result.WorkspaceID {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	encoded, err := json.Marshal(replay)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{result.WorkspaceID, result.CompanyID, result.FirstStoreID, result.InitialAdminUserID, "workspace_id", "owner_org_id"} {
		if forbidden != "" && strings.Contains(string(encoded), forbidden) {
			t.Fatalf("public result leaked %q: %s", forbidden, encoded)
		}
	}
	assertRowCount(t, store, "_workspaces", 1)
	assertRowCount(t, store, "_workspace_commercial_configuration", 1)
	assertRowCount(t, store, workspaceProvisioningReceiptTable, 1)
}

func TestWorkspaceBootstrapReceiptBindsRoleCatalogAndAdministratorEvidence(t *testing.T) {
	result := workspaceprovisionmodel.Result{WorkspaceID: "workspace", CompanyID: "company", FirstStoreID: "store", InitialAdminUserID: "user"}
	rolePolicy := workspaceBootstrapRolePolicyForTest(t)
	receipt := identitysdk.WorkspaceIdentityBootstrapReceipt{
		ContractVersion: identitysdk.WorkspaceIdentityBootstrapContractVersion,
		ContractHash:    identitysdk.WorkspaceIdentityBootstrapContractHash,
		ReceiptID:       "receipt", InvocationID: "invocation", WorkspaceID: result.WorkspaceID,
		CompanyID: result.CompanyID, FirstStoreID: result.FirstStoreID,
		InitialAdminUserID: result.InitialAdminUserID, InitialAdminLoginID: "admin@example.test",
		RoleCatalogSHA256:                    rolePolicy.RoleCatalogSHA256,
		NavigationCatalogSHA256:              rolePolicy.NavigationCatalogSHA256,
		InitialWorkspaceAdministratorRoleKey: rolePolicy.InitialWorkspaceAdministratorRoleKey,
	}
	if err := validateIdentityReceipt(result, receipt.InvocationID, rolePolicy, receipt); err != nil {
		t.Fatalf("valid role-policy receipt rejected: %v", err)
	}
	for name, mutate := range map[string]func(*identitysdk.WorkspaceIdentityBootstrapReceipt){
		"missing catalog digest": func(value *identitysdk.WorkspaceIdentityBootstrapReceipt) { value.RoleCatalogSHA256 = "" },
		"wrong catalog digest": func(value *identitysdk.WorkspaceIdentityBootstrapReceipt) {
			value.RoleCatalogSHA256 = strings.Repeat("b", 64)
		},
		"catalog digest whitespace": func(value *identitysdk.WorkspaceIdentityBootstrapReceipt) {
			value.RoleCatalogSHA256 = " " + value.RoleCatalogSHA256
		},
		"invalid catalog digest": func(value *identitysdk.WorkspaceIdentityBootstrapReceipt) {
			value.RoleCatalogSHA256 = strings.Repeat("z", 64)
		},
		"missing administrator": func(value *identitysdk.WorkspaceIdentityBootstrapReceipt) {
			value.InitialWorkspaceAdministratorRoleKey = ""
		},
		"wrong administrator": func(value *identitysdk.WorkspaceIdentityBootstrapReceipt) {
			value.InitialWorkspaceAdministratorRoleKey = "sales_rep"
		},
		"administrator whitespace": func(value *identitysdk.WorkspaceIdentityBootstrapReceipt) {
			value.InitialWorkspaceAdministratorRoleKey += " "
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := receipt
			mutate(&candidate)
			if err := validateIdentityReceipt(result, receipt.InvocationID, rolePolicy, candidate); err == nil {
				t.Fatalf("invalid role-policy receipt accepted: %#v", candidate)
			}
		})
	}
	if err := validateIdentityReceipt(result, receipt.InvocationID, WorkspaceBootstrapRolePolicyEvidence{}, receipt); err == nil {
		t.Fatal("receipt accepted without host-side role-policy evidence")
	}
}

func TestWorkspaceInitializationRollbackCompletesAndLeavesNoAuthorityRows(t *testing.T) {
	store, probe := newWorkspaceProvisionTestStore(t)
	repository := NewWorkspaceInitializationStore(store, probe, manifestmodel.ManifestSchema{}, probe.rolePolicy)
	repository.failures = NewAcceptanceFailureInjector(FailureAfterWorkspaceConfiguration)
	result, err := repository.Initialize(t.Context(), validWorkspaceRequest("rollback"))
	if !errors.Is(err, workspaceprovisionmodel.ErrAcceptanceFailure) || !reflect.DeepEqual(result, workspaceprovisionmodel.Result{}) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if len(probe.completions) != 1 || probe.completions[0].Outcome != identitysdk.WorkspaceIdentityBootstrapTransactionRolledBack || probe.pending || probe.claimed {
		t.Fatalf("completions=%#v pending=%t claimed=%t", probe.completions, probe.pending, probe.claimed)
	}
	for _, table := range []string{"_workspaces", "_workspace_commercial_configuration", workspaceProvisioningReceiptTable, "identity_provision_probe"} {
		assertRowCount(t, store, table, 0)
	}
}

func TestWorkspaceProvisionValidationRejectsUntypedOrInvalidCommercialLimits(t *testing.T) {
	store, probe := newWorkspaceProvisionTestStore(t)
	request := validWorkspaceRequest("invalid")
	request.CommercialConfiguration.MaxStores = 0
	if _, err := NewWorkspaceInitializationStore(store, probe, manifestmodel.ManifestSchema{}, probe.rolePolicy).Initialize(t.Context(), request); !errors.Is(err, workspaceprovisionmodel.ErrInvalid) {
		t.Fatalf("error=%v", err)
	}
}

func TestWorkspaceBootstrapParticipantCreatesFirstStoreOwnedAggregateInHostTransaction(t *testing.T) {
	store, probe := newWorkspaceProvisionTestStore(t)
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE "store_configuration" ("workspace_id" TEXT NOT NULL,"id" TEXT NOT NULL,"owner_org_id" TEXT NOT NULL,"created_at" TEXT NOT NULL,"updated_at" TEXT NOT NULL,"currency" TEXT NOT NULL,"label" TEXT NOT NULL,PRIMARY KEY("workspace_id","id"))`); err != nil {
		t.Fatal(err)
	}
	participant := &workspaceBootstrapParticipantProbe{}
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "store_configuration", Fields: []definitionmodel.FieldSchema{{Key: "currency", Type: "text", Required: true}, {Key: "label", Type: "text", Required: true, DefaultValue: "Default Store"}}}}}
	request := validWorkspaceRequest("participant")
	request.ApplicationBootstrap = map[string]any{"currency": "CNY"}
	result, err := NewWorkspaceInitializationStoreWithParticipant(store, probe, manifest, participant, probe.rolePolicy).Initialize(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if participant.context.WorkspaceCode != result.CanonicalCode || participant.context.FirstStoreOrganizationID != result.FirstStoreID || participant.context.CompanyOrganizationID != result.CompanyID || participant.context.InitialAdministratorUserID != result.InitialAdminUserID {
		t.Fatalf("participant context=%+v result=%+v", participant.context, result)
	}
	var workspaceID, ownerID, currency, label string
	if err := store.DB().QueryRowContext(t.Context(), `SELECT "workspace_id","owner_org_id","currency","label" FROM "store_configuration"`).Scan(&workspaceID, &ownerID, &currency, &label); err != nil {
		t.Fatal(err)
	}
	if workspaceID != result.WorkspaceID || ownerID != result.FirstStoreID || currency != "CNY" || label != "Default Store" {
		t.Fatalf("aggregate workspace=%q owner=%q currency=%q label=%q", workspaceID, ownerID, currency, label)
	}
	request.RequestID = "participant-unknown"
	request.WorkspaceCode = "other"
	request.ApplicationBootstrap["object_key"] = "users"
	if _, err := NewWorkspaceInitializationStoreWithParticipant(store, probe, manifest, participant, probe.rolePolicy).Provision(t.Context(), request); !errors.Is(err, workspaceprovisionmodel.ErrInvalid) {
		t.Fatalf("browser-selected bootstrap shape error=%v", err)
	}
}

func TestWorkspaceBootstrapInputRequiresParticipantAndPerRecordFailureRollsBackEverything(t *testing.T) {
	store, probe := newWorkspaceProvisionTestStore(t)
	request := validWorkspaceRequest("missing-participant")
	request.ApplicationBootstrap = map[string]any{"currency": "CNY"}
	if _, err := NewWorkspaceInitializationStore(store, probe, manifestmodel.ManifestSchema{}, probe.rolePolicy).Initialize(t.Context(), request); !errors.Is(err, workspaceprovisionmodel.ErrInvalid) {
		t.Fatalf("missing participant error=%v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE "store_configuration" ("workspace_id" TEXT NOT NULL,"id" TEXT NOT NULL,"owner_org_id" TEXT NOT NULL,"created_at" TEXT NOT NULL,"updated_at" TEXT NOT NULL,"currency" TEXT NOT NULL,PRIMARY KEY("workspace_id","id"))`); err != nil {
		t.Fatal(err)
	}
	participant := &workspaceBootstrapParticipantProbe{}
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "store_configuration", Fields: []definitionmodel.FieldSchema{{Key: "currency", Type: "text", Required: true}}}}}
	repository := NewWorkspaceInitializationStoreWithParticipant(store, probe, manifest, participant, probe.rolePolicy)
	repository.failures = NewAcceptanceFailureInjector(FailureAfterApplicationBootstrapRecord + "first_store_configuration")
	request.RequestID, request.WorkspaceCode = "participant-rollback", "participant-rollback"
	if _, err := repository.Initialize(t.Context(), request); !errors.Is(err, workspaceprovisionmodel.ErrAcceptanceFailure) {
		t.Fatalf("per-record failure=%v", err)
	}
	for _, table := range []string{"_workspaces", "_workspace_commercial_configuration", workspaceProvisioningReceiptTable, "store_configuration", "identity_provision_probe"} {
		assertRowCount(t, store, table, 0)
	}
	if len(probe.completions) != 1 || probe.completions[0].Outcome != identitysdk.WorkspaceIdentityBootstrapTransactionRolledBack {
		t.Fatalf("Identity completions=%+v", probe.completions)
	}
}

func newWorkspaceProvisionTestStore(t *testing.T) (*database.RuntimeStore, *identityBootstrapProbe) {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workspace.db"), IntegrationSecretKey: "workspace-provisioning-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseContext(t.Context()) })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE "identity_provision_probe" ("workspace_id" TEXT PRIMARY KEY, "admin_login_id" TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	probe := &identityBootstrapProbe{db: store.DB(), rolePolicy: workspaceBootstrapRolePolicyForTest(t), credential: identitysdk.WorkspaceIdentityBootstrapOneTimeCredential{
		LoginID: "owner@example.test", InitialPassword: "GeneratedOneTime1!", MustChangePassword: true,
	}}
	return store, probe
}

func workspaceBootstrapRolePolicyForTest(t *testing.T) WorkspaceBootstrapRolePolicyEvidence {
	t.Helper()
	policy, err := NewWorkspaceBootstrapRolePolicyEvidence(identitysdk.ProjectRoleCatalog{
		InitialWorkspaceAdministratorRoleKey: "crm_acceptance_admin",
		Roles: []identitysdk.ProjectRoleDefinition{{
			Key: "crm_acceptance_admin", Name: "CRM acceptance administrator",
			Audience: "user", AssignmentMode: "manual", ProvisionToWorkspaces: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func validWorkspaceRequest(requestID string) workspaceprovisionmodel.Request {
	return workspaceprovisionmodel.Request{
		RequestID: requestID, WorkspaceCode: "primary", WorkspaceName: "Primary",
		FirstStoreCode: "primary-store", FirstStoreName: "Primary Store",
		AdminLoginID: "OWNER@EXAMPLE.TEST", AdminName: "Owner",
		CommercialConfiguration: workspaceprovisionmodel.CommercialConfiguration{
			Plan: "standard", IncludedUserLimit: 1, MaxUserLimit: 100,
			IncludedCustomerLimit: 0, MaxCustomerLimit: 1000,
			IncludedStoreLimit: 1, MaxStores: 2, ContractDate: "2026-09-06", BillingDay: 1,
		},
	}
}

func assertRowCount(t *testing.T, store *database.RuntimeStore, table string, want int) {
	t.Helper()
	var count int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+store.TableIdentifier(table)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s count=%d want=%d", table, count, want)
	}
}
