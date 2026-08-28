package agent

import (
	"context"
	"encoding/json"
	"time"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AgentReportQueryRun struct {
	QueryRef      string         `json:"query_ref"`
	ReportKey     string         `json:"report_key,omitempty"`
	ObjectKey     string         `json:"object_key,omitempty"`
	WorkspaceID   string         `json:"workspace_id,omitempty"`
	UserID        string         `json:"user_id,omitempty"`
	Role          string         `json:"role,omitempty"`
	Status        string         `json:"status"`
	ExecutionMode string         `json:"execution_mode"`
	RowCount      int            `json:"row_count"`
	Total         int            `json:"total"`
	Truncated     bool           `json:"truncated"`
	AuditEventKey string         `json:"audit_event_key"`
	Scope         string         `json:"scope"`
	CreatedAt     int64          `json:"created_at"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}
type AgentReportExportAudit struct {
	QueryRef      string `json:"query_ref"`
	ReportKey     string `json:"report_key,omitempty"`
	WorkspaceID   string `json:"workspace_id,omitempty"`
	UserID        string `json:"user_id,omitempty"`
	Role          string `json:"role,omitempty"`
	Status        string `json:"status"`
	AuditEventKey string `json:"audit_event_key"`
	Handoff       string `json:"handoff"`
	PreparedAt    int64  `json:"prepared_at,omitempty"`
	PreparedBy    string `json:"prepared_by,omitempty"`
	NextStep      string `json:"next_step,omitempty"`
	CreatedAt     int64  `json:"created_at"`
}
type AgentReportDownloadTask struct {
	QueryRef    string `json:"query_ref"`
	ReportKey   string `json:"report_key,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	UserID      string `json:"user_id,omitempty"`
	Role        string `json:"role,omitempty"`
	Status      string `json:"status"`
	TaskKey     string `json:"task_key"`
	Handoff     string `json:"handoff"`
	PreparedAt  int64  `json:"prepared_at,omitempty"`
	PreparedBy  string `json:"prepared_by,omitempty"`
	NextStep    string `json:"next_step,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}
type AgentReportGovernanceRequest struct {
	QueryRef, ReportKey, ObjectKey, ExecutionMode, AuditEventKey string
	RowCount, Total                                              int
	Truncated                                                    bool
}
type AgentReportHandoff struct {
	QueryRun     AgentReportQueryRun     `json:"report_query_run"`
	ExportAudit  AgentReportExportAudit  `json:"report_export_audit"`
	DownloadTask AgentReportDownloadTask `json:"download_task"`
}

func (s *AgentApplicationService) RecordReportGovernance(ctx context.Context, request AgentReportGovernanceRequest, principal principalmodel.Principal) (map[string]any, error) {
	workspaceID, err := agentCommandWorkspace(principal)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().UnixNano()
	ref := request.QueryRef
	key := agentStateKey(workspaceID, principal.UserID, principal.RoleKey, agentStatePart(ref))
	query := AgentReportQueryRun{QueryRef: ref, ReportKey: request.ReportKey, ObjectKey: request.ObjectKey, WorkspaceID: workspaceID, UserID: principal.UserID, Role: principal.RoleKey, Status: "completed", ExecutionMode: request.ExecutionMode, RowCount: request.RowCount, Total: request.Total, Truncated: request.Truncated, AuditEventKey: request.AuditEventKey, Scope: "server_principal_scoped", CreatedAt: now, Metadata: map[string]any{"report_query_run_id": ref, "report_export_audit_id": ref, "download_task_id": ref}}
	audit := AgentReportExportAudit{QueryRef: ref, ReportKey: request.ReportKey, WorkspaceID: workspaceID, UserID: principal.UserID, Role: principal.RoleKey, Status: "handoff_required", AuditEventKey: request.AuditEventKey, Handoff: "report_center_export_audit", CreatedAt: now}
	task := AgentReportDownloadTask{QueryRef: ref, ReportKey: request.ReportKey, WorkspaceID: workspaceID, UserID: principal.UserID, Role: principal.RoleKey, Status: "pending_export_approval", TaskKey: "download_task:" + ref, Handoff: "report_center_download_task", CreatedAt: now}
	qp, _ := json.Marshal(query)
	ap, _ := json.Marshal(audit)
	tp, _ := json.Marshal(task)
	err = s.repository.PutBatch(ctx, workspaceID, []agentmodel.AgentStateRecord{{Kind: "report_query_run", Key: key, WorkspaceID: workspaceID, UserID: principal.UserID, RoleKey: principal.RoleKey, Payload: qp, UpdatedAt: now}, {Kind: "report_export_audit", Key: key, WorkspaceID: workspaceID, UserID: principal.UserID, RoleKey: principal.RoleKey, Payload: ap, UpdatedAt: now}, {Kind: "report_download_task", Key: key, WorkspaceID: workspaceID, UserID: principal.UserID, RoleKey: principal.RoleKey, Payload: tp, UpdatedAt: now}})
	if err != nil {
		return nil, err
	}
	return query.Metadata, nil
}
func (s *AgentApplicationService) GetReportQueryRun(ctx context.Context, ref string, principal principalmodel.Principal) (AgentReportQueryRun, error) {
	var value AgentReportQueryRun
	err := s.loadReportState(ctx, "report_query_run", ref, principal, &value, "agent_dialog.report_query_run_not_found")
	return value, err
}
func (s *AgentApplicationService) GetReportExportAudit(ctx context.Context, ref string, principal principalmodel.Principal) (AgentReportExportAudit, error) {
	var value AgentReportExportAudit
	err := s.loadReportState(ctx, "report_export_audit", ref, principal, &value, "agent_dialog.report_export_audit_not_found")
	return value, err
}
func (s *AgentApplicationService) GetReportDownloadTask(ctx context.Context, ref string, principal principalmodel.Principal) (AgentReportDownloadTask, error) {
	var value AgentReportDownloadTask
	err := s.loadReportState(ctx, "report_download_task", ref, principal, &value, "agent_dialog.download_task_not_found")
	return value, err
}
func (s *AgentApplicationService) PrepareReportHandoff(ctx context.Context, ref string, principal principalmodel.Principal) (AgentReportHandoff, error) {
	workspaceID, err := agentCommandWorkspace(principal)
	if err != nil {
		return AgentReportHandoff{}, err
	}
	query, err := s.GetReportQueryRun(ctx, ref, principal)
	if err != nil {
		return AgentReportHandoff{}, notFound("agent_dialog.report_download_handoff_not_found")
	}
	audit, err := s.GetReportExportAudit(ctx, ref, principal)
	if err != nil {
		return AgentReportHandoff{}, notFound("agent_dialog.report_download_handoff_not_found")
	}
	task, err := s.GetReportDownloadTask(ctx, ref, principal)
	if err != nil {
		return AgentReportHandoff{}, notFound("agent_dialog.report_download_handoff_not_found")
	}
	now := time.Now().UTC().UnixNano()
	audit.Status, audit.PreparedAt, audit.PreparedBy, audit.NextStep = "prepared_for_report_center", now, principal.UserID, "Open the report center export flow with this audit id before producing a downloadable file."
	task.Status, task.PreparedAt, task.PreparedBy, task.NextStep = "prepared_for_report_center", now, principal.UserID, "Use the report center download task to create the file, scan it, authorize access, and record download audit."
	key := agentStateKey(workspaceID, principal.UserID, principal.RoleKey, agentStatePart(ref))
	ap, _ := json.Marshal(audit)
	tp, _ := json.Marshal(task)
	if err := s.repository.PutBatch(ctx, workspaceID, []agentmodel.AgentStateRecord{{Kind: "report_export_audit", Key: key, WorkspaceID: workspaceID, UserID: principal.UserID, RoleKey: principal.RoleKey, Payload: ap, UpdatedAt: now}, {Kind: "report_download_task", Key: key, WorkspaceID: workspaceID, UserID: principal.UserID, RoleKey: principal.RoleKey, Payload: tp, UpdatedAt: now}}); err != nil {
		return AgentReportHandoff{}, err
	}
	return AgentReportHandoff{QueryRun: query, ExportAudit: audit, DownloadTask: task}, nil
}
func (s *AgentApplicationService) loadReportState(ctx context.Context, kind, ref string, principal principalmodel.Principal, target any, code string) error {
	workspaceID, err := agentQueryWorkspace(principal)
	if err != nil {
		return err
	}
	state, ok, err := s.repository.Get(ctx, workspaceID, kind, agentStateKey(workspaceID, principal.UserID, principal.RoleKey, agentStatePart(ref)))
	if err != nil {
		return err
	}
	if !ok || json.Unmarshal(state.Payload, target) != nil {
		return notFound(code)
	}
	return nil
}
