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
	releases   *WorkflowWorkloadReleaseState
}

func NewWorkflowPrincipalResolver(principals identitysdk.PrincipalResolver, releases ...*WorkflowWorkloadReleaseState) *WorkflowPrincipalResolver {
	resolver := &WorkflowPrincipalResolver{principals: principals}
	if len(releases) > 0 {
		resolver.releases = releases[0]
	}
	return resolver
}

type WorkflowPrincipalExecutionContext struct {
	TaskID             string
	SourceEventID      string
	InitiatorSubjectID string
}

// ResolveWorkflowPrincipal resolves run_as through Identity. Runtime never
// derives permissions from actions or mints a RoleSchema-backed principal.
func (r *WorkflowPrincipalResolver) ResolveWorkflowPrincipal(ctx context.Context, workflow definitionmodel.WorkflowSchema, initiator principalmodel.Principal) (principalmodel.Principal, error) {
	return r.ResolveWorkflowPrincipalForExecution(ctx, workflow, initiator, WorkflowPrincipalExecutionContext{})
}

func (r *WorkflowPrincipalResolver) ResolveWorkflowPrincipalForExecution(ctx context.Context, workflow definitionmodel.WorkflowSchema, initiator principalmodel.Principal, execution WorkflowPrincipalExecutionContext) (principalmodel.Principal, error) {
	runAs := workflowpolicy.WorkflowRunAs(workflow)
	if runAs == "" {
		return initiator, nil
	}
	if r == nil || r.principals == nil {
		return principalmodel.Principal{}, apperror.New(apperror.KindUnavailable, "backend.workflow.execution_principal_unavailable", nil, nil)
	}

	// The subject name is only a lookup key. Identity derives the non-human
	// principal from the active release binding and service role; it never
	// creates a user or a user-role assignment for a Workflow.
	subjectID := "workflow:" + strings.TrimSpace(workflow.Key)
	request := identitysdk.PrincipalResolutionRequest{Application: identitysdk.ApplicationScope{WorkspaceID: identitysdk.WorkspaceID(initiator.WorkspaceID)}, SubjectID: identitysdk.SubjectID(subjectID), RoleKey: runAs}
	var expectedWorkload *identitysdk.WorkflowWorkloadResolution
	if r.releases != nil && r.releases.Configured() {
		workload, found := r.releases.Resolution(workflow)
		if !found {
			return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workflow.workload_release_not_active", nil, map[string]string{"workflow": strings.TrimSpace(workflow.Key), "role": runAs})
		}
		workload.TaskID = strings.TrimSpace(execution.TaskID)
		workload.SourceEventID = strings.TrimSpace(execution.SourceEventID)
		initiatorSubjectID := strings.TrimSpace(execution.InitiatorSubjectID)
		if initiatorSubjectID == "" {
			initiatorSubjectID = strings.TrimSpace(initiator.UserID)
		}
		workload.InitiatorSubjectID = identitysdk.SubjectID(initiatorSubjectID)
		request.Application = r.releases.Application()
		request.Workload = &workload
		expectedWorkload = &workload
	}
	resolution, err := r.principals.Resolve(ctx, request)
	if err != nil {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workflow.execution_principal_denied", err, map[string]string{"workflow": strings.TrimSpace(workflow.Key), "role": runAs})
	}
	resolution.Principal.AccessBundle = &resolution.AccessBundle
	resolved := principalmodel.NewPrincipalFromIdentity(resolution.Principal, "")
	if !resolved.Known || resolved.AccessBundle == nil {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workflow.execution_principal_denied", nil, map[string]string{"workflow": strings.TrimSpace(workflow.Key), "role": runAs})
	}
	if expectedWorkload != nil && (resolved.UserID != subjectID || resolved.RoleKey != runAs || resolved.Workload == nil || resolved.Workload.WorkflowKey != expectedWorkload.WorkflowKey || resolved.Workload.DefinitionVersionID != expectedWorkload.DefinitionVersionID || resolved.Workload.DefinitionVersion != expectedWorkload.DefinitionVersion || resolved.Workload.ReleaseID != expectedWorkload.ReleaseID || resolved.Workload.ReleaseDigest != expectedWorkload.ReleaseDigest || resolved.Workload.TaskID != expectedWorkload.TaskID || resolved.Workload.SourceEventID != expectedWorkload.SourceEventID || resolved.Workload.InitiatorSubjectID != expectedWorkload.InitiatorSubjectID) {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workflow.execution_principal_mismatch", nil, map[string]string{"workflow": strings.TrimSpace(workflow.Key), "role": runAs})
	}
	if workspaceID := strings.TrimSpace(initiator.WorkspaceID); workspaceID != "" && strings.TrimSpace(resolved.WorkspaceID) != workspaceID {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workflow.execution_principal_workspace_mismatch", nil, nil)
	}
	resolved.RequestID = strings.TrimSpace(initiator.RequestID)
	if resolved.RequestID == "" {
		// Durable workers rehydrate the process without the original HTTP
		// request context. Give each workflow task a stable invocation identity
		// so project Actions can require a non-empty request ID without falling
		// back to a human or system user session.
		resolved.RequestID = strings.TrimSpace(execution.TaskID)
	}
	resolved.CorrelationID = initiator.CorrelationID
	resolved.CausationID = initiator.CausationID
	return resolved, nil
}
