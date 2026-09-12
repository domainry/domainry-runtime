package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityhttpapi "github.com/domainry/domainry-identity-sdk/httpapi"
	identitymodule "github.com/domainry/domainry-identity/module"
	integrationmodule "github.com/domainry/domainry-integration/module"
	notificationmodule "github.com/domainry/domainry-notification/module"
	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	dataexchangefixture "github.com/domainry/domainry-runtime/testsupport/dataexchangefixture"
	schedulermodule "github.com/domainry/domainry-scheduler/module"
)

const businessWebOrigin = "http://127.0.0.1:8093"
const businessWebPassword = "Business-Browser-Changed!2026"

type businessWebFixture struct {
	t               *testing.T
	cfg             config.Config
	identityPath    string
	options         agentmodule.Options
	files           fs.FS
	runtime         *bootstrap.Runtime
	identity        identitysdk.Binding
	identityFactory identitysdk.Factory
	identityHandler http.Handler
	handler         http.Handler
}

func newBusinessWebFixture(t *testing.T, customize ...func(map[string]any)) *businessWebFixture {
	return newBusinessWebFixtureWithModel(t, businessWebModel{}, customize...)
}

func newBusinessWebFixtureWithModel(t *testing.T, model agentsdk.ConversationModel, customize ...func(map[string]any)) *businessWebFixture {
	f := prepareBusinessWebFixtureWithModel(t, model, customize...)
	f.open()
	return f
}

// Preparation is separate so service acceptance can supply its actual remote
// Identity factory before any Runtime or local Identity database is opened.
func prepareBusinessWebFixtureWithModel(t *testing.T, model agentsdk.ConversationModel, customize ...func(map[string]any)) *businessWebFixture {
	t.Helper()
	t.Setenv("AUTH_DEFAULT_PASSWORD", "Business-Browser-Initial!2026")
	t.Setenv("AUTH_JWT_SECRET", "business-browser-signing-secret-2026-stable")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "business-browser-data-secret-2026-stable")
	t.Setenv("APP_ENV", "development")
	dir := t.TempDir()
	path := managedBusinessConversationManifest(t, dir)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, item := range manifest["objects"].([]any) {
		object := item.(map[string]any)
		if object["key"] == "customer" {
			object["fields"] = append(object["fields"].([]any), map[string]any{"key": "balance", "name": "余额", "type": "integer"})
		}
	}
	for _, item := range manifest["roles"].([]any) {
		role := item.(map[string]any)
		if role["key"] != "headquarters_admin" && role["key"] != "business_restricted" {
			continue
		}
		role["field_permissions"] = append(role["field_permissions"].([]any), map[string]any{"object_key": "customer", "field_key": "balance", "read": true, "write": false, "export": false})
		if role["key"] == "headquarters_admin" {
			encoded, _ := json.Marshal(role)
			var limited map[string]any
			_ = json.Unmarshal(encoded, &limited)
			limited["key"], limited["name"] = "business_field_restricted", "Restricted business fields"
			for _, item := range limited["field_permissions"].([]any) {
				field := item.(map[string]any)
				if field["field_key"] == "balance" {
					field["read"] = false
				}
			}
			manifest["roles"] = append(manifest["roles"].([]any), limited)
		}
	}
	for _, item := range manifest["seed_records"].([]any) {
		seed := item.(map[string]any)
		if seed["object_key"] == "customer" {
			seed["data"].(map[string]any)["balance"] = 10
		}
	}
	for i, name := range []string{"Beta", "Gamma"} {
		manifest["seed_records"] = append(manifest["seed_records"].([]any), map[string]any{"object_key": "customer", "owner_user_id": "admin", "data": map[string]any{"__seed_key": "customer_browser_" + name, "name": name, "owner": "admin", "balance": (i + 2) * 10}})
	}
	for _, apply := range customize {
		apply(manifest)
	}
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	options := agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}}
	if os.Getenv("RUNTIME_BUSINESS_LIVE") == "1" {
		options = agentmodule.OptionsFromEnvironment()
		if options.ConversationModel == "" || options.ConversationAPIKey == "" {
			t.Fatal("real business acceptance requires configured model and credential")
		}
		options.ConversationOptions.Poll = 10 * time.Millisecond
		options.Client = &http.Client{Timeout: 120 * time.Second, Transport: businessLiveModelTransport{t: t}}
	}
	var files fs.FS = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("business acceptance UI")}}
	if os.Getenv("RUNTIME_BUSINESS_BROWSER") == "1" {
		root := os.Getenv("RUNTIME_BUSINESS_FRONTEND")
		if root == "" {
			t.Fatal("RUNTIME_BUSINESS_FRONTEND must point to the built Agent UI")
		}
		if _, err := os.Stat(filepath.Join(root, "index.html")); err != nil {
			t.Fatal(err)
		}
		files = os.DirFS(root)
	}
	f := &businessWebFixture{t: t, cfg: initializedIntegrationRuntimeConfig(config.Config{AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(dir, "runtime.db"), ManifestPath: path, UploadDir: filepath.Join(dir, "uploads"), IdentityAudience: "domainry-runtime", RuntimeInstanceID: "business-browser-runtime", IntegrationSecretKey: "business-browser-integration-secret-fixture"}), identityPath: filepath.Join(dir, "identity.db"), options: options, files: files}
	t.Cleanup(f.close)
	return f
}

func (f *businessWebFixture) open() {
	f.t.Helper()
	var err error
	factory := f.identityFactory
	if factory == nil {
		factory = identitymodule.NewFactory(identitymodule.Options{DatabaseDriver: "sqlite", DatabasePath: f.identityPath})
	}
	f.identity, err = factory.Open(f.t.Context(), identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(f.cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(f.cfg.IdentityAudience)})
	if err != nil {
		f.t.Fatal(err)
	}
	f.runtime = bootstrap.NewWithScheduler(f.t.Context(), f.cfg, f.identity, notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()), schedulermodule.NewFactory(schedulermodule.OptionsFromEnvironment()), dataexchangefixture.NewFactory(), integrationmodule.NewFactory(), agentmodule.NewFactory(f.options))
	f.handler, err = bootstrap.ConversationWebHandler(f.runtime, bootstrap.ConversationWebOptions{Origin: businessWebOrigin, Model: f.options.ConversationModel, Files: f.files})
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *businessWebFixture) close() {
	if f.runtime != nil {
		if err := f.runtime.CloseContext(context.Background()); err != nil {
			f.t.Error(err)
		}
		f.runtime = nil
	}
	if f.identity != nil {
		if err := f.identity.Close(context.Background()); err != nil {
			f.t.Error(err)
		}
		f.identity = nil
	}
}

type businessBrowser struct {
	t       *testing.T
	f       *businessWebFixture
	cookies map[string]*http.Cookie
	scope   string
}

func (b *businessBrowser) call(method, path string, body any, want int) *httptest.ResponseRecorder {
	b.t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			b.t.Fatal(err)
		}
	}
	key := fmt.Sprintf("business-browser-%d", time.Now().UnixNano())
	once := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, businessWebOrigin+path, strings.NewReader(string(data)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", businessWebOrigin)
		r.Header.Set("X-Agent-Scope", b.scope)
		r.Header.Set("Idempotency-Key", key)
		for _, cookie := range b.cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		b.f.handler.ServeHTTP(w, r)
		for _, cookie := range w.Result().Cookies() {
			if cookie.MaxAge < 0 {
				delete(b.cookies, cookie.Name)
			} else {
				b.cookies[cookie.Name] = cookie
			}
		}
		return w
	}
	w := once()
	if w.Code == http.StatusUnauthorized && want != http.StatusUnauthorized && strings.HasPrefix(path, "/agent/") {
		// Match sessionFetch: refresh a rejected access token once, even when
		// /app/session still recognizes its owner after a policy change.
		b.call("POST", "/auth/refresh", map[string]any{}, 200)
		b.session()
		w = once()
	}
	if w.Code != want {
		b.t.Fatalf("browser %s %s status=%d want=%d body=%s", method, path, w.Code, want, w.Body.String())
	}
	return w
}

func (b *businessBrowser) session() {
	var session struct {
		Scope string `json:"scope"`
		Ready bool   `json:"ready"`
	}
	if err := json.Unmarshal(b.call("GET", "/app/session", nil, 200).Body.Bytes(), &session); err != nil || session.Scope == "" || !session.Ready {
		b.t.Fatal("browser session not ready", session, err)
	}
	b.scope = session.Scope
}

func (b *businessBrowser) assign(role string) {
	b.t.Helper()
	if b.f.identityHandler != nil {
		managedIdentityRequest(b.t, b.f.identityHandler, b.cookies["domainry_agent_access"].Value, "PUT", "/identity/users/admin/account-and-roles", map[string]any{"user": map[string]any{"name": "Admin", "email": "admin@example.com", "status": "active"}, "assignments": []any{map[string]any{"role_id": role}}}, fmt.Sprintf("business-role-%d", time.Now().UnixNano()), 200)
		return
	}
	mux := http.NewServeMux()
	for _, adapter := range b.f.identity.(identityhttpapi.Provider).HTTPAdapters() {
		for _, route := range adapter.Routes() {
			mux.Handle(route.Pattern(), adapter.Handler())
		}
	}
	managedIdentityRequest(b.t, mux, b.cookies["domainry_agent_access"].Value, "PUT", "/identity/users/admin/account-and-roles", map[string]any{"user": map[string]any{"name": "Admin", "email": "admin@example.com", "status": "active"}, "assignments": []any{map[string]any{"role_id": role}}}, fmt.Sprintf("business-role-%d", time.Now().UnixNano()), 200)
}

func (b *businessBrowser) run(conversationID string) agentsdk.ConversationRun {
	return b.runWithMessage(conversationID, "请通过业务目录发现客户对象和动作，再查询我能读取的全部客户：按名称升序，每页只查 1 条，使用返回的游标逐页查完；读取第一条客户的详情。最终列出每条客户的名称与余额。只使用工具实际返回的数据。")
}

func (b *businessBrowser) runWithMessage(conversationID, message string) agentsdk.ConversationRun {
	b.t.Helper()
	var run agentsdk.ConversationRun
	response := b.call("POST", "/agent/conversations/"+conversationID+"/messages", map[string]any{"client_message_id": fmt.Sprintf("business-query-%d", time.Now().UnixNano()), "message": message}, 202)
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		b.t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Minute)
	poll := 25 * time.Millisecond
	if b.f.identityFactory != nil {
		// Remote history reads revalidate saved sources over HTTP. Match a UI
		// polling interval instead of flooding its real application limiter.
		poll = 500 * time.Millisecond
	}
	for {
		response = b.call("GET", "/agent/conversations/"+conversationID+"/runs/"+run.ID, nil, 200)
		if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
			b.t.Fatal(err)
		}
		if run.Status == "completed" {
			return run
		}
		if run.Status == "failed" || run.Status == "waiting_user" || time.Now().After(deadline) {
			b.t.Fatalf("business browser run incomplete: %s", response.Body.String())
		}
		time.Sleep(poll)
	}
}
