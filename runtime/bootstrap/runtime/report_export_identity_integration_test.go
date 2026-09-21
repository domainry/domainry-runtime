package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	dataexchangemodule "github.com/domainry/domainry-data-exchange/module"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/modulehttp"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalresolver "github.com/domainry/domainry-identity-sdk/authorization/principal"
	identitymodule "github.com/domainry/domainry-identity/module"
	"github.com/domainry/domainry-orm/query"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	reportexportapplication "github.com/domainry/domainry-runtime/runtime/application/report/export/application"
	"github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	reportpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/report"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type exportIdentityWorkspaceResolver struct{}

func (exportIdentityWorkspaceResolver) ResolveWorkspace(_ context.Context, id identitysdk.WorkspaceID) (identitysdk.WorkspaceID, error) {
	if id != "workspace-export" {
		return "", fmt.Errorf("unknown workspace")
	}
	return id, nil
}

// Uses the real Identity module and Runtime composition, and the real Data
// Exchange HTTP adapter, durable worker and artifact store. Only the small
// report source/audit fixture is controlled by the test.
func TestReportExportRealIdentityRecoveryRetryDownloadAndRevocation(t *testing.T) {
	ctx := t.Context()
	store, err := persistence.OpenContext(ctx, config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "exports.db"), IntegrationSecretKey: "export-identity-integration"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	handle := identitysdk.DatabaseHandle{Pool: store.DB(), Driver: "sqlite", Migrations: store, ModuleMigrations: store}
	factory := identitymodule.NewFactory(identitymodule.Options{DatabaseDriver: "sqlite"})
	bootstrap, err := factory.OpenBootstrapWithDatabase(ctx, "runtime", handle)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bootstrap.Close(context.Background()) })
	catalog := identitysdk.ProjectRoleCatalog{Application: identitysdk.ApplicationRef{ApplicationKey: "runtime"}, InitialWorkspaceAdministratorRoleKey: "member", Roles: []identitysdk.ProjectRoleDefinition{
		{Key: "member", Name: "Member", Audience: "any", AssignmentMode: "manual", ProvisionToWorkspaces: true, Permissions: []identitysdk.ProjectRolePermission{{PermissionKey: dataexchange.ActionDataExchangeJobGet, DataScope: "owner"}, {PermissionKey: dataexchange.ActionDataExchangeJobDownload, DataScope: "owner"}, {PermissionKey: "customer.read", DataScope: "owner"}, {PermissionKey: "customer.export", DataScope: "owner"}}},
		{Key: "member_onboarding", Name: "Onboarding", Audience: "any", AssignmentMode: "manual", ProvisionToWorkspaces: true, Permissions: []identitysdk.ProjectRolePermission{{PermissionKey: "identity.users.list", DataScope: "owner"}}},
	}}
	if err := bootstrap.BindBootstrapProjectRoleCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	request := identitysdk.WorkspaceIdentityBootstrapRequest{ContractVersion: identitysdk.WorkspaceIdentityBootstrapContractVersion, ContractHash: identitysdk.WorkspaceIdentityBootstrapContractHash, InvocationID: "export-identity", WorkspaceID: "workspace-export", CompanyID: "company", CompanyCode: "COMPANY", CompanyName: "Company", FirstStoreID: "store", FirstStoreCode: "STORE", FirstStoreName: "Store", InitialAdminUserID: "member-user", InitialAdminLoginID: "member@example.test", InitialAdminName: "Member"}
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := bootstrap.BootstrapWorkspaceIdentity(ctx, request, identitysdk.EmbeddedTransaction{Executor: tx})
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.CompleteWorkspaceIdentityBootstrap(ctx, identitysdk.WorkspaceIdentityBootstrapCompletion{WorkspaceID: request.WorkspaceID, ReceiptID: receipt.ReceiptID, Outcome: identitysdk.WorkspaceIdentityBootstrapTransactionCommitted}); err != nil {
		t.Fatal(err)
	}
	credential, err := bootstrap.ClaimWorkspaceIdentityBootstrapCredential(ctx, identitysdk.WorkspaceIdentityBootstrapCredentialClaim{WorkspaceID: request.WorkspaceID, ReceiptID: receipt.ReceiptID})
	if err != nil {
		t.Fatal(err)
	}
	handle.WorkspaceResolver = exportIdentityWorkspaceResolver{}
	identity, err := factory.OpenWithDatabase(ctx, identitysdk.ApplicationRef{WorkspaceID: "workspace-export", ApplicationKey: "runtime"}, handle)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = identity.Close(context.Background()) })
	if err := identity.(identitysdk.BootstrapProjectRoleCatalogBinder).BindBootstrapProjectRoleCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	scoped := requestcontext.WithWorkspaceID(ctx, request.WorkspaceID)
	roles, err := identity.Projection().ListRoles(scoped, identitysdk.ProjectionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	roleID, memberRoleID := "", ""
	for _, role := range roles {
		if role.Key == "member_onboarding" {
			roleID = role.ID
		}
		if role.Key == "member" {
			memberRoleID = role.ID
		}
	}
	if roleID == "" || memberRoleID == "" {
		t.Fatal("member or onboarding role missing")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	statement, args, err := query.NewWorkspaceInsertBuilder(store.RuntimeRenderer(), "_identity_user_role_assignments", request.WorkspaceID).Columns("id", "user_id", "role_id", "source", "status", "created_at", "updated_at").Values("onboarding-assignment", request.InitialAdminUserID, roleID, "manual", "active", now, now).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, statement, args...); err != nil {
		t.Fatal(err)
	}
	permissionDefinitions := []identitysdk.PermissionDefinition{}
	for _, permission := range catalog.Roles[0].Permissions {
		separator := strings.LastIndexByte(permission.PermissionKey, '.')
		permissionDefinitions = append(permissionDefinitions, identitysdk.PermissionDefinition{PermissionKey: permission.PermissionKey, ResourceKey: permission.PermissionKey[:separator], OperationKey: permission.PermissionKey[separator+1:], Label: permission.PermissionKey, Category: "Exports", SourceKind: "object_action"})
	}
	permissionRequest, err := identitysdk.NewPermissionReconcileRequest(identitysdk.ApplicationRef{WorkspaceID: "workspace-export", ApplicationKey: "runtime"}, "application:runtime", "", permissionDefinitions)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := identity.Permissions().Reconcile(scoped, permissionRequest); err != nil {
		t.Fatal(err)
	}
	login := func() (context.Context, principalmodel.Principal) {
		session, err := identity.Authentication().LoginWithPassword(ctx, identitysdk.PasswordLoginRequest{WorkspaceID: "workspace-export", Login: credential.LoginID, Password: credential.InitialPassword})
		if err != nil {
			t.Fatal(err)
		}
		resolver, err := principalresolver.NewResolver(identity, principalresolver.Options{})
		if err != nil {
			t.Fatal(err)
		}
		principal, err := resolver.Authenticate(ctx, session.AccessToken)
		if err != nil {
			t.Fatal(err)
		}
		return identitysdk.WithRequestIdentity(ctx, identitysdk.RequestIdentity{Principal: principal, AccessToken: session.AccessToken}), principalmodel.NewPrincipalFromIdentity(principal, "")
	}
	requestContext, principal := login()
	if len(principal.Roles) != 2 || requestcontext.WorkspaceID(requestContext) != "" {
		t.Fatal("fixture must exercise multi-role recovery without a Foundation workspace")
	}
	resolutionProviders := recordapplication.NewDataExchangeProviders(nil)
	composition.NewRuntimeServices(ctx, composition.RuntimeServicesConfig{Dependencies: composition.RuntimeServicesDependencies{IdentityPrincipals: identity.Principals(), DataExchangeProviders: resolutionProviders}})
	recovered := resolutionProviders.ResolvePrincipal(requestContext, dataexchange.Scope{WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID, RoleKey: principal.RoleKey})
	if !recovered.Known || recovered.AuthorizationRevision != principal.AuthorizationRevision || recovered.RoleKey != principal.RoleKey {
		t.Fatalf("recovery differs: HTTP=%+v recovered=%+v", principal, recovered)
	}
	providers := recordapplication.NewDataExchangeProviders(func(ctx context.Context, actorID, roleKey string) principalmodel.Principal {
		return resolutionProviders.ResolvePrincipal(ctx, dataexchange.Scope{WorkspaceID: requestcontext.WorkspaceID(ctx), ActorID: actorID, RoleKey: roleKey})
	})
	binding, err := openDataExchangeBinding(ctx, dataexchangemodule.NewFactory(dataexchangemodule.Options{}), dataexchange.ApplicationRef{ApplicationID: "export-test", RuntimeID: "runtime-test"}, dataExchangeModuleHost{store: store, providers: providers})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(context.Background()) })
	control := realBindingReportControl()
	definition := reportmodel.ReportSchema{Key: "revenue", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{SQL: "SELECT c.id AS id FROM customer c ORDER BY c.id LIMIT 2000", SourceObjects: []string{"customer"}, ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}}}}
	records := &realBindingReportRecords{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "approved"}}}
	owner := &realBindingReportExports{definition: reportmodel.ReportExportDefinition{Report: definition, Control: control}}
	service := reportexportapplication.NewReportExportApplicationService(reportexportapplication.ReportExportApplicationDependencies{Records: records, Audit: realBindingReportAudit{}, DataExchange: binding, DataExchangeProviders: providers, PrepareReceipts: reportpersistence.NewReportExportPrepareReceiptStore(store)})
	if err := service.BindReportExports(owner); err != nil {
		t.Fatal(err)
	}
	prepare := reportmodel.ReportExportPrepareRequest{ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1", IdempotencyKey: "initial", Scope: reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "real identity export", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}}}
	first, err := service.PrepareResolvedExport(requestContext, prepare, definition, control, principal)
	if err != nil {
		t.Fatal(err)
	}
	owner.sourceVersion = "changed-after-first-prepare"
	frozen, err := binding.Job(requestContext, dataexchange.JobRequest{Scope: dataexchange.Scope{WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID}, JobID: first.ID, Provider: "reports", Operation: "export"})
	if err != nil {
		t.Fatal(err)
	}
	provider, found := providers.ExportProvider("reports")
	planner, canPlan := provider.(interface {
		PlanExport(context.Context, dataexchange.ExportPlanRequest) (dataexchange.ExportPlan, error)
	})
	if !found || !canPlan {
		t.Fatal("Report provider must plan frozen exports")
	}
	_, err = planner.PlanExport(requestContext, dataexchange.ExportPlanRequest{Scope: dataexchange.Scope{WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID, RoleKey: principal.RoleKey}, ObjectKey: frozen.ObjectKey, ReferenceID: frozen.ReferenceID, Options: frozen.Options})
	var sourceError *apperror.AppError
	if !errors.As(err, &sourceError) || sourceError.Code != "backend.report.export_source_changed" {
		t.Fatalf("changed source must reject the frozen plan: %v", err)
	}
	done := binding.Start(ctx, dataexchange.WorkerConfig{Enabled: true, PollInterval: time.Millisecond, BatchSize: 2, LeaseTTL: time.Second})
	t.Cleanup(func() { _ = binding.Close(context.Background()); <-done })
	var failed dataexchange.Job
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		failed, err = binding.Job(requestContext, dataexchange.JobRequest{Scope: dataexchange.Scope{WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID}, JobID: first.ID, Provider: "reports", Operation: "export"})
		if err != nil {
			bundle, _ := json.Marshal(principal.AccessBundle)
			t.Fatalf("job access: %v bundle=%s", err, bundle)
		}
		if failed.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Data Exchange stores a sanitized code for worker failures; the provider's
	// precise source mismatch was asserted above against the same frozen job.
	if failed.Status != "failed" || failed.ArtifactID != "" || failed.ErrorCode != "processing_failed" {
		t.Fatalf("expected terminal frozen-source failure: status=%s artifact=%s code=%s", failed.Status, failed.ArtifactID, failed.ErrorCode)
	}
	adapters := binding.(modulehttp.Provider).HTTPAdapters()
	if len(adapters) != 1 {
		t.Fatal("missing HTTP adapter")
	}
	get := func(callContext context.Context, path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		adapters[0].Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil).WithContext(callContext))
		return response
	}
	if response := get(requestContext, "/data-exchange/jobs/"+first.ID); response.Code != 200 {
		t.Fatalf("own query without Foundation scope: %d %s", response.Code, response.Body.String())
	}
	prepare.IdempotencyKey, prepare.RetryOfJobID = "new-attempt", first.ID
	second, err := service.PrepareResolvedExport(requestContext, prepare, definition, control, principal)
	if err != nil || second.ID == first.ID {
		t.Fatalf("retry=%+v err=%v", second, err)
	}
	completed := waitRealBindingReportJob(t, requestContext, binding, principal, second.ID)
	if completed.ArtifactID == "" || completed.Checkpoint != 1 {
		t.Fatalf("artifact not completed: %+v", completed)
	}
	if response := get(requestContext, "/data-exchange/jobs/"+second.ID+"/download"); response.Code != 200 || !strings.Contains(response.Body.String(), "customer-1") {
		t.Fatalf("real CSV download: %d %s", response.Code, response.Body.String())
	}
	old, err := binding.Job(requestContext, dataexchange.JobRequest{Scope: dataexchange.Scope{WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID}, JobID: first.ID, Provider: "reports", Operation: "export"})
	if err != nil || !reflect.DeepEqual(old, failed) {
		t.Fatal("retry rewrote original failed job")
	}
	statement, args, err = query.NewWorkspaceUpdateBuilder(store.RuntimeRenderer(), "_identity_user_role_assignments", request.WorkspaceID).Set("status", "revoked").Where(query.Equal("id", "onboarding-assignment")).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, statement, args...); err != nil {
		t.Fatal(err)
	}
	changedContext, changed := login()
	if changed.AuthorizationRevision == principal.AuthorizationRevision {
		t.Fatal("revocation did not change authority")
	}
	if response := get(changedContext, "/data-exchange/jobs/"+second.ID+"/download"); response.Code == 200 {
		t.Fatal("revoked frozen scope still downloads")
	}
	statement, args, err = query.NewWorkspaceUpdateBuilder(store.RuntimeRenderer(), "_identity_user_role_assignments", request.WorkspaceID).Set("status", "revoked").Where(query.And(query.Equal("user_id", request.InitialAdminUserID), query.Equal("role_id", memberRoleID), query.Equal("status", "active"))).Build()
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.DB().ExecContext(ctx, statement, args...)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := updated.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("primary member assignment not revoked: count=%d err=%v", count, err)
	}
	if recovered := providers.ResolvePrincipal(requestContext, dataexchange.Scope{WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID, RoleKey: principal.RoleKey}); recovered.Known {
		t.Fatal("cached authenticated principal restored a revoked primary role")
	}
	if response := get(requestContext, "/data-exchange/jobs/"+second.ID+"/download"); response.Code != http.StatusForbidden {
		t.Fatalf("revoked primary role downloaded through stale authenticated context: %d %s", response.Code, response.Body.String())
	}
}
