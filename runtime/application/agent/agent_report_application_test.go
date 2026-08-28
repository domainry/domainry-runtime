package agent

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestAgentReportApplicationOwnsAtomicGovernanceAndHandoff(t *testing.T) {
	application := NewAgentApplicationService(NewAgentMemoryStateRepository())
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}, accessfixture.Bundle{Key: "admin"})
	metadata, err := application.RecordReportGovernance(t.Context(), AgentReportGovernanceRequest{QueryRef: "query-1", ReportKey: "revenue", ObjectKey: "order", ExecutionMode: "server", RowCount: 2, Total: 2, AuditEventKey: "audit-1"}, principal)
	if err != nil || metadata["download_task_id"] != "query-1" {
		t.Fatalf("metadata=%#v err=%v", metadata, err)
	}
	query, err := application.GetReportQueryRun(t.Context(), "query-1", principal)
	if err != nil || query.Status != "completed" {
		t.Fatalf("query=%#v err=%v", query, err)
	}
	handoff, err := application.PrepareReportHandoff(t.Context(), "query-1", principal)
	if err != nil || handoff.ExportAudit.Status != "prepared_for_report_center" || handoff.DownloadTask.Status != "prepared_for_report_center" {
		t.Fatalf("handoff=%#v err=%v", handoff, err)
	}
	other := principal
	other.UserID = "user-b"
	if _, err := application.GetReportDownloadTask(t.Context(), "query-1", other); errorKind(err) != apperror.KindNotFound {
		t.Fatalf("cross-user task err=%v", err)
	}
}
