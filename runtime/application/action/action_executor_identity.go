package action

import (
	"context"
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
	granted := false
	for _, operation := range e.fileGrants {
		granted = granted || operation == runtimeext.FileOperationVerifyClean
	}
	if !granted {
		return runtimeext.FileVerificationEvidence{}, apperror.New(apperror.KindForbidden, runtimeext.FileActionGrantDeniedErrorCode, nil, nil)
	}
	if e.dependencies.VerifyFileClean == nil {
		return runtimeext.FileVerificationEvidence{}, missingExecutorPort("verify_file_clean")
	}
	return e.dependencies.VerifyFileClean(e.unitOfWork.executionContext(ctx), e.workspace.ID, request)
}
