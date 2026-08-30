package agentdialog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type agentDialogAuditRepository struct {
	events []auditmodel.AuditEvent
	err    error
}

func (repository *agentDialogAuditRepository) InsertAuditEvent(context.Context, string, auditmodel.AuditEvent) error {
	return repository.err
}
func (repository *agentDialogAuditRepository) ListAuditEvents(context.Context, string, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	return repository.events, repository.err
}
func (repository *agentDialogAuditRepository) ListAuditEventsForSystem(context.Context, principalmodel.SystemScope, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	return repository.events, repository.err
}
func (repository *agentDialogAuditRepository) ListAuditOptions(context.Context, string, auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error) {
	return nil, repository.err
}

func agentDialogDiagnosticsHandler(stateRepository *agentDialogStateRepository, auditRepository *agentDialogAuditRepository) *AgentDialogHandler {
	principal := agentDialogStatePrincipal()
	state := agentapplication.NewAgentApplicationService(stateRepository)
	return NewAgentDialogHandler(AgentDialogDependencies{
		ProposalState: state, DiagnosticAudit: auditapplication.NewAuditApplicationService(auditRepository),
		Config:    Config{},
		Principal: func(*http.Request) principalmodel.Principal { return principal },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		Admin: func(next http.HandlerFunc) http.HandlerFunc { return next },
	})
}

func TestAgentDialogDiagnosticsProjectsContextToolsEventsAndProposalQueue(t *testing.T) {
	stateRepository := newAgentDialogStateRepository()
	principal := agentDialogStatePrincipal()
	state := agentapplication.NewAgentApplicationService(stateRepository)
	for _, proposal := range []agentapplication.AgentProposal{
		{ProposalID: "customer-proposal", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, Metadata: map[string]any{"object_key": "customer"}},
		{ProposalID: "order-proposal", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, Metadata: map[string]any{"object_key": "order"}},
	} {
		if _, err := state.StoreProposal(t.Context(), proposal); err != nil {
			t.Fatal(err)
		}
	}
	auditRepository := &agentDialogAuditRepository{events: []auditmodel.AuditEvent{
		{Event: "agent_dialog_run", ObjectKey: "customer"},
		{Event: "agent_analysis_query", Metadata: map[string]any{"object_key": "customer"}},
		{Event: "agent_dialog_proposal_approved", ObjectKey: "order"},
		{Event: "agent_analysis_query", ObjectKey: "order"},
		{Event: "record.updated", ObjectKey: "customer"},
	}}
	handler := agentDialogDiagnosticsHandler(stateRepository, auditRepository)
	request := httptest.NewRequest(http.MethodGet, "/agent-dialog/diagnostics?workspace=CRM&module_key=customers&object_key=customer&record_id=customer-1", nil)
	response := httptest.NewRecorder()
	handler.agentDialogDiagnostics(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	agentRunner := payload["agent_runner"].(map[string]any)
	if agentRunner["configured"] != false || agentRunner["transport"] != "domainry-agent-sdk" {
		t.Fatalf("agent runner diagnostics=%#v", agentRunner)
	}
	if queue := payload["proposal_queue"].([]any); len(queue) != 1 || !strings.Contains(response.Body.String(), "customer-proposal") || strings.Contains(response.Body.String(), "order-proposal") {
		t.Fatalf("proposal queue=%#v body=%s", queue, response.Body.String())
	}
	if events := payload["execution_logs"].([]any); len(events) != 3 || payload["execution_log_error"] != "" {
		t.Fatalf("events=%#v error=%#v", events, payload["execution_log_error"])
	}
	if events, message := handler.agentDialogDiagnosticEvents(t.Context(), principal, ""); len(events) != 4 || message != "" {
		t.Fatalf("unfiltered events=%#v message=%q", events, message)
	}
	if queue := handler.agentDialogDiagnosticProposalQueue(t.Context(), principal, ""); len(queue) != 2 {
		t.Fatalf("unfiltered proposal queue=%#v", queue)
	}
	if !strings.Contains(response.Body.String(), `"workspace_id":"workspace-1"`) || !strings.Contains(response.Body.String(), `"run_mode":"suggested_write"`) {
		t.Fatalf("runtime/policy context missing: %s", response.Body.String())
	}
}

func TestAgentDialogDiagnosticEventsAndProposalQueueFailures(t *testing.T) {
	want := errors.New("audit unavailable")
	auditRepository := &agentDialogAuditRepository{err: want}
	stateRepository := newAgentDialogStateRepository()
	handler := agentDialogDiagnosticsHandler(stateRepository, auditRepository)
	events, message := handler.agentDialogDiagnosticEvents(t.Context(), agentDialogStatePrincipal(), "")
	if len(events) != 0 || message != "backend.internal" {
		t.Fatalf("events=%#v message=%q", events, message)
	}
	stateRepository.listErr = errors.New("state unavailable")
	if queue := handler.agentDialogDiagnosticProposalQueue(t.Context(), agentDialogStatePrincipal(), ""); len(queue) != 0 {
		t.Fatalf("failed proposal queue=%#v", queue)
	}

	response := httptest.NewRecorder()
	handler.agentDialogDiagnostics(response, httptest.NewRequest(http.MethodGet, "/agent-dialog/diagnostics?agent_mode=&run_mode=&risk_level=", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "backend.internal") {
		t.Fatalf("diagnostics error status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentDialogDiagnosticToolPolicyWhyAndNextStepMatrix(t *testing.T) {
	if agentDiagnosticsValue(" ", "fallback") != "fallback" || agentDiagnosticsValue(" value ", "fallback") != "value" {
		t.Fatal("diagnostic value fallback mismatch")
	}
	allowed := agentDialogDiagnosticTool("read", "generated", "read_only", "low", false, "")
	if allowed["allowed"] != true || allowed["why"] == "" || allowed["next_step"] == "" {
		t.Fatalf("allowed tool=%#v", allowed)
	}
	denied := agentDialogDiagnosticTool("mysql", "database", "read_only", "medium", false, " fallback_diagnosis_only ")
	if denied["allowed"] != false || !strings.Contains(denied["why"].(string), "fallback") {
		t.Fatalf("denied tool=%#v", denied)
	}

	policy := agentDialogDiagnosticPolicyTool("approve_proposal", "critical", true, map[string]any{"agent_mode": "domain-flow", "run_mode": "suggested_write"})
	if policy["allowed"] != false || policy["denial_reason"] != "agent_dialog.policy_risk_denied" || policy["required_run_mode"] != "suggested_write" {
		t.Fatalf("policy tool=%#v", policy)
	}
	for reason, whyFragment := range map[string]string{
		"":                                    "allowed",
		"agent_dialog.policy_read_only":       "read_only",
		"agent_dialog.policy_run_mode_denied": "suggested_write",
		"agent_dialog.policy_risk_denied":     "critical-risk",
		"fallback_diagnosis_only":             "fallback",
		"custom.reason":                       "custom.reason",
	} {
		if got := agentDialogDiagnosticWhy(reason); !strings.Contains(got, whyFragment) {
			t.Errorf("why(%q)=%q missing %q", reason, got, whyFragment)
		}
	}
	for _, test := range []struct{ key, reason, fragment string }{
		{key: "approve_proposal", fragment: "explicit approval"},
		{key: "reject_proposal", fragment: "explicit approval"},
		{key: "read", fragment: "action binder"},
		{reason: "agent_dialog.policy_read_only", fragment: "suggested_write"},
		{reason: "agent_dialog.policy_run_mode_denied", fragment: "suggested_write"},
		{reason: "agent_dialog.policy_risk_denied", fragment: "manual admin review"},
		{reason: "fallback_diagnosis_only", fragment: "generated APIs"},
		{reason: "custom", fragment: "inspect runtime context"},
	} {
		if got := agentDialogDiagnosticNextStep(test.key, test.reason); !strings.Contains(got, test.fragment) {
			t.Errorf("nextStep(%q,%q)=%q missing %q", test.key, test.reason, got, test.fragment)
		}
	}
}

func TestAgentDialogRegistersAllRoutes(t *testing.T) {
	handler := agentDialogDiagnosticsHandler(newAgentDialogStateRepository(), &agentDialogAuditRepository{})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	for _, path := range []string{"/agent-dialog/diagnostics", "/agent-dialog/sessions", "/agent-dialog/proposals", "/agent-dialog/report-query-runs/query-1"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		_, pattern := mux.Handler(request)
		if pattern == "" {
			t.Errorf("route %s was not registered", path)
		}
	}
}
