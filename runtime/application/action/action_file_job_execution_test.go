package action

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestOpenVerifiedFileRequiresExactReadableRecordBinding(t *testing.T) {
	openCalls := 0
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{
			GetRecord: func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
				return recordmodel.Record{ID: "source-1", Data: map[string]any{"file_id": "file-other"}}, nil
			},
			OpenVerifiedFile: func(context.Context, string, runtimeext.VerifiedFileRequest) (runtimeext.VerifiedFile, error) {
				openCalls++
				return runtimeext.VerifiedFile{}, nil
			},
		},
		invocation: actionmodel.ActionInvocation{Principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}},
		action:     definitionmodel.ActionSchema{Key: "document.process", EffectSet: &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{{ObjectKey: "document_source"}}}},
		workspace:  runtimeext.Workspace{ID: "workspace-a"}, unitOfWork: newActionTestUnitOfWork(), fileGrants: []string{runtimeext.FileOperationOpenVerified},
	}
	_, err := execution.OpenVerifiedFile(t.Context(), runtimeext.VerifiedFileRequest{
		FileVerificationRequest: runtimeext.FileVerificationRequest{FileID: "file-1", ContentSHA256: "sha", ScanReceipt: "receipt"},
		Binding:                 runtimeext.FileRecordBinding{ObjectKey: "document_source", RecordID: "source-1", FileIDField: "file_id"},
	})
	if apperror.CodeOf(err) != "backend.upload.file_record_binding_denied" || openCalls != 0 {
		t.Fatalf("code=%q open calls=%d err=%v", apperror.CodeOf(err), openCalls, err)
	}
}

func TestIssueFileDownloadRequiresGrantAndExactReadableRecordBinding(t *testing.T) {
	issueCalls := 0
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{
			GetRecord: func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error) {
				return recordmodel.Record{ID: "source-1", Data: map[string]any{"file_id": "file-1"}}, nil
			},
			IssueFileDownload: func(_ context.Context, workspaceID string, principal runtimeext.Principal, request runtimeext.FileDownloadRequest) (runtimeext.FileDownloadTicket, error) {
				issueCalls++
				if workspaceID != "workspace-a" || principal.UserID != "user-a" || request.Binding.RecordID != "source-1" {
					t.Fatalf("unexpected issue request: workspace=%q principal=%+v request=%+v", workspaceID, principal, request)
				}
				return runtimeext.FileDownloadTicket{ProtectedDownload: "/uploads/file-1?download_ticket=token"}, nil
			},
		},
		principal:  runtimeext.Principal{Known: true, UserID: "user-a", AuthorizationRevision: "auth-1"},
		invocation: actionmodel.ActionInvocation{Principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user-a", WorkspaceID: "workspace-a"}}},
		action:     definitionmodel.ActionSchema{Key: "document.query", EffectSet: &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{{ObjectKey: "document_source"}}}},
		workspace:  runtimeext.Workspace{ID: "workspace-a"}, unitOfWork: newActionTestUnitOfWork(),
	}
	request := runtimeext.FileDownloadRequest{
		FileVerificationRequest: runtimeext.FileVerificationRequest{FileID: "file-1", ContentSHA256: "sha", ScanReceipt: "receipt"},
		Binding:                 runtimeext.FileRecordBinding{ObjectKey: "document_source", RecordID: "source-1", FileIDField: "file_id"},
	}
	if _, err := execution.IssueFileDownload(t.Context(), request); apperror.CodeOf(err) != runtimeext.FileActionGrantDeniedErrorCode || issueCalls != 0 {
		t.Fatalf("missing grant code=%q issue calls=%d err=%v", apperror.CodeOf(err), issueCalls, err)
	}
	execution.fileGrants = []string{runtimeext.FileOperationIssueDownload}
	ticket, err := execution.IssueFileDownload(t.Context(), request)
	if err != nil || ticket.ProtectedDownload == "" || issueCalls != 1 {
		t.Fatalf("ticket=%+v issue calls=%d err=%v", ticket, issueCalls, err)
	}
	request.FileID = "other-file"
	if _, err := execution.IssueFileDownload(t.Context(), request); apperror.CodeOf(err) != "backend.upload.file_record_binding_denied" || issueCalls != 1 {
		t.Fatalf("mismatch code=%q issue calls=%d err=%v", apperror.CodeOf(err), issueCalls, err)
	}
}

func TestRunBusinessJobRequiresGrantAndBusinessMutation(t *testing.T) {
	stageCalls := 0
	newExecution := func(granted bool) *businessActionExecution {
		grants := []string(nil)
		if granted {
			grants = []string{runtimeext.FileOperationRunJob}
		}
		return &businessActionExecution{
			dependencies: BusinessHandlerExecutionDependencies{StageBusinessJob: func(context.Context, string, runtimeext.BusinessJobRequest) (transactionmodel.RecordMutationCommit, runtimeext.BusinessJobReceipt, error) {
				stageCalls++
				commit := transactionmodel.RecordMutationCommit{Operation: "create", Object: definitionmodel.ObjectSchema{Key: "record_timer"}, Record: recordmodel.Record{ID: "job-1"}, RecordID: "job-1"}
				return commit, runtimeext.BusinessJobReceipt{JobID: "job-1"}, nil
			}},
			workspace: runtimeext.Workspace{ID: "workspace-a"}, unitOfWork: newActionTestUnitOfWork(), fileGrants: grants,
		}
	}
	request := runtimeext.BusinessJobRequest{JobKey: "import", ObjectKey: "document_import", RecordID: "import-1", ActionKey: "document.process_import", Payload: json.RawMessage(`{}`)}
	denied := newExecution(false)
	if _, err := denied.RunBusinessJob(t.Context(), request); apperror.CodeOf(err) != runtimeext.FileActionGrantDeniedErrorCode || stageCalls != 0 {
		t.Fatalf("code=%q stage calls=%d err=%v", apperror.CodeOf(err), stageCalls, err)
	}
	granted := newExecution(true)
	receipt, err := granted.RunBusinessJob(t.Context(), request)
	if err != nil || receipt.JobID != "job-1" || stageCalls != 1 {
		t.Fatalf("receipt=%+v stage calls=%d err=%v", receipt, stageCalls, err)
	}
	if _, err := granted.canonicalCommits(); apperror.CodeOf(err) != "backend.business_job.requires_business_mutation" {
		t.Fatalf("expected mutation requirement, code=%q err=%v", apperror.CodeOf(err), err)
	}
	granted.setCommits = []transactionmodel.RecordMutationCommit{{Operation: "update", Object: definitionmodel.ObjectSchema{Key: "document_import"}, Record: recordmodel.Record{ID: "import-1"}, RecordID: "import-1"}}
	commits, err := granted.canonicalCommits()
	if err != nil || len(commits) != 2 || commits[1].Record.ID != "job-1" {
		t.Fatalf("commits=%+v err=%v", commits, err)
	}
}
