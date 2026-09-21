package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentmodule "github.com/domainry/domainry-agent/module"
	connector "github.com/domainry/domainry-connector-sdk"
	dataexchangemodule "github.com/domainry/domainry-data-exchange/module"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalresolver "github.com/domainry/domainry-identity-sdk/authorization/principal"
	identitymodule "github.com/domainry/domainry-identity/module"
	integrationmodule "github.com/domainry/domainry-integration/module"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	notificationmodule "github.com/domainry/domainry-notification/module"
	"github.com/domainry/domainry-orm/query"
	reportmodule "github.com/domainry/domainry-report/module"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordstore "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	schedulermodule "github.com/domainry/domainry-scheduler/module"
)

type accountErasureIntegrationHandler struct{ descriptor runtimeext.HandlerDescriptor }

func (h accountErasureIntegrationHandler) Descriptor() runtimeext.HandlerDescriptor {
	return h.descriptor
}
func (h accountErasureIntegrationHandler) Invoke(ctx context.Context, execution runtimeext.ActionExecution, raw json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Abort     bool
		RequestID string
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	if input.RequestID != "" {
		receipt, err := runtimeext.GetAccountErasure(ctx, execution, runtimeext.AccountErasureGetRequest{RequestID: input.RequestID, ProfileID: "profile-a"})
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"request_id": receipt.RequestID, "completed": receipt.Completed})
	}
	receipt, err := runtimeext.StageAccountErasure(ctx, execution, runtimeext.AccountErasureStageRequest{
		ProfileID: "profile-a", ApprovalID: "approval-a", ExpectedIdentityVersion: 1, ExpectedBindingVersion: 1,
	})
	if err != nil {
		return nil, err
	}
	if input.Abort {
		return nil, &runtimeext.BusinessError{Code: "account_erasure.obligation_failed", Message: "business obligation failed after staging"}
	}
	return json.Marshal(map[string]any{"request_id": receipt.RequestID, "completed": receipt.Completed})
}

// Exercises the published Action capability with the actual host transaction,
// Identity binder, Lifecycle approval queue, controlled Runtime worker and
// source-owned record/identity cleanup. No delivery or erasure port is stubbed.
func TestAccountErasureActionRollbackCommitAndControlledWorker(t *testing.T) {
	t.Setenv("AUTH_JWT_SECRET", "account-erasure-integration-signing-secret")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "account-erasure-integration-data-secret")
	cfg := moduleSetTestConfig(t)
	cfg.RuntimeVersion = "account-erasure-integration"
	h := accountErasureIntegrationHandler{descriptor: runtimeRoleCapabilityDescriptor("erase_request.approve", func(d *runtimeext.HandlerDescriptor) {
		d.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceRecordOwner}
		d.ObjectCapabilities = []runtimeext.ActionObjectCapability{
			{ObjectKey: "member_profile", Operations: []string{"get_for_update"}},
			{ObjectKey: "erase_request", Operations: []string{"get_for_update"}},
		}
		d.AccountErasure = &runtimeext.AccountErasureCapability{
			Operations:       []runtimeext.AccountErasureOperation{runtimeext.AccountErasureStage, runtimeext.AccountErasureGet},
			ProfileBinding:   runtimeext.IdentityProfileBindingCapability{BindingKey: "member", ObjectKey: "member_profile"},
			RequestObjectKey: "erase_request", RequestProfileField: "profile_id", RequestRequesterField: "requested_by",
		}
	})}
	field := func(key, kind, mode string, extras map[string]any) map[string]any {
		config := map[string]any{"lifecycle_erase": mode}
		for k, v := range extras {
			config[k] = v
		}
		return map[string]any{"key": key, "name": key, "type": kind, "config": config}
	}
	profileUser := field("identity_user_id", "relation", "anonymize", map[string]any{"lifecycle_subject_identity": true})
	profileUser["validation"] = map[string]any{"target": "identity_user"}
	profileUser["unique"] = true
	requestProfile := field("profile_id", "relation", "retain", map[string]any{"lifecycle_subject_relation": true})
	requestProfile["validation"] = map[string]any{"target": "member_profile"}
	permissions := []any{}
	for _, key := range []string{"erase_request.approve", "erase_request.read", "member_profile.read"} {
		permissions = append(permissions, map[string]any{"permission_key": key, "data_scope": "all"})
	}
	manifest := map[string]any{
		"template_id": "account_erasure_integration", "version": "0.1.0", "name": "Account erasure integration", "schema_version": "2",
		"initial_workspace_administrator_role":     "operator",
		"initial_workspace_administrator_password": "domainry!123",
		"objects": []any{
			map[string]any{"key": "member_profile", "name": "Member", "ux": map[string]any{"kind": "identity_profile_extension"}, "fields": []any{profileUser, field("private_name", "text", "anonymize", nil)}},
			map[string]any{"key": "erase_request", "name": "Erasure request", "fields": []any{requestProfile, field("requested_by", "user", "anonymize", nil), field("status", "text", "retain", nil)}},
		},
		"identity_profile_extensions": []any{map[string]any{
			"contract_version": "identity-profile-extension", "min_reader_version": "identity-profile-extension-reader",
			"object_key": "member_profile", "identity_relation_field": "identity_user_id", "cardinality": "one_to_one",
			"business_identity": map[string]any{"key": "member"}, "default_visibility": "when_readable",
		}},
		"roles": []any{map[string]any{"key": "operator", "name": "Operator", "audience": "user", "assignment_mode": "manual", "provision_to_workspaces": true, "permissions": permissions}},
		"actions": []any{map[string]any{
			"key": h.descriptor.ActionKey, "object_key": "erase_request", "label": "Approve erasure", "kind": "record_operation", "audit_event": "account_erasure_approved",
			"target_organization": map[string]any{"source": "record_owner"},
			"input_type":          h.descriptor.InputType, "output_type": h.descriptor.OutputType,
			"payload_fields": []any{
				map[string]any{"key": "Abort", "name": "Abort", "type": "boolean"},
				map[string]any{"key": "RequestID", "name": "RequestID", "type": "text"},
			},
			"effect_set": map[string]any{"read": []any{
				map[string]any{"object_key": "member_profile", "operations": []string{"get_for_update"}, "fields": []string{}},
				map[string]any{"object_key": "erase_request", "operations": []string{"get_for_update"}, "fields": []string{}},
			}},
		}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ManifestPath = filepath.Join(t.TempDir(), "account-erasure.json")
	if err = os.WriteFile(cfg.ManifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := PrepareProjectDatabase(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	definition, err := prepareRuntimeManifest(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := RuntimeWorkspaceBootstrapRoleCatalog(definition.Objects, definition.Roles, "operator", cfg.IdentityAudience, h.descriptor)
	if err != nil {
		t.Fatal(err)
	}
	handle := identitysdk.DatabaseHandle{Pool: store.DB(), Driver: "sqlite", Migrations: store, ModuleMigrations: store}
	factory := identitymodule.NewFactory(identitymodule.Options{DatabaseDriver: "sqlite"})
	bootstrap, err := factory.OpenBootstrapWithDatabase(t.Context(), identitysdk.ApplicationKey(cfg.IdentityAudience), handle)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bootstrap.Close(context.Background()) })
	if err = bootstrap.BindBootstrapProjectRoleCatalog(t.Context(), catalog); err != nil {
		t.Fatal(err)
	}
	command := identitysdk.WorkspaceIdentityBootstrapRequest{
		ContractVersion: identitysdk.WorkspaceIdentityBootstrapContractVersion, ContractHash: identitysdk.WorkspaceIdentityBootstrapContractHash,
		InvocationID: "account-erasure-bootstrap", WorkspaceID: cfg.IdentityWorkspaceID, CompanyID: "company-a", CompanyCode: "COMPANY", CompanyName: "Company",
		FirstStoreID: "store-a", FirstStoreCode: "STORE", FirstStoreName: "Store", InitialAdminUserID: "operator", InitialAdminLoginID: "operator@example.test", InitialAdminName: "Operator",
		InitialAdminPassword: "domainry!123",
	}
	tx, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	bootReceipt, err := bootstrap.BootstrapWorkspaceIdentity(t.Context(), command, identitysdk.EmbeddedTransaction{Executor: tx})
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = bootstrap.CompleteWorkspaceIdentityBootstrap(t.Context(), identitysdk.WorkspaceIdentityBootstrapCompletion{
		WorkspaceID: command.WorkspaceID, ReceiptID: bootReceipt.ReceiptID, Outcome: identitysdk.WorkspaceIdentityBootstrapTransactionCommitted,
	}); err != nil {
		t.Fatal(err)
	}
	credential, err := bootstrap.ClaimWorkspaceIdentityBootstrapCredential(t.Context(), identitysdk.WorkspaceIdentityBootstrapCredentialClaim{WorkspaceID: command.WorkspaceID, ReceiptID: bootReceipt.ReceiptID})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := factory.OpenWithDatabase(t.Context(), identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience)}, handle)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = identity.Close(context.Background()) })
	handlers := runtimeext.NewProjectExtensionRegistry()
	if err = handlers.RegisterBusinessHandler(h); err != nil {
		t.Fatal(err)
	}
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	app := NewVerifiedProjectWithAllTopologyFactoriesAndDatabaseOptions(t.Context(), cfg, handlers, connectors,
		runtimehttp.RuntimeReleaseIdentity{}, deploymentapplication.RuntimeReleaseArtifactEvidence{}, identity,
		notificationmodule.NewFactory(notificationmodule.Options{}), moduleSetMonitoringModuleFactory(t), schedulermodule.NewFactory(schedulermodule.Options{}),
		dataexchangemodule.NewFactory(dataexchangemodule.Options{}), integrationmodule.NewFactory(), reportmodule.NewFactory(), store,
		ProjectStartupOptions{ProjectNavigationCatalog: identitysdk.ProjectNavigationCatalog{ContractVersion: identitysdk.ProjectNavigationContractVersion}},
		agentmodule.NewFactory(agentmodule.Options{BaseURL: "http://127.0.0.1", APIKey: "account-erasure-test", AgentID: 1}),
	)
	t.Cleanup(func() { _ = app.CloseContext(context.Background()) })
	execInsert := func(table string, columns []string, values ...any) {
		t.Helper()
		sql, args, err := query.NewWorkspaceInsertBuilder(store.RuntimeRenderer(), table, cfg.IdentityWorkspaceID).Columns(columns...).Values(values...).Build()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.DB().ExecContext(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Stable fixture rows model an already bound member. Authentication of the
	// approving operator uses the actual committed bootstrap credential.
	execInsert("_identity_users", []string{"id", "name", "email", "org_id", "status", "version", "created_at", "updated_at"}, "alice", "Alice private", "alice@private.example.test", "store-a", "active", 1, "before", "before")
	execInsert("_identity_profile_bindings", []string{"id", "binding_key", "object_key", "profile_id", "identity_user_id", "status", "version", "created_at", "updated_at"}, "binding-a", "member", "member_profile", "profile-a", "alice", "active", 1, "before", "before")
	recordObjects := map[string]bool{}
	for _, object := range definition.Objects {
		var row recordmodel.Record
		switch object.Key {
		case "member_profile":
			row = recordmodel.Record{ID: "profile-a", CreatedAt: "before", UpdatedAt: "before", OwnerOrgID: "store-a", OwnerUserID: "alice", Data: map[string]any{"identity_user_id": "alice", "private_name": "Alice private"}}
		case "erase_request":
			row = recordmodel.Record{ID: "approval-a", CreatedAt: "before", UpdatedAt: "before", OwnerOrgID: "store-a", OwnerUserID: "alice", Data: map[string]any{"profile_id": "profile-a", "requested_by": "alice", "status": "approved"}}
		default:
			continue
		}
		if err = recordstore.NewRecordStore(store).InsertRecord(t.Context(), cfg.IdentityWorkspaceID, object, row); err != nil {
			t.Fatal(err)
		}
		recordObjects[object.Key] = true
	}
	if len(recordObjects) != 2 {
		t.Fatal("fixture business roots missing")
	}
	session, err := identity.Authentication().LoginWithPassword(t.Context(), identitysdk.PasswordLoginRequest{WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience), Login: credential.LoginID, Password: credential.InitialPassword})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := principalresolver.NewResolver(identity, principalresolver.Options{})
	if err != nil {
		t.Fatal(err)
	}
	current, err := resolver.Authenticate(t.Context(), session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	ctx := identitysdk.WithRequestIdentity(requestcontext.WithWorkspaceID(t.Context(), cfg.IdentityWorkspaceID), identitysdk.RequestIdentity{Principal: current, AccessToken: session.AccessToken})
	principal := principalmodel.NewPrincipalFromIdentity(current, "")
	invoke := func(key string, abort bool) (actionmodel.ActionInvocationResult, error) {
		return app.records.Applications().Actions.Invoke(ctx, actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{ObjectKey: "erase_request", RecordID: "approval-a", ActionKey: h.descriptor.ActionKey, IdempotencyKey: key, Principal: principal, Input: map[string]any{"Abort": abort}})
	}
	if _, err = invoke("failed-obligation", true); err == nil || !strings.Contains(err.Error(), "obligation_failed") {
		t.Fatalf("did not reach staged failure: %v params=%v", err, apperror.ParamsOf(err))
	}
	var queued int
	if err = store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _lifecycle_account_erasure_approvals").Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("rollback left queue=%d err=%v", queued, err)
	}
	var status, name string
	if err = store.DB().QueryRowContext(t.Context(), "SELECT status,name FROM _identity_users WHERE workspace_id=? AND id=?", cfg.IdentityWorkspaceID, "alice").Scan(&status, &name); err != nil || status != "active" || name != "Alice private" {
		t.Fatalf("rollback identity=%s/%s err=%v", status, name, err)
	}
	result, err := invoke("successful-obligation", false)
	if err != nil || result.Status != "success" {
		t.Fatalf("stage result=%+v err=%v", result, err)
	}
	data, ok := result.Output["data"].(map[string]any)
	if !ok || data["completed"] != false {
		t.Fatalf("stage returned premature or missing completion: %+v", result.Output)
	}
	requestID, _ := data["request_id"].(string)
	if requestID == "" {
		t.Fatal("missing durable request")
	}
	queue := app.lifecycleBinding.(lifecyclesdk.AccountErasureBinding).AccountErasures()
	scope := lifecycleaccess.NewSystemScope(lifecycleaccess.SystemScopeGlobal, "inspect committed integration approval")
	ref := lifecyclecontract.AccountErasureReference{WorkspaceID: cfg.IdentityWorkspaceID, RequestID: requestID, OwnerOrgID: "store-a", BindingKey: "member", ObjectKey: "member_profile", ProfileID: "profile-a"}
	pending, err := queue.GetAccountErasure(ctx, ref, scope)
	if err != nil || pending.Status != lifecyclemodel.SubjectRequestApproved {
		t.Fatalf("uncommitted/premature erasure: %+v %v", pending, err)
	}
	if err = store.DB().QueryRowContext(t.Context(), "SELECT status,name FROM _identity_users WHERE workspace_id=? AND id=?", cfg.IdentityWorkspaceID, "alice").Scan(&status, &name); err != nil || status != "disabled" || name != "Alice private" {
		t.Fatalf("stage did not only disable: %s/%s %v", status, name, err)
	}
	app.startAccountErasureWorker(t.Context())
	deadline := time.Now().Add(10 * time.Second)
	for {
		pending, err = queue.GetAccountErasure(ctx, ref, scope)
		if err != nil {
			t.Fatal(err)
		}
		if pending.Status == lifecyclemodel.SubjectRequestSucceeded {
			break
		}
		if pending.Status == lifecyclemodel.SubjectRequestFailed || time.Now().After(deadline) {
			t.Fatalf("controlled worker did not finish: %+v", pending)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err = store.DB().QueryRowContext(t.Context(), "SELECT status,name FROM _identity_users WHERE workspace_id=? AND id=?", cfg.IdentityWorkspaceID, "alice").Scan(&status, &name); err != nil || status != "erased" || !strings.HasPrefix(name, "Erased subject ") || strings.Contains(name, "Alice") {
		t.Fatalf("worker did not erase Identity: %s/%s %v", status, name, err)
	}
	var private string
	if err = store.DB().QueryRowContext(t.Context(), "SELECT COALESCE(private_name,'') FROM member_profile WHERE workspace_id=? AND id=?", cfg.IdentityWorkspaceID, "profile-a").Scan(&private); err != nil || !strings.HasPrefix(private, "erased-") || strings.Contains(private, "Alice") {
		t.Fatalf("worker did not erase profile: %q %v", private, err)
	}
	if n, err := queue.ProcessApprovedAccountErasures(ctx, "verify-no-replay", 10, time.Now(), scope); err != nil || n != 0 {
		t.Fatalf("completed approval replayed: %d %v", n, err)
	}
	read, err := app.records.Applications().Actions.Invoke(ctx, actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{ObjectKey: "erase_request", RecordID: "approval-a", ActionKey: h.descriptor.ActionKey, IdempotencyKey: "read-erased-receipt", Principal: principal, Input: map[string]any{"RequestID": requestID}})
	if err != nil || read.Status != "success" {
		t.Fatalf("erased account receipt unavailable through formal Action: %+v %v", read, err)
	}
	data, ok = read.Output["data"].(map[string]any)
	if !ok || data["completed"] != true || data["request_id"] != requestID {
		t.Fatalf("formal Action did not return completed erasure receipt: %+v", read.Output)
	}
	t.Logf("committed Action %s completed erasure %s through controlled worker", result.InvocationID, requestID)
}
