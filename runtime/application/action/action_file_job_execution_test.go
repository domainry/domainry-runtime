package action

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
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

func TestCreateDerivedFilePassesTheInvocationPrincipal(t *testing.T) {
	expected := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user-a", WorkspaceID: "workspace-a"}}
	var received principalmodel.Principal
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{CreateDerivedFile: func(_ context.Context, workspaceID string, principal principalmodel.Principal, request runtimeext.DerivedFileRequest) (runtimeext.DerivedFileEvidence, error) {
			if workspaceID != expected.WorkspaceID || request.IdempotencyKey != "attachment-1" {
				t.Fatalf("unexpected derived file request: workspace=%q request=%+v", workspaceID, request)
			}
			received = principal
			return runtimeext.DerivedFileEvidence{FileVerificationEvidence: runtimeext.FileVerificationEvidence{FileID: "derived-1"}}, nil
		}},
		invocation: actionmodel.ActionInvocation{Principal: expected},
		workspace:  runtimeext.Workspace{ID: expected.WorkspaceID}, unitOfWork: newActionTestUnitOfWork(), fileGrants: []string{runtimeext.FileOperationCreateDerived},
	}
	created, err := execution.CreateDerivedFile(t.Context(), runtimeext.DerivedFileRequest{IdempotencyKey: "attachment-1", ObjectKey: "attachment", FieldKey: "file_id", Filename: "source.txt", ContentType: "text/plain", Content: strings.NewReader("source")})
	if err != nil {
		t.Fatal(err)
	}
	if created.FileID != "derived-1" || !reflect.DeepEqual(received, expected) {
		t.Fatalf("created=%+v principal=%+v", created, received)
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

func TestRunBusinessJobRejectsInvalidOrUnboundedRequestsBeforeStaging(t *testing.T) {
	stageCalls := 0
	execution := &businessActionExecution{
		dependencies: BusinessHandlerExecutionDependencies{StageBusinessJob: func(context.Context, string, runtimeext.BusinessJobRequest) (transactionmodel.RecordMutationCommit, runtimeext.BusinessJobReceipt, error) {
			stageCalls++
			return transactionmodel.RecordMutationCommit{}, runtimeext.BusinessJobReceipt{}, nil
		}},
		workspace: runtimeext.Workspace{ID: "workspace-a"}, unitOfWork: newActionTestUnitOfWork(), fileGrants: []string{runtimeext.FileOperationRunJob},
	}
	for _, request := range []runtimeext.BusinessJobRequest{
		{JobKey: "job", ObjectKey: "object", RecordID: "record", ActionKey: "action", Payload: json.RawMessage(`[]`)},
		{JobKey: "job", ObjectKey: "object", RecordID: "record", ActionKey: "action", Payload: json.RawMessage(`null`)},
		{JobKey: "job", ObjectKey: "object", RecordID: "record", ActionKey: "action", Payload: json.RawMessage(`{"valid":true}`), MaxAttempts: 101},
		{JobKey: "job", ObjectKey: "object", RecordID: "record", ActionKey: "action", Payload: json.RawMessage(`{"valid":true}`), RetryDelaySeconds: 10, RetryMaxDelaySeconds: 5},
		{JobKey: "job", ObjectKey: "object", RecordID: "record", ActionKey: "action", Payload: json.RawMessage(`{"value":"` + strings.Repeat("x", runtimeext.MaximumBusinessJobPayloadBytes) + `"}`)},
	} {
		if _, err := execution.RunBusinessJob(t.Context(), request); apperror.CodeOf(err) != "backend.business_job.request_invalid" {
			t.Fatalf("request=%+v code=%q err=%v", request, apperror.CodeOf(err), err)
		}
	}
	if stageCalls != 0 {
		t.Fatalf("invalid requests reached staging: calls=%d", stageCalls)
	}
}
