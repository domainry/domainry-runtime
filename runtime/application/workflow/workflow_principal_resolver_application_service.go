package workflow

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

type WorkflowPrincipalResolver struct {
	principals identitysdk.PrincipalResolver
}

func NewWorkflowPrincipalResolver(principals identitysdk.PrincipalResolver) *WorkflowPrincipalResolver {
	return &WorkflowPrincipalResolver{principals: principals}
}

// ResolveWorkflowPrincipal resolves run_as through Identity. Runtime never
// derives permissions from actions or mints a RoleSchema-backed principal.
func (r *WorkflowPrincipalResolver) ResolveWorkflowPrincipal(ctx context.Context, workflow definitionmodel.WorkflowSchema, initiator principalmodel.Principal) (principalmodel.Principal, error) {
	runAs := workflowpolicy.WorkflowRunAs(workflow)
	if runAs == "" {
		return initiator, nil
	}
	if r == nil || r.principals == nil {
		return principalmodel.Principal{}, apperror.New(apperror.KindUnavailable, "backend.workflow.execution_principal_unavailable", nil, nil)
	}

	// The subject name is only a lookup key. Identity must contain this service
	// account, its role assignment and the resulting AccessBundle; otherwise
	// workflow execution fails closed.
	subjectID := "workflow:" + strings.TrimSpace(workflow.Key)
	resolution, err := r.principals.Resolve(ctx, identitysdk.PrincipalResolutionRequest{Application: identitysdk.ApplicationScope{WorkspaceID: identitysdk.WorkspaceID(initiator.WorkspaceID)}, SubjectID: identitysdk.SubjectID(subjectID), RoleKey: runAs})
	if err != nil {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workflow.execution_principal_denied", err, map[string]string{"workflow": strings.TrimSpace(workflow.Key), "role": runAs})
	}
	resolution.Principal.AccessBundle = &resolution.AccessBundle
	resolved := principalmodel.NewPrincipalFromIdentity(resolution.Principal, "")
	if !resolved.Known || resolved.AccessBundle == nil {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workflow.execution_principal_denied", nil, map[string]string{"workflow": strings.TrimSpace(workflow.Key), "role": runAs})
	}
	if workspaceID := strings.TrimSpace(initiator.WorkspaceID); workspaceID != "" && strings.TrimSpace(resolved.WorkspaceID) != workspaceID {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workflow.execution_principal_workspace_mismatch", nil, nil)
	}
	resolved.RequestID = initiator.RequestID
	resolved.CorrelationID = initiator.CorrelationID
	resolved.CausationID = initiator.CausationID
	return resolved, nil
}
