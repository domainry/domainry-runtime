package action

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func (e *businessActionExecution) uploadFieldPolicy(objectKey, fieldKey string, write bool) (recordmodel.RecordFileFieldPolicy, error) {
	allowed := false
	for _, grant := range e.objectGrants {
		if grant.ObjectKey != objectKey {
			continue
		}
		for _, operation := range grant.Operations {
			if write && (operation == runtimeext.ObjectCapabilityCreate || operation == runtimeext.ObjectCapabilityUpdate || operation == runtimeext.ObjectCapabilityConditionalUpdate) {
				allowed = true
			}
			if !write && (operation == runtimeext.ObjectCapabilityGet || operation == runtimeext.ObjectCapabilityList || operation == runtimeext.ObjectCapabilityGetForUpdate) {
				allowed = true
			}
		}
	}
	if !allowed {
		return recordmodel.RecordFileFieldPolicy{}, apperror.New(apperror.KindForbidden, runtimeext.FileActionGrantDeniedErrorCode, nil, nil)
	}
	if e.dependencies.ObjectForKey == nil {
		return recordmodel.RecordFileFieldPolicy{}, missingExecutorPort("file_field_catalog")
	}
	object, found := e.dependencies.ObjectForKey(objectKey)
	if found {
		for _, field := range object.Fields {
			if field.Key == fieldKey {
				policy, err := recordmodel.RecordFileFieldPolicyFor(field)
				if err == nil && policy.ScanRequired {
					return policy, nil
				}
				return recordmodel.RecordFileFieldPolicy{}, apperror.New(apperror.KindBadRequest, "backend.upload.field_policy_invalid", err, nil)
			}
		}
	}
	return recordmodel.RecordFileFieldPolicy{}, apperror.New(apperror.KindBadRequest, "backend.upload.field_not_defined", nil, nil)
}

func (e *businessActionExecution) IngestUploadedFile(ctx context.Context, request runtimeext.UploadedFileRequest) (runtimeext.FileVerificationEvidence, error) {
	if !e.hasFileGrant(runtimeext.FileOperationIngestUpload) {
		return runtimeext.FileVerificationEvidence{}, apperror.New(apperror.KindForbidden, runtimeext.FileActionGrantDeniedErrorCode, nil, nil)
	}
	request.ObjectKey, request.FieldKey = strings.TrimSpace(request.ObjectKey), strings.TrimSpace(request.FieldKey)
	policy, err := e.uploadFieldPolicy(request.ObjectKey, request.FieldKey, true)
	if err != nil {
		return runtimeext.FileVerificationEvidence{}, err
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" || e.identity.ExecutionID == "" || request.Content == nil {
		return runtimeext.FileVerificationEvidence{}, apperror.New(apperror.KindBadRequest, "backend.upload.ingest_request_invalid", nil, nil)
	}
	if e.dependencies.IngestUploadedFile == nil {
		return runtimeext.FileVerificationEvidence{}, missingExecutorPort("ingest_uploaded_file")
	}
	request.IdempotencyKey = e.identity.ExecutionID + ":" + strings.TrimSpace(request.IdempotencyKey)
	return e.dependencies.IngestUploadedFile(e.unitOfWork.executionContext(ctx), e.workspace.ID, e.invocation.Principal, request, policy)
}

func (e *businessActionExecution) InspectUploadedFile(ctx context.Context, request runtimeext.UploadedFileInspection) (runtimeext.FileVerificationEvidence, error) {
	if !e.hasFileGrant(runtimeext.FileOperationInspectUpload) {
		return runtimeext.FileVerificationEvidence{}, apperror.New(apperror.KindForbidden, runtimeext.FileActionGrantDeniedErrorCode, nil, nil)
	}
	request.ObjectKey, request.FieldKey = strings.TrimSpace(request.ObjectKey), strings.TrimSpace(request.FieldKey)
	if _, err := e.uploadFieldPolicy(request.ObjectKey, request.FieldKey, false); err != nil {
		return runtimeext.FileVerificationEvidence{}, err
	}
	if e.dependencies.InspectUploadedFile == nil {
		return runtimeext.FileVerificationEvidence{}, missingExecutorPort("inspect_uploaded_file")
	}
	return e.dependencies.InspectUploadedFile(e.unitOfWork.executionContext(ctx), e.workspace.ID, e.invocation.Principal, request)
}
