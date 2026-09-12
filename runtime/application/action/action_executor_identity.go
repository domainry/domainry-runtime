package action

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func (e *BusinessHandlerExecutor) ValidationErrors() []error {
	if e == nil {
		return []error{fmt.Errorf("business handler executor is required")}
	}
	errors := []error{}
	if e.dependencies.RuntimeRevision == "" {
		errors = append(errors, fmt.Errorf("business handler Runtime revision is required"))
	}
	if e.dependencies.ProjectRevision == "" {
		errors = append(errors, fmt.Errorf("business handler Project revision is required"))
	}
	if e.dependencies.ApplicationSchemaRevision == "" && e.dependencies.ResolveMetadataRevision == nil && e.dependencies.ResolveApplicationConfiguration == nil {
		errors = append(errors, fmt.Errorf("business handler Metadata revision resolver is required"))
	}
	return errors
}

func (e *BusinessHandlerExecutor) executionIdentity(ctx context.Context, invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema, descriptor runtimeext.HandlerDescriptor, executionID string) (runtimeext.ExecutionIdentity, error) {
	metadataRevision := e.dependencies.ApplicationSchemaRevision
	if e.dependencies.ResolveMetadataRevision != nil {
		resolved, err := e.dependencies.ResolveMetadataRevision(ctx, invocation.Principal)
		if err != nil {
			return runtimeext.ExecutionIdentity{}, apperror.New(apperror.KindInternal, "backend.action.metadata_revision_unavailable", err, map[string]string{"action": action.Key})
		}
		metadataRevision = strings.TrimSpace(resolved)
	}
	return e.executionIdentityWithRevision(invocation, action, descriptor, executionID, metadataRevision)
}

func (e *BusinessHandlerExecutor) executionIdentityWithRevision(invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema, descriptor runtimeext.HandlerDescriptor, executionID, metadataRevision string) (runtimeext.ExecutionIdentity, error) {
	metadataRevision = strings.TrimSpace(metadataRevision)
	if e.dependencies.RuntimeRevision == "" || e.dependencies.ProjectRevision == "" || metadataRevision == "" {
		return runtimeext.ExecutionIdentity{}, apperror.New(apperror.KindInternal, "backend.action.execution_identity_incomplete", nil, map[string]string{"action": action.Key})
	}
	return runtimeext.ExecutionIdentity{
		ExecutionID: executionID, ReceiptID: executionID, ActionKey: action.Key, ObjectKey: action.ObjectKey, RecordID: invocation.RecordID,
		IdempotencyKey: invocation.IdempotencyKey, RuntimeRevision: e.dependencies.RuntimeRevision, ApplicationSchemaRevision: metadataRevision,
		ProjectRevision: e.dependencies.ProjectRevision, HandlerRevision: descriptor.HandlerRevision,
	}, nil
}

func (e *businessActionExecution) Phase() runtimeext.ExecutionPhase {
	return e.unitOfWork.phases.current()
}

func (e *businessActionExecution) VerifyFileClean(ctx context.Context, request runtimeext.FileVerificationRequest) (runtimeext.FileVerificationEvidence, error) {
	if !e.hasFileGrant(runtimeext.FileOperationVerifyClean) {
		return runtimeext.FileVerificationEvidence{}, apperror.New(apperror.KindForbidden, runtimeext.FileActionGrantDeniedErrorCode, nil, nil)
	}
	if e.dependencies.VerifyFileClean == nil {
		return runtimeext.FileVerificationEvidence{}, missingExecutorPort("verify_file_clean")
	}
	return e.dependencies.VerifyFileClean(e.unitOfWork.executionContext(ctx), e.workspace.ID, request)
}

func (e *businessActionExecution) OpenVerifiedFile(ctx context.Context, request runtimeext.VerifiedFileRequest) (runtimeext.VerifiedFile, error) {
	if !e.hasFileGrant(runtimeext.FileOperationOpenVerified) {
		return runtimeext.VerifiedFile{}, apperror.New(apperror.KindForbidden, runtimeext.FileActionGrantDeniedErrorCode, nil, nil)
	}
	binding := request.Binding
	objectKey, recordID, fieldKey := strings.TrimSpace(binding.ObjectKey), strings.TrimSpace(binding.RecordID), strings.TrimSpace(binding.FileIDField)
	if objectKey == "" || recordID == "" || fieldKey == "" || strings.TrimSpace(request.FileID) == "" || strings.TrimSpace(request.ContentSHA256) == "" || strings.TrimSpace(request.ScanReceipt) == "" {
		return runtimeext.VerifiedFile{}, apperror.New(apperror.KindBadRequest, "backend.upload.file_open_request_invalid", nil, nil)
	}
	result, err := e.QueryRecords(ctx, runtimeext.RecordQuery{Operation: runtimeext.QueryGet, ObjectKey: objectKey, RecordID: recordID})
	if err != nil {
		return runtimeext.VerifiedFile{}, err
	}
	if len(result.Records) != 1 || strings.TrimSpace(fmt.Sprint(result.Records[0].Fields[fieldKey])) != strings.TrimSpace(request.FileID) {
		return runtimeext.VerifiedFile{}, apperror.New(apperror.KindForbidden, "backend.upload.file_record_binding_denied", nil, map[string]string{"object": objectKey, "record_id": recordID, "field": fieldKey})
	}
	if e.dependencies.OpenVerifiedFile == nil {
		return runtimeext.VerifiedFile{}, missingExecutorPort("open_verified_file")
	}
	return e.dependencies.OpenVerifiedFile(e.unitOfWork.executionContext(ctx), e.workspace.ID, request)
}

func (e *businessActionExecution) CreateDerivedFile(ctx context.Context, request runtimeext.DerivedFileRequest) (runtimeext.DerivedFileEvidence, error) {
	if !e.hasFileGrant(runtimeext.FileOperationCreateDerived) {
		return runtimeext.DerivedFileEvidence{}, apperror.New(apperror.KindForbidden, runtimeext.FileActionGrantDeniedErrorCode, nil, nil)
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" || strings.TrimSpace(request.ObjectKey) == "" || strings.TrimSpace(request.FieldKey) == "" || strings.TrimSpace(request.Filename) == "" || strings.TrimSpace(request.ContentType) == "" || request.Content == nil {
		return runtimeext.DerivedFileEvidence{}, apperror.New(apperror.KindBadRequest, "backend.upload.derived_file_request_invalid", nil, nil)
	}
	if e.dependencies.CreateDerivedFile == nil {
		return runtimeext.DerivedFileEvidence{}, missingExecutorPort("create_derived_file")
	}
	return e.dependencies.CreateDerivedFile(e.unitOfWork.executionContext(ctx), e.workspace.ID, request)
}

func (e *businessActionExecution) RunBusinessJob(ctx context.Context, request runtimeext.BusinessJobRequest) (runtimeext.BusinessJobReceipt, error) {
	if !e.hasFileGrant(runtimeext.FileOperationRunJob) {
		return runtimeext.BusinessJobReceipt{}, apperror.New(apperror.KindForbidden, runtimeext.FileActionGrantDeniedErrorCode, nil, nil)
	}
	request.JobKey, request.ObjectKey, request.RecordID, request.ActionKey = strings.TrimSpace(request.JobKey), strings.TrimSpace(request.ObjectKey), strings.TrimSpace(request.RecordID), strings.TrimSpace(request.ActionKey)
	if request.JobKey == "" || request.ObjectKey == "" || request.RecordID == "" || request.ActionKey == "" || len(request.Payload) == 0 || !json.Valid(request.Payload) || request.MaxAttempts < 0 || request.RetryDelaySeconds < 0 || request.RetryMaxDelaySeconds < 0 {
		return runtimeext.BusinessJobReceipt{}, apperror.New(apperror.KindBadRequest, "backend.business_job.request_invalid", nil, nil)
	}
	if e.dependencies.StageBusinessJob == nil {
		return runtimeext.BusinessJobReceipt{}, missingExecutorPort("stage_business_job")
	}
	if _, err := e.unitOfWork.beginDeferredWriting(ctx); err != nil {
		return runtimeext.BusinessJobReceipt{}, err
	}
	commit, receipt, err := e.dependencies.StageBusinessJob(e.unitOfWork.executionContext(ctx), e.workspace.ID, request)
	if err != nil {
		return runtimeext.BusinessJobReceipt{}, err
	}
	e.businessJobCommits = append(e.businessJobCommits, commit)
	return receipt, nil
}

func (e *businessActionExecution) hasFileGrant(operation string) bool {
	for _, candidate := range e.fileGrants {
		if strings.TrimSpace(candidate) == operation {
			return true
		}
	}
	return false
}
