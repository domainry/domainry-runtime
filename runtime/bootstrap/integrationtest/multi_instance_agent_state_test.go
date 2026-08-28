package integrationtest

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"

	agentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestTwoRuntimeInstancesShareAgentSessionAndReportHTTPState(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(dir, "shared.db"), ManifestPath: filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "domain-only-minimal.json"), UploadDir: filepath.Join(dir, "uploads")}
	first := newIntegrationRuntime(t, cfg)
	defer first.Close()
	second := newIntegrationRuntime(t, cfg)
	defer second.Close()
	store := openRuntimePersistenceFixture(t, cfg)

	created := runtimeFixtureRequest[map[string]any](t, first.Routes(), "business_admin", http.MethodPost, "/agent-dialog/sessions", map[string]any{"external_session_id": "shared-session", "title": "Shared session"})
	if created["external_session_id"] != "shared-session" {
		t.Fatalf("created session=%#v", created)
	}
	listed := runtimeFixtureRequest[map[string]any](t, second.Routes(), "business_admin", http.MethodGet, "/agent-dialog/sessions", nil)
	if sessions, ok := listed["sessions"].([]any); !ok || len(sessions) != 1 {
		t.Fatalf("second runtime sessions=%#v", listed)
	}
	runtimeFixtureRequest[map[string]any](t, second.Routes(), "business_admin", http.MethodPost, "/agent-dialog/sessions/shared-session/archive", nil)
	archived := runtimeFixtureRequest[map[string]any](t, first.Routes(), "business_admin", http.MethodGet, "/agent-dialog/sessions?archived=true", nil)
	if sessions, ok := archived["sessions"].([]any); !ok || len(sessions) != 1 {
		t.Fatalf("first runtime archived sessions=%#v", archived)
	}

	queryRef, key, now := "shared-query", "default:runtime_fixture_user:business_admin:shared-query", time.Now().UTC().UnixNano()
	proposalPayload, _ := json.Marshal(map[string]any{"proposal_id": "shared-proposal", "workspace_id": "default", "user_id": "runtime_fixture_user", "role": "business_admin", "status": "draft", "title": "Shared proposal", "created_at": now, "updated_at": now, "audited": true})
	queryPayload, _ := json.Marshal(map[string]any{"query_ref": queryRef, "workspace_id": "default", "user_id": "runtime_fixture_user", "role": "business_admin", "status": "completed", "execution_mode": "server", "created_at": now})
	auditPayload, _ := json.Marshal(map[string]any{"query_ref": queryRef, "workspace_id": "default", "user_id": "runtime_fixture_user", "role": "business_admin", "status": "handoff_required", "handoff": "report_center_export_audit", "created_at": now})
	taskPayload, _ := json.Marshal(map[string]any{"query_ref": queryRef, "workspace_id": "default", "user_id": "runtime_fixture_user", "role": "business_admin", "status": "pending_export_approval", "task_key": "download_task:" + queryRef, "handoff": "report_center_download_task", "created_at": now})
	repository := agentpersistence.NewAgentStateStore(store)
	if err := repository.PutBatch(t.Context(), "default", []agentmodel.AgentStateRecord{{Kind: "proposal", Key: "default:runtime_fixture_user:business_admin:shared-proposal", WorkspaceID: "default", UserID: "runtime_fixture_user", RoleKey: "business_admin", Payload: proposalPayload, UpdatedAt: now}, {Kind: "report_query_run", Key: key, WorkspaceID: "default", UserID: "runtime_fixture_user", RoleKey: "business_admin", Payload: queryPayload, UpdatedAt: now}, {Kind: "report_export_audit", Key: key, WorkspaceID: "default", UserID: "runtime_fixture_user", RoleKey: "business_admin", Payload: auditPayload, UpdatedAt: now}, {Kind: "report_download_task", Key: key, WorkspaceID: "default", UserID: "runtime_fixture_user", RoleKey: "business_admin", Payload: taskPayload, UpdatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	query := runtimeFixtureRequest[map[string]any](t, second.Routes(), "business_admin", http.MethodGet, "/agent-dialog/report-query-runs/"+queryRef, nil)
	if query["status"] != "completed" {
		t.Fatalf("second runtime query=%#v", query)
	}
	runtimeFixtureRequest[map[string]any](t, second.Routes(), "business_admin", http.MethodPost, "/agent-dialog/download-tasks/"+queryRef+"/prepare", nil)
	task := runtimeFixtureRequest[map[string]any](t, first.Routes(), "business_admin", http.MethodGet, "/agent-dialog/download-tasks/"+queryRef, nil)
	if task["status"] != "prepared_for_report_center" {
		t.Fatalf("first runtime task=%#v", task)
	}

	if err := os.RemoveAll(cfg.UploadDir); err != nil {
		t.Fatal(err)
	}
	third := newIntegrationRuntime(t, cfg)
	defer third.Close()
	proposal := runtimeFixtureRequest[map[string]any](t, third.Routes(), "business_admin", http.MethodGet, "/agent-dialog/proposals/shared-proposal", nil)
	if proposal["status"] != "draft" {
		t.Fatalf("replacement runtime proposal=%#v", proposal)
	}
	audit := runtimeFixtureRequest[map[string]any](t, third.Routes(), "business_admin", http.MethodGet, "/agent-dialog/report-export-audits/"+queryRef, nil)
	if audit["status"] != "prepared_for_report_center" {
		t.Fatalf("replacement runtime audit=%#v", audit)
	}
	replacementSessions := runtimeFixtureRequest[map[string]any](t, third.Routes(), "business_admin", http.MethodGet, "/agent-dialog/sessions?archived=true", nil)
	if sessions, ok := replacementSessions["sessions"].([]any); !ok || len(sessions) != 1 {
		t.Fatalf("replacement runtime sessions=%#v", replacementSessions)
	}
}
