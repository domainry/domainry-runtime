package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// Model decisions are a protocol fixture; Identity, Runtime bootstrap, Agent,
// HTTP authorization, owner/field policies and business SQL are actual services.
type managedBusinessConversationModel struct{}

func (managedBusinessConversationModel) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{}, fmt.Errorf("unexpected text-only model path")
}
func (managedBusinessConversationModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "business-identity", Fingerprint: "business-identity-v1"}
}
func (managedBusinessConversationModel) StreamConversationStep(_ context.Context, in agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	tool := func(key, id string, args any) (agentsdk.ConversationStepResult, error) {
		raw, _ := json.Marshal(args)
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: id, Name: key, Arguments: string(raw)}}}, FinishReason: "tool_calls"}, nil
	}
	last := in.Messages[len(in.Messages)-1]
	if last.Role != "tool" {
		return tool("business_catalog", "objects", map[string]any{"object_key": "customer"})
	}
	var result agentsdk.ConversationToolResult
	var evidence agentsdk.ConversationBusinessEvidence
	if json.Unmarshal([]byte(last.Content), &result) != nil || result.Status != "completed" || json.Unmarshal(result.Content, &evidence) != nil {
		return agentsdk.ConversationStepResult{}, fmt.Errorf("business tool failed: %s", last.Content)
	}
	switch last.ToolCallID {
	case "objects":
		var page agentsdk.ConversationBusinessCatalogPage
		if json.Unmarshal(evidence.Data, &page) != nil || len(page.Items) != 1 || len(page.Items[0].Fields) != 1 || page.Items[0].Fields[0].Key != "name" {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("Identity field projection was not applied")
		}
		return tool("business_catalog", "actions", map[string]any{"kind": "actions", "object_key": "customer"})
	case "actions":
		var page agentsdk.ConversationBusinessCatalogPage
		if json.Unmarshal(evidence.Data, &page) != nil || len(page.Actions) != 1 || page.Actions[0].Key != "customer.rename" {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("published action not discovered")
		}
		return tool("query_records", "records", map[string]any{"object_key": "customer", "fields": []string{"name"}, "page_size": 1})
	case "records":
		var page agentsdk.ConversationBusinessRecordPage
		if json.Unmarshal(evidence.Data, &page) != nil || len(page.Items) != 1 || page.HasNext || page.Total == nil || *page.Total != 1 || string(page.Items[0].Data["name"]) != `"Acme"` {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("Identity owner scope was not applied")
		}
		return tool("get_record", "detail", map[string]any{"object_key": "customer", "record_id": page.Items[0].ID, "fields": []string{"name"}})
	case "detail":
		var record agentsdk.ConversationBusinessRecord
		if json.Unmarshal(evidence.Data, &record) != nil || len(record.Data) != 1 || string(record.Data["name"]) != `"Acme"` {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("authorized record detail mismatch")
		}
		text := "当前可访问的客户为 Acme。"
		if err := emit(agentsdk.ConversationModelEvent{Type: "text.delta", Delta: text}); err != nil {
			return agentsdk.ConversationStepResult{}, err
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: text}, FinishReason: "stop"}, nil
	}
	return agentsdk.ConversationStepResult{}, fmt.Errorf("unexpected business model step")
}

func managedBusinessConversationManifest(t *testing.T, dir string) string {
	t.Helper()
	manifestPath := managedIdentityFieldAccessManifest(t, dir)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, value := range manifest["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] != "headquarters_admin" {
			continue
		}
		for _, key := range []string{"identity.user_role_assignments.account_and_roles_update"} {
			role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": key, "data_scope": "all"})
		}
		for _, tool := range agentsdk.BusinessConversationTools() {
			role["permissions"] = append(role["permissions"].([]any), map[string]any{"permission_key": tool.ActionKey, "data_scope": "all"})
		}
	}
	// Runtime owns these role definitions; Identity owns user assignments.
	// The restricted role retains the same tool grants but lacks customer.read.
	for _, value := range manifest["roles"].([]any) {
		role := value.(map[string]any)
		if role["key"] != "headquarters_admin" {
			continue
		}
		encoded, _ := json.Marshal(role)
		var restricted map[string]any
		_ = json.Unmarshal(encoded, &restricted)
		restricted["key"], restricted["name"] = "business_restricted", "Restricted business reader"
		kept := []any{}
		for _, value := range restricted["permissions"].([]any) {
			if value.(map[string]any)["permission_key"] != "customer.read" {
				kept = append(kept, value)
			}
		}
		restricted["permissions"] = kept
		manifest["roles"] = append(manifest["roles"].([]any), restricted)
		break
	}

	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifestPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return manifestPath
}

func TestConversationBusinessThroughManagedIdentityAndRuntimeHTTP(t *testing.T) {
	dir := t.TempDir()
	manifestPath := managedBusinessConversationManifest(t, dir)
	cfg := initializedIntegrationRuntimeConfig(config.Config{AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(dir, "runtime.db"), ManifestPath: manifestPath, UploadDir: filepath.Join(dir, "uploads"), IdentityAudience: "domainry-runtime", RuntimeInstanceID: "conversation-business-identity"})
	binding, err := identitymodule.NewFactory(identitymodule.Options{DatabaseDriver: "sqlite", DatabasePath: filepath.Join(dir, "identity.db")}).Open(t.Context(), identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(context.Background()) })
	runtime := bootstrap.NewWithScheduler(t.Context(), cfg, binding, notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()), schedulermodule.NewFactory(schedulermodule.OptionsFromEnvironment()), dataexchangefixture.NewFactory(), integrationmodule.NewFactory(), agentmodule.NewFactory(agentmodule.Options{ConversationProvider: managedBusinessConversationModel{}, ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}}))
	t.Cleanup(func() { _ = runtime.CloseContext(context.Background()) })
	login := func(password string) identitysdk.AuthSession {
		session, err := binding.Authentication().LoginWithPassword(t.Context(), identitysdk.PasswordLoginRequest{WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience), Login: "admin@example.com", Password: password})
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	session := login("Domainry@2026")
	session, err = binding.Credentials().ChangePassword(t.Context(), identitysdk.ChangePasswordRequest{AccessToken: session.AccessToken, CurrentPassword: "Domainry@2026", NewPassword: "ConversationBusiness@2026", IdempotencyKey: "business-password"})
	if err != nil {
		t.Fatal(err)
	}
	managedIdentityAssignHeadquartersRole(t, binding, session.AccessToken)
	session = login("ConversationBusiness@2026")
	handler := runtime.Routes()
	var conversation agentsdk.Conversation
	created := managedIdentityRequest(t, handler, session.AccessToken, "POST", "/agent/conversations", map[string]any{"client_id": "business-identity"}, "create-business", 200)
	if err := json.Unmarshal(created.Body.Bytes(), &conversation); err != nil || conversation.ID == "" {
		t.Fatal(created.Body.String(), err)
	}
	var run agentsdk.ConversationRun
	started := managedIdentityRequest(t, handler, session.AccessToken, "POST", "/agent/conversations/"+conversation.ID+"/messages", map[string]any{"client_message_id": "query-business", "message": "查询我能访问的客户"}, "send-business", 202)
	if err := json.Unmarshal(started.Body.Bytes(), &run); err != nil || run.ID == "" {
		t.Fatal(started.Body.String(), err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		response := managedIdentityRequest(t, handler, session.AccessToken, "GET", "/agent/conversations/"+conversation.ID+"/runs/"+run.ID, nil, "", 200)
		if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
		if run.Status == "completed" {
			break
		}
		if run.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("real business run did not complete: %s", response.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(run.Steps) != 5 {
		t.Fatalf("missing persisted tool steps: %+v", run)
	}
	path := "/agent/conversations/" + conversation.ID + "/messages"
	if response := managedIdentityRequest(t, handler, session.AccessToken, "GET", path, nil, "", 200); !strings.Contains(response.Body.String(), "Acme") {
		t.Fatal("business answer missing")
	}
	// Change the real Identity assignment to a role with the business read
	// grant removed. The user stays signed in and retains all tool permissions.
	mux := http.NewServeMux()
	for _, adapter := range binding.(identityhttpapi.Provider).HTTPAdapters() {
		for _, route := range adapter.Routes() {
			mux.Handle(route.Pattern(), adapter.Handler())
		}
	}
	managedIdentityRequest(t, mux, session.AccessToken, "PUT", "/identity/users/admin/account-and-roles", map[string]any{
		"user":        map[string]any{"name": "Admin", "email": "admin@example.com", "status": "active"},
		"assignments": []any{map[string]any{"role_id": "business_restricted"}},
	}, "business-role-revoke", 200)
	resolution, err := binding.Principals().Resolve(t.Context(), identitysdk.PrincipalResolutionRequest{SubjectID: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	principal := resolution.Principal
	principal.AccessBundle = &resolution.AccessBundle
	if !principal.Known || principal.HasPermission("customer.read") {
		t.Fatal("business read grant was not revoked")
	}
	for _, tool := range agentsdk.BusinessConversationTools() {
		if !principal.HasPermission(tool.ActionKey) {
			t.Fatal("test unintentionally removed tool grant", tool.ActionKey)
		}
	}
	history := managedIdentityRequest(t, handler, session.AccessToken, "GET", path, nil, "", 200)
	var messages agentsdk.ConversationMessagePage
	if err := json.Unmarshal(history.Body.Bytes(), &messages); err != nil || len(messages.Items) != 2 || messages.Items[1].AccessError == "" || strings.Contains(messages.Items[1].Content, "Acme") {
		t.Fatalf("revoked business facts still readable: %s err=%v", history.Body.String(), err)
	}
}
