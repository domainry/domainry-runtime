package integrationtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationmodule "github.com/domainry/domainry-integration/module"
	notificationmodule "github.com/domainry/domainry-notification/module"
	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	dispatchhttp "github.com/domainry/domainry-runtime/runtime/transport/http/dispatch"
	dataexchangefixture "github.com/domainry/domainry-runtime/testsupport/dataexchangefixture"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	schedulergateway "github.com/domainry/domainry-scheduler-sdk/dispatchgateway"
	schedulermodule "github.com/domainry/domainry-scheduler/module"
)

type g06FollowUpModel struct{ calls atomic.Int32 }

func (*g06FollowUpModel) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{}, fmt.Errorf("unexpected text-only path")
}
func (*g06FollowUpModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "responses", Model: "g06-follow-up", Fingerprint: "g06-follow-up-v1"}
}
func (m *g06FollowUpModel) StreamConversationStep(_ context.Context, _ agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	var content string
	switch m.calls.Add(1) {
	case 1:
		content = `{"status":"active","observation":"{\"open\":2,\"blocked\":0}","summary":"Two tasks remain"}`
	case 2:
		content = `{"status":"active","observation":"{\"blocked\":0,\"open\":2}","summary":"Still two tasks"}`
	case 3:
		content = `{"status":"active","observation":"{\"open\":1,\"blocked\":0}","summary":"One task remains"}`
	default:
		content = `{"status":"completed","observation":"{\"open\":0,\"blocked\":0}","summary":"All release tasks are complete"}`
	}
	if err := emit(agentsdk.ConversationModelEvent{Type: "text.delta", Delta: content}); err != nil {
		return agentsdk.ConversationStepResult{}, err
	}
	return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: content}, FinishReason: "stop"}, nil
}

func g06IdentityBinding(t *testing.T, cfg config.Config) identitysdk.Binding {
	t.Helper()
	roles := integrationIdentityFixtureRoles()
	for index := range roles {
		if roles[index].Key == "admin" {
			roles[index].Permissions = append(roles[index].Permissions,
				"agent.conversations.create", "agent.conversations.run", "agent.conversations.messages", "agent.conversations.tasks_list",
				"notification.inbox.list", "notification.inbox.item.get",
			)
		}
	}
	factory := runtimetestkit.NewIdentityFactory(runtimetestkit.IdentityFixtureConfig{
		Roles:               roles,
		Users:               []identitysdk.User{{ID: "runtime_fixture_user", Name: "Runtime fixture user", Email: "runtime-fixture@example.com", Status: "active", Locale: "en-US"}},
		UserRoleAssignments: map[string][]string{"runtime_fixture_user": {"admin"}},
	})
	binding, err := factory.Open(t.Context(), identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience)})
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func g06Manifest(t *testing.T, directory string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "domain-only-minimal.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "g06-manifest.json")
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func g06AgentRequest(t *testing.T, handler http.Handler, method, path string, body any, idempotency string, want int) []byte {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	request.Header.Set("Authorization", "Bearer "+integrationIdentityAccessTokenFor("runtime_fixture_user", "admin"))
	request.Header.Set("X-Workspace-ID", "workspace-primary")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotency != "" {
		request.Header.Set("Idempotency-Key", idempotency)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.Code, want, response.Body.String())
	}
	return response.Body.Bytes()
}

func g06SendScheduledWindow(t *testing.T, handler http.Handler, cfg config.Config, conversationID string, window int) {
	t.Helper()
	dueAt := time.Now().UTC().Truncate(time.Millisecond)
	idempotency := fmt.Sprintf("plan-g06:%d:%s", window, dueAt.Format(time.RFC3339Nano))
	dispatch, err := json.Marshal(schedulersdk.ScheduledPlanDispatch{
		ContractVersion: schedulersdk.ScheduledPlanDispatchContractVersion, PlanID: "plan-g06", Owner: schedulersdk.ScheduledPlanOwner{WorkspaceID: cfg.IdentityWorkspaceID, UserID: "runtime_fixture_user", ProductKey: "business_only_minimal"},
		Input:           json.RawMessage(`{"goal":"Follow release tasks","allowed_tools":[],"follow_up":{"completion_condition":"all release tasks are complete"}}`),
		ConversationRef: schedulersdk.ScheduledPlanConversationRef{ConversationID: conversationID},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"runtime_id": cfg.RuntimeInstanceID, "execution_id": fmt.Sprintf("scheduler-g06-%d", window), "idempotency_key": idempotency, "due_at": dueAt,
		"target": map[string]any{"type": "runtime_operation", "owner": "agent", "operation": "conversation_task_start", "payload": json.RawMessage(dispatch)},
	})
	if err != nil {
		t.Fatal(err)
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature, err := schedulergateway.SignRequest(body, schedulergateway.SignedRequest{Method: http.MethodPost, Path: dispatchhttp.RuntimeExecutionPath, RuntimeID: cfg.RuntimeInstanceID, IdempotencyKey: idempotency}, schedulergateway.SchedulerClientID, timestamp, []byte(cfg.IntegrationSecretKey))
	if err != nil {
		t.Fatal(err)
	}
	var response *httptest.ResponseRecorder
	for attempt := 0; attempt < 5; attempt++ {
		request := httptest.NewRequest(http.MethodPost, dispatchhttp.RuntimeExecutionPath, bytes.NewReader(body))
		request.Header.Set(schedulergateway.RuntimeIDHeader, cfg.RuntimeInstanceID)
		request.Header.Set(schedulergateway.SignatureVersionHeader, schedulergateway.CallbackSignatureContractVersion)
		request.Header.Set(schedulergateway.ClientIDHeader, schedulergateway.SchedulerClientID)
		request.Header.Set(schedulergateway.TimestampHeader, timestamp)
		request.Header.Set(schedulergateway.SignatureHeader, signature)
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code == http.StatusOK {
			return
		}
		if response.Code < http.StatusInternalServerError {
			break
		}
		// Scheduler retries the exact signed callback after a retryable receiver
		// failure. Preserve the body and idempotency key so Runtime must reclaim
		// its failed-retryable receipt rather than create a second execution.
		time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
	}
	t.Fatalf("scheduled window %d status=%d body=%s", window, response.Code, response.Body.String())
}

func g06WaitTasks(t *testing.T, handler http.Handler, count int) []agentsdk.ConversationTaskSummary {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var page agentsdk.ConversationTaskPage
		raw := g06AgentRequest(t, handler, http.MethodGet, "/agent/conversation-tasks?limit=20", nil, "", http.StatusOK)
		if json.Unmarshal(raw, &page) == nil && len(page.Items) == count {
			allTerminal := true
			for _, task := range page.Items {
				allTerminal = allTerminal && task.Status != agentsdk.ConversationTaskStatusQueued && task.Status != agentsdk.ConversationTaskStatusRunning
			}
			if allTerminal {
				return page.Items
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("did not observe %d terminal follow-up tasks", count)
	return nil
}

func g06FollowUpNotifications(t *testing.T, handler http.Handler) []map[string]any {
	t.Helper()
	raw := g06AgentRequest(t, handler, http.MethodGet, "/notification/inbox?scope=mine&mailbox=inbox&limit=20", nil, "", http.StatusOK)
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	items := []map[string]any{}
	for _, item := range page.Items {
		if value, _ := item["event_type"].(string); len(value) >= len("agent.follow_up.") && value[:len("agent.follow_up.")] == "agent.follow_up." {
			items = append(items, item)
		}
	}
	return items
}

func TestScheduledAgentFollowUpUsesSilentBaselineSurvivesRestartAndNotifiesOnlyMeaningfulOutcomes(t *testing.T) {
	directory := t.TempDir()
	cfg := initializedIntegrationRuntimeConfig(config.Config{AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(directory, "runtime.db"), ManifestPath: g06Manifest(t, directory), UploadDir: filepath.Join(directory, "uploads"), RuntimeInstanceID: "g06-runtime", IdentityAudience: "domainry-runtime", IntegrationSecretKey: "g06-signing-secret", WorkerPollInterval: 5 * time.Millisecond, WorkerBatchSize: 25})
	identity := g06IdentityBinding(t, cfg)
	defer identity.Close(context.Background())
	model := &g06FollowUpModel{}
	open := func() *bootstrap.Runtime {
		runtime := bootstrap.NewWithScheduler(t.Context(), cfg, identity, notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()), schedulermodule.NewFactory(schedulermodule.OptionsFromEnvironment()), dataexchangefixture.NewFactory(), integrationmodule.NewFactory(), agentmodule.NewFactory(agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond, Lease: 300 * time.Millisecond}}))
		bootstrap.StartWorkers(t.Context(), runtime)
		return runtime
	}
	runtime := open()
	handler := runtime.Routes()
	var conversation agentsdk.Conversation
	if err := json.Unmarshal(g06AgentRequest(t, handler, http.MethodPost, "/agent/conversations", map[string]any{"client_id": "g06-follow-up", "title": "Release follow-up"}, "create-g06", http.StatusOK), &conversation); err != nil || conversation.ID == "" {
		t.Fatalf("conversation=%+v err=%v", conversation, err)
	}

	g06SendScheduledWindow(t, handler, cfg, conversation.ID, 1)
	g06WaitTasks(t, handler, 1)
	time.Sleep(100 * time.Millisecond)
	if items := g06FollowUpNotifications(t, handler); len(items) != 0 {
		t.Fatalf("baseline notifications=%#v", items)
	}
	if err := runtime.CloseContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Reopen the complete Runtime against the same SQLite file. The next
	// observation is semantically equal with reordered JSON keys and stays quiet.
	runtime = open()
	defer runtime.CloseContext(context.Background())
	handler = runtime.Routes()
	g06SendScheduledWindow(t, handler, cfg, conversation.ID, 2)
	g06WaitTasks(t, handler, 2)
	time.Sleep(100 * time.Millisecond)
	if items := g06FollowUpNotifications(t, handler); len(items) != 0 {
		t.Fatalf("unchanged notifications=%#v", items)
	}

	g06SendScheduledWindow(t, handler, cfg, conversation.ID, 3)
	g06WaitTasks(t, handler, 3)
	deadline := time.Now().Add(5 * time.Second)
	for len(g06FollowUpNotifications(t, handler)) != 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	items := g06FollowUpNotifications(t, handler)
	if len(items) != 1 || items[0]["event_type"] != "agent.follow_up.changed" || items[0]["body"] != "Follow release tasks: One task remains" {
		t.Fatalf("changed notifications=%#v", items)
	}

	g06SendScheduledWindow(t, handler, cfg, conversation.ID, 4)
	g06WaitTasks(t, handler, 4)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current := g06FollowUpNotifications(t, handler)
		if len(current) == 1 && current[0]["event_type"] == "agent.follow_up.completed" && current[0]["occurrence_count"] == float64(2) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	items = g06FollowUpNotifications(t, handler)
	// Notification owns grouping: the completed event updates the existing
	// plan group and its occurrence_count proves both meaningful events were
	// materialized, while the two quiet windows created none.
	if len(items) != 1 || items[0]["event_type"] != "agent.follow_up.completed" || items[0]["occurrence_count"] != float64(2) || model.calls.Load() != 4 {
		t.Fatalf("terminal notifications=%#v calls=%d", items, model.calls.Load())
	}
}
