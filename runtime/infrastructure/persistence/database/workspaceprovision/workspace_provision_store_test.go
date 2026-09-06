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

type identityBootstrapV2Probe struct {
	db            *sql.DB
	request       identitysdk.WorkspaceIdentityBootstrapV2Request
	receipt       identitysdk.WorkspaceIdentityBootstrapV2Receipt
	completions   []identitysdk.WorkspaceIdentityBootstrapCompletion
	credential    identitysdk.WorkspaceIdentityBootstrapOneTimeCredential
	fixtureCalls  int
	pending       bool
	claimed       bool
	completionErr error
	claimErr      error
}

func (*identityBootstrapV2Probe) BindBootstrapProjectRoleCatalog(context.Context, identitysdk.ProjectRoleCatalog) error {
	return nil
}

func (probe *identityBootstrapV2Probe) BootstrapWorkspaceIdentityV2(ctx context.Context, request identitysdk.WorkspaceIdentityBootstrapV2Request, transaction identitysdk.EmbeddedTransaction) (identitysdk.WorkspaceIdentityBootstrapV2Receipt, error) {
	tx, ok := transaction.Executor.(*sql.Tx)
	if !ok {
		return identitysdk.WorkspaceIdentityBootstrapV2Receipt{}, errors.New("host transaction unavailable")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO "identity_provision_probe" ("workspace_id", "admin_login_id") VALUES (?, ?)`, request.WorkspaceID, request.InitialAdminLoginID); err != nil {
		return identitysdk.WorkspaceIdentityBootstrapV2Receipt{}, err
	}
	probe.request = request
	probe.pending = true
	probe.receipt = identitysdk.WorkspaceIdentityBootstrapV2Receipt{
		ContractVersion: request.ContractVersion, ContractHash: request.ContractHash,
		ReceiptID: "identity-receipt-" + request.InvocationID, InvocationID: request.InvocationID,
		WorkspaceID: request.WorkspaceID, CompanyID: request.CompanyID, FirstStoreID: request.FirstStoreID,
		InitialAdminUserID: request.InitialAdminUserID, InitialAdminLoginID: request.InitialAdminLoginID,
	}
	return probe.receipt, nil
}

func (probe *identityBootstrapV2Probe) CompleteWorkspaceIdentityBootstrapV2(ctx context.Context, completion identitysdk.WorkspaceIdentityBootstrapCompletion) error {
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

func (probe *identityBootstrapV2Probe) ClaimWorkspaceIdentityBootstrapCredentialV2(context.Context, identitysdk.WorkspaceIdentityBootstrapCredentialClaim) (identitysdk.WorkspaceIdentityBootstrapOneTimeCredential, error) {
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
			repository := NewWorkspaceInitializationStore(store, probe, manifestmodel.ManifestSchema{})
			request := validWorkspaceRequest("postcommit-" + test.name)
			result, err := repository.InitializeV2(t.Context(), request)
			if err != nil || result.WorkspaceID == "" || result.CanonicalCode != request.WorkspaceCode || result.InitialPassword != "" || result.CredentialDelivery != workspaceprovisionmodel.CredentialUnavailableResetRequired {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if count := len(probe.completions); count != 1 {
				t.Fatalf("completion calls=%d", count)
			}
			replay, err := repository.InitializeV2(t.Context(), request)
			if err != nil || !replay.Replayed || replay.InitialPassword != "" || replay.CredentialDelivery != workspaceprovisionmodel.CredentialUnavailableResetRequired || len(probe.completions) != 1 {
				t.Fatalf("replay=%+v completions=%d error=%v", replay, len(probe.completions), err)
			}
		})
	}
}

func (probe *identityBootstrapV2Probe) WorkspaceAcceptanceFixtureProvisioner() identitysdk.EmbeddedWorkspaceAcceptanceFixtureProvisioner {
	return probe
}

func (probe *identityBootstrapV2Probe) ProvisionWorkspaceAcceptanceFixtures(context.Context, identitysdk.WorkspaceAcceptanceFixtureRequest, identitysdk.EmbeddedTransaction) error {
	probe.fixtureCalls++
	return nil
}

func (*identityBootstrapV2Probe) Close(context.Context) error { return nil }

var _ identitysdk.BootstrapBinding = (*identityBootstrapV2Probe)(nil)
var _ identitysdk.EmbeddedWorkspaceAcceptanceFixtureProvisionerBinding = (*identityBootstrapV2Probe)(nil)

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

func TestWorkspaceInitializationV2CompletesThenClaimsOnceAndReplayHasNoSecret(t *testing.T) {
	store, probe := newWorkspaceProvisionTestStore(t)
	repository := NewWorkspaceInitializationStore(store, probe, manifestmodel.ManifestSchema{})
	request := validWorkspaceRequest("bootstrap-v2")
	result, err := repository.InitializeV2(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.InitialPassword != probe.credential.InitialPassword || !result.MustChangePassword || result.CredentialDelivery != workspaceprovisionmodel.CredentialDelivered || !probe.claimed {
		t.Fatalf("result=%#v claimed=%t", result, probe.claimed)
	}
	if probe.fixtureCalls != 0 {
		t.Fatalf("V3 reached legacy acceptance provisioner %d times", probe.fixtureCalls)
	}
	if probe.request.ContractVersion != "domainry-workspace-identity-bootstrap-v2" || probe.request.ContractHash != "5011287354029d67c64e1f9dedf3767234c9af8d7ec7e29886a9b4b419ccc9c8" {
		t.Fatalf("bootstrap contract=%q hash=%q", probe.request.ContractVersion, probe.request.ContractHash)
	}
	if len(probe.completions) != 1 || probe.completions[0].Outcome != identitysdk.WorkspaceIdentityBootstrapTransactionCommitted {
		t.Fatalf("completions=%#v", probe.completions)
	}

	replay, err := repository.InitializeV2(t.Context(), request)
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

func TestWorkspaceInitializationV2RollbackCompletesAndLeavesNoAuthorityRows(t *testing.T) {
	store, probe := newWorkspaceProvisionTestStore(t)
	repository := NewWorkspaceInitializationStore(store, probe, manifestmodel.ManifestSchema{})
	repository.failures = NewAcceptanceFailureInjector(FailureAfterWorkspaceConfiguration)
	result, err := repository.InitializeV2(t.Context(), validWorkspaceRequest("rollback"))
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
	if _, err := NewWorkspaceInitializationStore(store, probe, manifestmodel.ManifestSchema{}).InitializeV2(t.Context(), request); !errors.Is(err, workspaceprovisionmodel.ErrInvalid) {
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
	result, err := NewWorkspaceInitializationStoreWithParticipant(store, probe, manifest, participant).InitializeV2(t.Context(), request)
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
	if _, err := NewWorkspaceInitializationStoreWithParticipant(store, probe, manifest, participant).Provision(t.Context(), request); !errors.Is(err, workspaceprovisionmodel.ErrInvalid) {
		t.Fatalf("browser-selected bootstrap shape error=%v", err)
	}
}

func TestWorkspaceBootstrapInputRequiresParticipantAndPerRecordFailureRollsBackEverything(t *testing.T) {
	store, probe := newWorkspaceProvisionTestStore(t)
	request := validWorkspaceRequest("missing-participant")
	request.ApplicationBootstrap = map[string]any{"currency": "CNY"}
	if _, err := NewWorkspaceInitializationStore(store, probe, manifestmodel.ManifestSchema{}).InitializeV2(t.Context(), request); !errors.Is(err, workspaceprovisionmodel.ErrInvalid) {
		t.Fatalf("missing participant error=%v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE "store_configuration" ("workspace_id" TEXT NOT NULL,"id" TEXT NOT NULL,"owner_org_id" TEXT NOT NULL,"created_at" TEXT NOT NULL,"updated_at" TEXT NOT NULL,"currency" TEXT NOT NULL,PRIMARY KEY("workspace_id","id"))`); err != nil {
		t.Fatal(err)
	}
	participant := &workspaceBootstrapParticipantProbe{}
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "store_configuration", Fields: []definitionmodel.FieldSchema{{Key: "currency", Type: "text", Required: true}}}}}
	repository := NewWorkspaceInitializationStoreWithParticipant(store, probe, manifest, participant)
	repository.failures = NewAcceptanceFailureInjector(FailureAfterApplicationBootstrapRecord + "first_store_configuration")
	request.RequestID, request.WorkspaceCode = "participant-rollback", "participant-rollback"
	if _, err := repository.InitializeV2(t.Context(), request); !errors.Is(err, workspaceprovisionmodel.ErrAcceptanceFailure) {
		t.Fatalf("per-record failure=%v", err)
	}
	for _, table := range []string{"_workspaces", "_workspace_commercial_configuration", workspaceProvisioningReceiptTable, "store_configuration", "identity_provision_probe"} {
		assertRowCount(t, store, table, 0)
	}
	if len(probe.completions) != 1 || probe.completions[0].Outcome != identitysdk.WorkspaceIdentityBootstrapTransactionRolledBack {
		t.Fatalf("Identity completions=%+v", probe.completions)
	}
}

func newWorkspaceProvisionTestStore(t *testing.T) (*database.RuntimeStore, *identityBootstrapV2Probe) {
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
	probe := &identityBootstrapV2Probe{db: store.DB(), credential: identitysdk.WorkspaceIdentityBootstrapOneTimeCredential{
		LoginID: "owner@example.test", InitialPassword: "GeneratedOneTime1!", MustChangePassword: true,
	}}
	return store, probe
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
