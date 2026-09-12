package integrationtest

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agent "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/businessrpc"
	agentmodule "github.com/domainry/domainry-agent/module"
	agentweb "github.com/domainry/domainry-agent/web"
	"github.com/domainry/domainry-agent/webhost"
	identity "github.com/domainry/domainry-identity-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
)

// The model chooses deterministic tool calls, but every identity lookup,
// business result, confirmation, receipt and database is an actual owner.
type businessProductServiceModel struct{ businessWebModel }

func businessServiceManifest(m map[string]any) {
	// Identity administration belongs to its service. The deployment operator
	// assigns test business roles there; Runtime does not declare Identity's
	// administrative permission as one of its own generated permissions.
	for _, value := range m["roles"].([]any) {
		role := value.(map[string]any)
		grants := []any{}
		for _, value := range role["permissions"].([]any) {
			if !strings.HasPrefix(value.(map[string]any)["permission_key"].(string), "identity.") {
				grants = append(grants, value)
			}
		}
		role["permissions"] = grants
	}
}

func (businessProductServiceModel) ConversationModelIdentity() agent.ConversationModelIdentity {
	return agent.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "business-product-service", Fingerprint: "business-product-service-v1"}
}
func (businessProductServiceModel) StreamConversationStep(ctx context.Context, in agent.ConversationStepRequest, emit func(agent.ConversationModelEvent) error) (agent.ConversationStepResult, error) {
	for _, message := range in.Messages {
		if message.Role == "user" && strings.HasPrefix(message.Content, "验收：") {
			return (businessActionWebModel{}).StreamConversationStep(ctx, in, emit)
		}
	}
	return (businessWebModel{}).StreamConversationStep(ctx, in, emit)
}

func TestConversationBusinessProductWithSharedIdentityService(t *testing.T) {
	if os.Getenv("RUNTIME_BUSINESS_SERVICE_ACCEPTANCE") != "1" {
		t.Skip("set RUNTIME_BUSINESS_SERVICE_ACCEPTANCE=1 to build and start the actual Identity process")
	}
	t.Setenv("RUNTIME_BUSINESS_LIVE", "")
	f := prepareBusinessWebFixtureWithModel(t, businessProductServiceModel{}, businessRPCManifest, businessReportManifest, businessServiceManifest)
	scope := identity.ApplicationRef{WorkspaceID: identity.WorkspaceID(f.cfg.IdentityWorkspaceID), ApplicationKey: identity.ApplicationKey(f.cfg.IdentityAudience)}
	id := newBusinessIdentityProcess(t, scope, f.cfg.ManifestPath)
	f.identityFactory = id.factory()
	target, err := url.Parse(id.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	f.open()
	t.Cleanup(f.close)
	operator, err := f.identity.Authentication().LoginWithPassword(t.Context(), identity.PasswordLoginRequest{WorkspaceID: scope.WorkspaceID, ApplicationKey: scope.ApplicationKey, Login: "organization_administrator@example.com", Password: "Business-Browser-Initial!2026"})
	if err != nil {
		t.Fatal("Identity deployment operator login", err)
	}
	operator, err = f.identity.Credentials().ChangePassword(t.Context(), identity.ChangePasswordRequest{AccessToken: operator.AccessToken, CurrentPassword: "Business-Browser-Initial!2026", NewPassword: "Business-Service-Operator!2026", IdempotencyKey: "service-operator-password"})
	if err != nil {
		t.Fatal("Identity deployment operator password", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	f.identityHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+operator.AccessToken)
		proxy.ServeHTTP(w, r)
	})
	provisionBusinessServiceRoles(t, f)
	if _, err := os.Stat(f.identityPath); !os.IsNotExist(err) {
		t.Fatal("remote fixture created a local Identity database", err)
	}
	var slot atomic.Pointer[businessHandlerSlot]
	var calls atomic.Int32
	bindService := func() {
		h, err := bootstrap.ConversationBusinessHandler(f.runtime, "f05-isolated-business-service-credential")
		if err != nil {
			t.Fatal(err)
		}
		slot.Store(&businessHandlerSlot{handler: h})
	}
	bindService()
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Cookies()) != 0 || r.Header.Get("Authorization") != "Bearer f05-isolated-business-service-credential" {
			t.Error("business request violated fixed service credential boundary")
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		calls.Add(1)
		slot.Load().handler.ServeHTTP(w, r)
	}))
	defer service.Close()
	productPath := filepath.Join(t.TempDir(), "product.db")
	var product *webhost.Host
	var productIdentity identity.Binding
	var client *businessrpc.Client
	productHandler := func(origin string, files fs.FS) (http.Handler, error) {
		adapters, err := product.ToolSettingsAdapters()
		if err != nil {
			return nil, err
		}
		return agentweb.NewHandler(agentweb.Options{Identity: product.Identity, Agent: product.Agent, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, ApplicationKey: f.cfg.IdentityAudience, Origin: origin, Files: files, ModuleAdapters: adapters, ApplicationRoutes: product.ToolSettingsSetupRoutes()})
	}
	closeProduct := func() {
		if product != nil {
			if err := product.Close(context.Background()); err != nil {
				t.Error(err)
			}
			product = nil
		}
		if productIdentity != nil {
			if err := productIdentity.Close(context.Background()); err != nil {
				t.Error(err)
			}
			productIdentity = nil
		}
	}
	t.Cleanup(closeProduct)
	openProduct := func() {
		var err error
		productIdentity, err = id.factory().Open(t.Context(), scope)
		if err != nil {
			t.Fatal(err)
		}
		source, err := bootstrap.ConversationBusinessSource(f.runtime)
		if err != nil {
			t.Fatal(err)
		}
		client, err = businessrpc.Open(t.Context(), businessrpc.ClientOptions{
			BaseURL: service.URL, Token: "f05-isolated-business-service-credential", ExpectedSourceIdentity: source.BusinessSourceIdentity(), ExpectedContractSHA256: businessrpc.ContractSHA256(),
			Scope: businessrpc.Scope{RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, ApplicationKey: f.cfg.IdentityAudience, IdentityIssuer: productIdentity.Descriptor().Issuer},
		})
		if err != nil {
			t.Fatal(err)
		}
		product, err = webhost.Open(t.Context(), webhost.Options{
			DatabasePath: productPath, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, ApplicationKey: f.cfg.IdentityAudience, IdentityBinding: productIdentity,
			Agent: agentmodule.Options{ConversationProvider: businessProductServiceModel{}, ConversationOptions: agentmodule.ConversationOptions{Business: client, Poll: 5 * time.Millisecond}},
		})
		if err != nil {
			t.Fatal("open actual independent product", err)
		}
		f.handler, err = productHandler(businessWebOrigin, f.files)
		if err != nil {
			t.Fatal(err)
		}
	}
	openProduct()
	b := &businessBrowser{t: t, f: f, cookies: map[string]*http.Cookie{}}
	b.call("POST", "/auth/login", map[string]any{"login": "admin@example.com", "password": "Business-Browser-Initial!2026"}, 200)
	b.call("POST", "/auth/password/change", map[string]any{"current_password": "Business-Browser-Initial!2026", "new_password": businessWebPassword}, 200)
	b.assign("headquarters_admin")
	b.session()
	authority := agent.ConversationAuthority{Known: true, RuntimeID: f.cfg.RuntimeInstanceID, WorkspaceID: f.cfg.IdentityWorkspaceID, UserID: "admin"}
	definitions := append(agent.BusinessConversationTools(), agent.BusinessRelationConversationTools()...)
	definitions = append(definitions, agent.BusinessActionConversationTools()...)
	definitions = append(definitions, agent.BusinessWorkflowConversationTools()...)
	for _, definition := range definitions {
		request := agent.ConversationToolRequest{Authority: authority, Definition: definition}
		local, localErr := product.AuthorizeConversationTool(t.Context(), request)
		remote, remoteErr := client.AuthorizeConversationTool(t.Context(), request)
		if localErr != nil || remoteErr != nil || !local.Granted || !remote.Granted {
			t.Fatal("business tool unavailable", definition.Key, localErr, remoteErr)
		}
	}
	_, actionErr := client.AuthorizeBusinessAction(t.Context(), agent.ConversationBusinessAction{}, authority)
	_, workflowErr := client.AuthorizeWorkflowStart(t.Context(), agent.ConversationWorkflowStart{}, authority)
	if actionErr != nil || workflowErr != nil {
		t.Fatal("empty action/workflow preflight", actionErr, workflowErr)
	}
	if _, err := product.ToolSettings.Settings().ListToolSettings(t.Context(), authority); err != nil {
		t.Fatal("actual product tool catalog", err)
	}
	var conversation agent.Conversation
	if err := json.Unmarshal(b.call("POST", "/agent/conversations", map[string]any{"client_id": "service-business-query", "title": "共享身份的业务查询"}, 200).Body.Bytes(), &conversation); err != nil {
		t.Fatal(err)
	}
	run := b.run(conversation.ID)
	if len(run.Steps) < 6 {
		t.Fatal("missing actual catalog, three pages and detail", len(run.Steps))
	}
	created := func() int {
		page, err := client.QueryBusinessRecords(t.Context(), agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name"}, Filters: []agent.ConversationBusinessFilter{{Field: "name", Operator: "eq", Value: json.RawMessage(`"Agent Created"`)}}}, authority)
		if err != nil {
			t.Fatal(err)
		}
		return len(page.Items)
	}
	c, pending := b.startAction("创建")
	if created() != 0 {
		t.Fatal("business write happened before product confirmation")
	}
	completed := b.approveAction(c, pending)
	if created() != 1 || !strings.Contains(completed.Steps[len(completed.Steps)-1].Text, "Agent Created") {
		t.Fatal("confirmed action or duplicate confirmation lost exact once result")
	}
	history := func(hidden bool) {
		var page agent.ConversationMessagePage
		if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+conversation.ID+"/messages", nil, 200).Body.Bytes(), &page); err != nil || len(page.Items) != 2 {
			t.Fatal(page, err)
		}
		message := page.Items[1]
		if hidden {
			if message.AccessError == "" || strings.Contains(message.Content, "Acme") {
				t.Fatal("old answer bypassed current business permissions", message)
			}
		} else if message.AccessError != "" || !strings.Contains(message.Content, "Acme") {
			t.Fatal("actual business answer missing", message)
		}
	}
	history(false)
	b.assign("business_field_restricted")
	b.session()
	history(true)
	b.assign("headquarters_admin")
	b.session()
	history(false)
	b.assign("business_restricted")
	b.session()
	history(true)
	b.assign("headquarters_admin")
	b.session()
	if result, err := client.ReportSummary(t.Context(), reportmodel.ReportSummaryRequest{ReportKey: "customer_names", Page: reportmodel.ReportPageRequest{PageSize: 1}}, authority); err != nil || len(result.Rows) != 1 {
		t.Fatal("shared Identity Report owner", result, err)
	}
	// Keep original product cookies, databases and source identity. The actual
	// Identity OS process is stopped and started, and all Runtime/product
	// bindings are destroyed and reopened. No in-memory fake owns persistence.
	closeProduct()
	f.close()
	id.stop()
	id.start()
	f.open()
	bindService()
	openProduct()
	b.session()
	history(false)
	if created() != 1 {
		t.Fatal("business effect did not survive all owner restarts")
	}
	// Removing the real service never falls back to product-local records or
	// releases previously persisted content without source authorization.
	slot.Store(&businessHandlerSlot{handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "Unavailable", 503) })})
	history(true)
	bindService()
	history(false)
	if os.Getenv("RUNTIME_BUSINESS_SERVICE_BROWSER") == "1" {
		runBusinessServiceBrowser(t, businessServiceBrowserOptions{
			ConversationID: conversation.ID,
			Handler:        productHandler,
			AssignRole:     b.assign,
			Restart: func() {
				closeProduct()
				f.close()
				id.stop()
				id.start()
				f.open()
				bindService()
				openProduct()
			},
			ServiceAvailable: func(available bool) {
				if available {
					bindService()
				} else {
					slot.Store(&businessHandlerSlot{handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "Unavailable", 503) })})
				}
			},
			Records: func() ([]agent.ConversationBusinessRecord, error) {
				page, err := client.QueryBusinessRecords(t.Context(), agent.ConversationBusinessQuery{ObjectKey: "customer", Fields: []string{"name"}, PageSize: 10}, authority)
				return page.Items, err
			},
		})
	}
	t.Logf("actual Identity OS process, independent Agent web host and Runtime business/Report HTTP: %d business and %d Identity SDK requests; product login, catalog/pages/detail, explicit confirmation, duplicate response, field/read revocation, preserved-source history, unavailable owner and complete process/binding/database restart; isolated Identity application quota 12000/minute", calls.Load(), id.requests.Load())
}
