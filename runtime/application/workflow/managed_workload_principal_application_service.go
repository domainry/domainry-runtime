package workflow

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// ManagedWorkloadExecution identifies one durable execution against the
// authorization version embedded in its owner target.
type ManagedWorkloadExecution struct {
	WorkloadKey         string
	DefinitionVersionID string
	DefinitionVersion   int
	RoleKey             string
	ExecutionID         string
	SourceEventID       string
	IdempotencyKey      string
}

// ResolveManagedWorkloadPrincipal asks Identity for the active managed
// workload principal. Runtime never constructs a Role-backed principal from
// manifest data and never falls back to an installation system principal.
func (s *WorkflowApplicationService) ResolveManagedWorkloadPrincipal(ctx context.Context, execution ManagedWorkloadExecution) (principalmodel.Principal, error) {
	key := strings.TrimSpace(execution.WorkloadKey)
	roleKey := strings.TrimSpace(execution.RoleKey)
	if s == nil || s.principals == nil || s.workloadReleases == nil {
		return principalmodel.Principal{}, apperror.New(apperror.KindUnavailable, "backend.workload.execution_principal_unavailable", nil, nil)
	}
	binding, found := s.workloadReleases.managedBinding(key, execution.DefinitionVersionID, execution.DefinitionVersion)
	if !found || binding.RoleKey != roleKey {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workload.release_not_active", nil, map[string]string{"workload": key, "role": roleKey})
	}
	workload := identitysdk.WorkflowWorkloadResolution{
		WorkflowKey: key, DefinitionVersionID: binding.DefinitionVersionID, DefinitionVersion: binding.DefinitionVersion,
		ReleaseID: binding.ReleaseID, ReleaseDigest: binding.ReleaseDigest, TaskID: strings.TrimSpace(execution.ExecutionID), SourceEventID: strings.TrimSpace(execution.SourceEventID),
	}
	application := s.workloadReleases.Application()
	resolution, err := s.principals.Resolve(requestcontext.WithWorkspaceID(ctx, string(application.WorkspaceID)), identitysdk.PrincipalResolutionRequest{
		SubjectID: identitysdk.WorkflowWorkloadSubjectID(key), RoleKey: roleKey, Workload: &workload,
	})
	if err != nil {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workload.execution_principal_denied", err, map[string]string{"workload": key, "role": roleKey})
	}
	resolution.Principal.AccessBundle = &resolution.AccessBundle
	principal := principalmodel.NewPrincipalFromIdentity(resolution.Principal, strings.TrimSpace(execution.IdempotencyKey))
	expectedSubject := string(identitysdk.WorkflowWorkloadSubjectID(key))
	if !principal.Known || principal.WorkspaceID != string(application.WorkspaceID) || principal.UserID != expectedSubject || principal.RoleKey != roleKey || principal.Workload == nil || principal.Workload.WorkflowKey != key || principal.Workload.DefinitionVersionID != binding.DefinitionVersionID || principal.Workload.DefinitionVersion != binding.DefinitionVersion || principal.Workload.ReleaseID != binding.ReleaseID || principal.Workload.ReleaseDigest != binding.ReleaseDigest || principal.Workload.TaskID != workload.TaskID || principal.Workload.SourceEventID != workload.SourceEventID || !principal.HasAllPermissions(binding.ActionKeys) {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workload.execution_principal_mismatch", nil, map[string]string{"workload": key, "role": roleKey})
	}
	principal.RequestID = strings.TrimSpace(execution.IdempotencyKey)
	if principal.RequestID == "" {
		principal.RequestID = strings.TrimSpace(execution.ExecutionID)
	}
	return principal, nil
}
