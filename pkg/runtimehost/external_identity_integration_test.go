//go:build external_identity_integration

package runtimehost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	connector "github.com/domainry/domainry-connector-sdk"
	bridgeconfig "github.com/domainry/domainry-identity-bridge/config"
	bridgemodule "github.com/domainry/domainry-identity-bridge/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationmodule "github.com/domainry/domainry-integration/module"
	notificationmodule "github.com/domainry/domainry-notification/module"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	dataexchangefixture "github.com/domainry/domainry-runtime/testsupport/dataexchangefixture"
	schedulermodule "github.com/domainry/domainry-scheduler/module"
)

// Run through the public host composition and actual Runtime router. This
// opt-in test imports the source bridge from the development Go workspace.
func TestExternalIdentityRealRuntime(t *testing.T) {
	dir := t.TempDir()
	var unavailable atomic.Bool
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if r.Method != "POST" || r.URL.Path != "/passport/token/validate" || r.Header.Get("Content-Type") != "application/json" ||
			r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 {
			t.Error("Runtime did not use the configured account validation protocol")
			http.Error(w, "invalid request", 400)
			return
		}
		token := body["token"]
		w.Header().Set("Content-Type", "application/json")
		if token != "alice" && token != "bob" {
			json.NewEncoder(w).Encode(map[string]any{"errCode": 100003, "errMsg": "invalid token", "data": nil})
			return
		}
		userID := int64(9007199254740993)
		if token == "bob" {
			userID += 2
		}
		json.NewEncoder(w).Encode(map[string]any{"errCode": 0, "data": map[string]any{"valid": !unavailable.Load(), "user_id": userID, "email": token + "@example.com", "token_type": "access", "expires_at": time.Now().Add(time.Hour).Unix()}})
	}))
	defer provider.Close()
	cfg := serverTestConfig()
	cfg.DatabaseDriver = "sqlite"
	cfg.DBPath = filepath.Join(dir, "runtime.db")
	cfg.UploadDir = filepath.Join(dir, "uploads")
	cfg.ManifestPath = filepath.Join(dir, "manifest.json")
	cfg.AppLocale = "en-US"
	cfg.AuditExportTokenKey = "test-external-audit-cursor-secret"
	cfg.IntegrationSecretKey = "test-external-integration-secret"
	cfg.SkipManifestValidation = false
	manifest := manifestmodel.ManifestSchema{SchemaVersion: manifestmodel.CurrentManifestSchemaVersion, TemplateID: "personal", InitialWorkspaceAdministratorRole: "personal_owner", Version: "0.1.0", Name: "Personal", Objects: []definitionmodel.ObjectSchema{{Key: "note", Name: "Note", Fields: []definitionmodel.FieldSchema{{Key: "text", Name: "Text", Type: "text"}, {Key: "secret", Name: "Secret", Type: "text"}}}}, Roles: []manifestmodel.RoleSchema{{Key: "personal_owner", Name: "Personal owner", Audience: "user", AssignmentMode: "manual", ProvisionToWorkspaces: true, Permissions: []manifestmodel.RolePermission{{PermissionKey: "note.read", DataScope: identitysdk.DataScopeAll}, {PermissionKey: "note.create", DataScope: identitysdk.DataScopeAll}, {PermissionKey: "note.export", DataScope: identitysdk.DataScopeAll}}, FieldPermissions: []manifestmodel.RoleFieldPermission{{ObjectKey: "note", FieldKey: "text", Read: true, Write: true, Export: true}, {ObjectKey: "note", FieldKey: "secret", Read: false, Write: true, Export: false}}, ExportRules: []manifestmodel.RoleExportRule{{ObjectKey: "note", Mode: "selected_fields", Fields: []string{"text"}}}}}}
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(cfg.ManifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	bc := bridgeconfig.Config{
		Version: bridgeconfig.Version, InstallationID: "personal-test", ApplicationKey: cfg.IdentityAudience,
		Provider: bridgeconfig.Provider{Key: "accounts", Verification: bridgeconfig.Verification{
			Kind: "http_introspection", Endpoint: provider.URL + "/passport/token/validate", Method: "POST", Timeout: "3s", MaxResponseBytes: 4096,
			Credential: bridgeconfig.Credential{Location: "json", Name: "token"},
			Response: bridgeconfig.Response{SubjectID: "/data/user_id", DisplayName: "/data/email", Email: "/data/email", ExpiresAt: "/data/expires_at", ExpiryFormat: "unix_seconds",
				Checks: []bridgeconfig.ResponseCheck{{Path: "/errCode", Equals: json.RawMessage(`0`)}, {Path: "/data/valid", Equals: json.RawMessage(`true`)}, {Path: "/data/token_type", Equals: json.RawMessage(`"access"`)}}},
		}},
		PersonalWorkspace: bridgeconfig.PersonalWorkspace{Mode: "per_user", CreateOnFirstAccess: true, NameTemplate: "Personal", InitialRoleKeys: []string{"personal_owner"}, ApplicationBootstrap: map[string]any{"text": "Welcome"}},
		Browser: &bridgeconfig.Browser{DisplayName: "Accounts", LoginURL: "https://accounts.example.com/login", LogoutURL: "https://accounts.example.com/logout",
			Credential: bridgeconfig.Credential{Location: "cookie", Name: "runtime_account_token"}, AllowedOrigins: []string{"https://app.example.com"}},
	}
	raw, _ = json.Marshal(bc)
	configFile := filepath.Join(dir, "external.json")
	os.WriteFile(configFile, raw, 0600)
	store, err := bootstrap.PrepareProjectDatabase(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	factory := bridgemodule.NewFactory(configFile, bridgemodule.Options{Transport: provider.Client().Transport})
	manager, err := newProjectWorkspaceManager(t.Context(), cfg, factory, store, projectIdentityDatabaseHandle(store, cfg.DBPath, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(t.Context())
	if err := manager.Activate(t.Context(), manifest, externalPersonalBootstrap{}); err != nil {
		t.Fatal(err)
	}
	cfg = manager.Config()
	binding := manager.Binding()
	handlers := runtimeext.NewProjectExtensionRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	runtime := bootstrap.NewVerifiedProjectWithOwnerFactoriesAndDatabase(t.Context(), cfg, handlers, connectors, runtimehttp.RuntimeReleaseIdentity{}, bootstrap.RuntimeReleaseArtifactEvidence{}, binding, notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()), nil, schedulermodule.NewFactory(schedulermodule.OptionsFromEnvironment()), dataexchangefixture.NewFactory(), integrationmodule.NewFactory(), store)
	defer runtime.CloseContext(t.Context())
	// Production mounts module-owned identity routes in the process host,
	// outside the business Runtime router. Exercise the same guarded binding
	// and subsequent module rebind used by host.go.
	guard, err := newModuleHTTPRouteGuard(binding)
	if err != nil {
		t.Fatal(err)
	}
	listener := newIdentityAdapterRouter(runtimehttp.ListenerRouteGroupPublic, runtime.Routes())
	if err := listener.Bind(manager.Adapters(), guard); err != nil {
		t.Fatal(err)
	}
	if err := listener.Bind(append(manager.Adapters(), runtime.ModuleHTTPAdapters()...)); err != nil {
		t.Fatal(err)
	}
	request := func(token, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: bc.Browser.Credential.Name, Value: token})
		req.Header.Set("Origin", "https://app.example.com")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if method == "POST" {
			req.Header.Set("Idempotency-Key", token+path)
		}
		out := httptest.NewRecorder()
		listener.ServeHTTP(out, req)
		if out.Code != want {
			t.Fatalf("%s %s status=%d want=%d body=%s", method, path, out.Code, want, out.Body.String())
		}
		return out
	}
	for _, path := range []string{"/auth/external/config", "/auth/external/client.js"} {
		out := request("", "GET", path, "", 200)
		if strings.Contains(out.Body.String(), provider.URL) || strings.Contains(out.Body.String(), bc.Browser.Credential.Name) {
			t.Fatalf("public identity discovery exposes private credential configuration: %s", path)
		}
	}
	authenticator := binding.(identitysdk.PrincipalAuthenticationBinding).PrincipalAuthenticator()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Go(func() { _, err := authenticator.Authenticate(t.Context(), "alice"); errs <- err })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	alice, err := authenticator.Authenticate(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := authenticator.Authenticate(t.Context(), "bob")
	if err != nil {
		t.Fatal(err)
	}
	if alice.WorkspaceID == bob.WorkspaceID || alice.WorkspaceID == cfg.IdentityWorkspaceID {
		t.Fatal("personal workspace isolation failed")
	}
	for _, token := range []string{"alice", "bob"} {
		out := request(token, "GET", "/auth/external/session", "", 200)
		var session struct {
			WorkspaceID string `json:"workspace_id"`
			SubjectID   string `json:"subject_id"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		principal := alice
		if token == "bob" {
			principal = bob
		}
		if session.WorkspaceID != principal.WorkspaceID || session.SubjectID != principal.UserID {
			t.Fatalf("browser session is not bound to its personal Workspace: %+v", session)
		}
	}
	// Fixture records enter the Runtime-owned object table only after full startup.
	for _, p := range []identitysdk.Principal{alice, bob} {
		if _, err := store.DB().Exec(`INSERT INTO note (workspace_id,id,text,secret) VALUES (?,?,?,?)`, p.WorkspaceID, "record", p.User.Name, "hidden"); err != nil {
			t.Fatal(err)
		}
	}
	for _, token := range []string{"alice", "bob"} {
		out := request(token, "GET", "/records/note?page=1&page_size=10", "", 200)
		other := "alice"
		if token == other {
			other = "bob"
		}
		if !strings.Contains(out.Body.String(), token) || strings.Contains(out.Body.String(), other) || strings.Contains(out.Body.String(), "hidden") {
			t.Fatalf("record projection=%s", out.Body.String())
		}
		out = request(token, "POST", "/records/note/export?fields=text", "", 200)
		if !strings.Contains(out.Body.String(), token) || strings.Contains(out.Body.String(), other) {
			t.Fatal("CSV crossed workspace")
		}
		request(token, "POST", "/records/note/export?fields=secret", "", 403)
	}
	request("invalid", "GET", "/records/note", "", 401)
	request("alice", "GET", "/records/note?workspace_id="+bob.WorkspaceID, "", 403)
	unavailable.Store(true)
	request("alice", "GET", "/auth/external/session", "", 401)
	request("alice", "GET", "/records/note", "", 401)
	unavailable.Store(false)
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM _workspaces`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("workspaces=%d err=%v", count, err)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name LIKE '_identity_%'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("Identity implementation tables=%d err=%v", count, err)
	}
}

type externalPersonalBootstrap struct{}

func (externalPersonalBootstrap) Descriptor() runtimeext.WorkspaceBootstrapDescriptor {
	d := runtimeext.WorkspaceBootstrapDescriptor{Key: "personal_setup", InputType: "runtimehost.PersonalInput", ParticipantRevision: "v1", InputFields: []runtimeext.WorkspaceBootstrapInputField{{Key: "text", Type: runtimeext.WorkspaceBootstrapInputString, Required: true}}, Records: []runtimeext.WorkspaceBootstrapRecordCapability{{Key: "welcome", ObjectKey: "note", Fields: []string{"text"}}}}
	d.InputContractSHA256 = d.ComputedInputContractSHA256()
	return d
}
func (externalPersonalBootstrap) BuildWorkspaceBootstrap(_ context.Context, _ runtimeext.WorkspaceBootstrapContext, input map[string]any) ([]runtimeext.WorkspaceBootstrapRecord, error) {
	return []runtimeext.WorkspaceBootstrapRecord{{CapabilityKey: "welcome", Data: map[string]any{"text": input["text"]}}}, nil
}
