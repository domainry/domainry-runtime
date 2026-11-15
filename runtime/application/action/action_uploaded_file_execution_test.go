package action

import (
	"context"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestUploadedFileExecutionRequiresExplicitFileAndObjectGrants(t *testing.T) {
	calls := 0
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "service-1", WorkspaceID: "workspace-a"}}
	object := definitionmodel.ObjectSchema{Key: "interaction", Fields: []definitionmodel.FieldSchema{{Key: "attachments", Type: recordmodel.RecordFileListFieldType}}}
	execution := &businessActionExecution{
		identity:  runtimeext.ExecutionIdentity{ExecutionID: "execution-a"},
		workspace: runtimeext.Workspace{ID: "workspace-a"}, invocation: actionmodel.ActionInvocation{Principal: principal}, unitOfWork: newActionTestUnitOfWork(),
		dependencies: BusinessHandlerExecutionDependencies{
			ObjectForKey: func(key string) (definitionmodel.ObjectSchema, bool) { return object, key == object.Key },
			IngestUploadedFile: func(_ context.Context, workspace string, actor principalmodel.Principal, request runtimeext.UploadedFileRequest, policy recordmodel.RecordFileFieldPolicy) (runtimeext.FileVerificationEvidence, error) {
				calls++
				if workspace != "workspace-a" || actor.UserID != "service-1" || request.IdempotencyKey != "execution-a:attachment" || !policy.ScanRequired {
					t.Fatalf("forged owner input: %+v %+v", actor, request)
				}
				return runtimeext.FileVerificationEvidence{FileID: "incoming", Status: "pending"}, nil
			},
			InspectUploadedFile: func(_ context.Context, workspace string, actor principalmodel.Principal, request runtimeext.UploadedFileInspection) (runtimeext.FileVerificationEvidence, error) {
				calls++
				if workspace != "workspace-a" || actor.UserID != "service-1" || request.FileID != "incoming" {
					t.Fatal("inspection lost runtime identity")
				}
				return runtimeext.FileVerificationEvidence{FileID: request.FileID}, nil
			},
		},
	}
	request := runtimeext.UploadedFileRequest{IdempotencyKey: "attachment", ObjectKey: "interaction", FieldKey: "attachments", Filename: "log.txt", ContentType: "text/plain", ContentSHA256: strings.Repeat("a", 64), Size: 1, Content: strings.NewReader("x")}
	if _, err := execution.IngestUploadedFile(t.Context(), request); apperror.CodeOf(err) != runtimeext.FileActionGrantDeniedErrorCode || calls != 0 {
		t.Fatal("missing upload grant reached owner")
	}
	execution.fileGrants = []string{runtimeext.FileOperationIngestUpload, runtimeext.FileOperationInspectUpload}
	if _, err := execution.IngestUploadedFile(t.Context(), request); apperror.CodeOf(err) != runtimeext.FileActionGrantDeniedErrorCode || calls != 0 {
		t.Fatal("missing object grant reached owner")
	}
	execution.objectGrants = []runtimeext.ActionObjectCapability{{ObjectKey: "interaction", Operations: []string{runtimeext.ObjectCapabilityCreate}}}
	if result, err := execution.IngestUploadedFile(t.Context(), request); err != nil || result.Status != "pending" || calls != 1 {
		t.Fatalf("upload=%+v %v", result, err)
	}
	request.ObjectKey = "secret_object"
	if _, err := execution.IngestUploadedFile(t.Context(), request); apperror.CodeOf(err) != runtimeext.FileActionGrantDeniedErrorCode || calls != 1 {
		t.Fatal("undeclared object reached upload owner")
	}
	inspection := runtimeext.UploadedFileInspection{FileID: "incoming", ObjectKey: "interaction", FieldKey: "attachments"}
	if _, err := execution.InspectUploadedFile(t.Context(), inspection); apperror.CodeOf(err) != runtimeext.FileActionGrantDeniedErrorCode || calls != 1 {
		t.Fatal("inspection lacks object read grant")
	}
	execution.objectGrants[0].Operations = append(execution.objectGrants[0].Operations, runtimeext.ObjectCapabilityGet)
	if _, err := execution.InspectUploadedFile(t.Context(), inspection); err != nil || calls != 2 {
		t.Fatal(err)
	}
	object.Fields[0].Type = "text"
	if _, err := execution.InspectUploadedFile(t.Context(), inspection); err == nil || calls != 2 {
		t.Fatal("text field accepted as upload target")
	}
}
