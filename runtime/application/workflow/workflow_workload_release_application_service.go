package workflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

// WorkflowWorkloadReleaseState is the in-process view of the one release that
// Identity currently accepts. Queued work must still name a version present in
// this state, so a replaced or disabled workflow cannot keep using an old
// workload binding after a metadata reload.
type WorkflowWorkloadReleaseState struct {
	mu          sync.RWMutex
	configured  bool
	application identitysdk.ApplicationScope
	bindings    map[string]identitysdk.WorkflowWorkloadBinding
}

func (s *WorkflowWorkloadReleaseState) Configured() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configured
}

func (s *WorkflowWorkloadReleaseState) configure(application identitysdk.ApplicationScope) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.configured, s.application = true, application
	s.mu.Unlock()
}

func NewWorkflowWorkloadReleaseState() *WorkflowWorkloadReleaseState {
	return &WorkflowWorkloadReleaseState{bindings: map[string]identitysdk.WorkflowWorkloadBinding{}}
}

func (s *WorkflowWorkloadReleaseState) Application() identitysdk.ApplicationScope {
	if s == nil {
		return identitysdk.ApplicationScope{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.application
}

func (s *WorkflowWorkloadReleaseState) Resolution(workflow definitionmodel.WorkflowSchema) (identitysdk.WorkflowWorkloadResolution, bool) {
	if s == nil {
		return identitysdk.WorkflowWorkloadResolution{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, found := s.bindings[strings.TrimSpace(workflow.Key)]
	if !found || binding.Status != identitysdk.WorkflowWorkloadBindingActive || binding.DefinitionVersionID != strings.TrimSpace(workflow.DefinitionVersionID) || binding.DefinitionVersion != workflowpolicy.WorkflowPublishedVersion(workflow) {
		return identitysdk.WorkflowWorkloadResolution{}, false
	}
	return identitysdk.WorkflowWorkloadResolution{
		WorkflowKey: binding.WorkflowKey, DefinitionVersionID: binding.DefinitionVersionID, DefinitionVersion: binding.DefinitionVersion,
		ReleaseID: binding.ReleaseID, ReleaseDigest: binding.ReleaseDigest,
	}, true
}

func (s *WorkflowWorkloadReleaseState) store(application identitysdk.ApplicationScope, bindings []identitysdk.WorkflowWorkloadBinding) {
	if s == nil {
		return
	}
	next := make(map[string]identitysdk.WorkflowWorkloadBinding, len(bindings))
	for _, binding := range bindings {
		if binding.Status == identitysdk.WorkflowWorkloadBindingActive {
			next[strings.TrimSpace(binding.WorkflowKey)] = binding
		}
	}
	s.mu.Lock()
	s.application, s.bindings = application, next
	s.mu.Unlock()
}

func (s *WorkflowWorkloadReleaseState) requestForRestore() identitysdk.ApplyWorkflowWorkloadBindingsRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	request := identitysdk.ApplyWorkflowWorkloadBindingsRequest{Application: s.application}
	for _, binding := range s.bindings {
		if request.ReleaseID == "" {
			request.ReleaseID, request.ReleaseDigest = binding.ReleaseID, binding.ReleaseDigest
		}
		request.Bindings = append(request.Bindings, identitysdk.WorkflowWorkloadBindingSpec{
			WorkflowKey: binding.WorkflowKey, DefinitionVersionID: binding.DefinitionVersionID, DefinitionVersion: binding.DefinitionVersion,
			RoleKey: binding.RoleKey, ActionKeys: append([]string(nil), binding.ActionKeys...),
		})
	}
	sort.Slice(request.Bindings, func(i, j int) bool { return request.Bindings[i].WorkflowKey < request.Bindings[j].WorkflowKey })
	if request.ReleaseID == "" {
		request.ReleaseDigest, _ = identitysdk.WorkflowWorkloadReleaseDigest(request.Bindings)
		request.ReleaseID = identitysdk.WorkflowWorkloadReleaseID(request.ReleaseDigest)
	}
	return request
}

// ConfigureWorkflowWorkloadIdentity binds the deployment control-plane port.
// It does not create a workload; only publication synchronization below may
// replace the complete active set.
func (s *WorkflowApplicationService) ConfigureWorkflowWorkloadIdentity(binding identitysdk.Binding, application identitysdk.ApplicationScope) error {
	if s == nil || !application.WorkspaceID.Valid() || !application.ApplicationKey.Valid() {
		return apperror.New(apperror.KindBadRequest, "backend.workflow.workload_application_invalid", nil, nil)
	}
	capability, ok := binding.(identitysdk.WorkflowWorkloadIdentityBinding)
	if !ok || capability.WorkflowWorkloads() == nil {
		s.workloads = nil
		s.workloadApplication = application
		s.workloadReleases.configure(application)
		return nil
	}
	s.workloads = capability.WorkflowWorkloads()
	s.workloadApplication = application
	s.workloadReleases.configure(application)
	return nil
}

func workflowWorkloadActionKeys(workflow definitionmodel.WorkflowSchema) []string {
	keys := map[string]struct{}{}
	if workflow.Graph != nil {
		for _, node := range workflow.Graph.Nodes {
			switch strings.TrimSpace(node.Type) {
			case "action":
				if key := strings.TrimSpace(workflowpolicy.WorkflowBusinessActionNodeContract(node).ActionKey); key != "" {
					keys[key] = struct{}{}
				}
			case "cc":
				if key := strings.TrimSpace(workflowpolicy.WorkflowCCNodeContract(node).NotificationActionKey); key != "" {
					keys[key] = struct{}{}
				}
			case "approval":
				if key := strings.TrimSpace(workflowpolicy.WorkflowApprovalNodeContract(node).ReminderActionKey); key != "" {
					keys[key] = struct{}{}
				}
			}
		}
	}
	result := make([]string, 0, len(keys))
	for key := range keys {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func workflowWorkloadReleaseRequest(application identitysdk.ApplicationScope, workflows map[string]definitionmodel.WorkflowSchema) (identitysdk.ApplyWorkflowWorkloadBindingsRequest, error) {
	request := identitysdk.ApplyWorkflowWorkloadBindingsRequest{Application: application}
	for _, workflow := range workflows {
		roleKey := workflowpolicy.WorkflowRunAs(workflow)
		if roleKey == "" {
			continue
		}
		actionKeys := workflowWorkloadActionKeys(workflow)
		if len(actionKeys) == 0 {
			return request, apperror.New(apperror.KindBadRequest, "backend.workflow.workload_action_required", nil, map[string]string{"workflow": strings.TrimSpace(workflow.Key), "role": roleKey})
		}
		request.Bindings = append(request.Bindings, identitysdk.WorkflowWorkloadBindingSpec{
			WorkflowKey: strings.TrimSpace(workflow.Key), DefinitionVersionID: strings.TrimSpace(workflow.DefinitionVersionID),
			DefinitionVersion: workflowpolicy.WorkflowPublishedVersion(workflow), RoleKey: roleKey, ActionKeys: actionKeys,
		})
	}
	sort.Slice(request.Bindings, func(i, j int) bool { return request.Bindings[i].WorkflowKey < request.Bindings[j].WorkflowKey })
	digest, err := identitysdk.WorkflowWorkloadReleaseDigest(request.Bindings)
	if err != nil {
		return request, fmt.Errorf("encode workflow workload release: %w", err)
	}
	request.ReleaseDigest = digest
	request.ReleaseID = identitysdk.WorkflowWorkloadReleaseID(digest)
	return request, nil
}

func (s *WorkflowApplicationService) synchronizeWorkflowWorkloadBindings(ctx context.Context, workflows map[string]definitionmodel.WorkflowSchema) error {
	request, err := workflowWorkloadReleaseRequest(s.workloadApplication, workflows)
	if err != nil {
		return err
	}
	if len(request.Bindings) == 0 && s.workloads == nil {
		s.workloadReleases.store(s.workloadApplication, nil)
		return nil
	}
	if s.workloads == nil {
		return apperror.New(apperror.KindUnavailable, "backend.workflow.workload_identity_unavailable", nil, nil)
	}
	if s.principals == nil {
		return apperror.New(apperror.KindUnavailable, "backend.workflow.execution_principal_unavailable", nil, nil)
	}
	result, err := s.workloads.ApplyWorkflowWorkloadBindings(ctx, request)
	if err != nil {
		return fmt.Errorf("apply workflow workload release: %w", err)
	}
	if len(result.Bindings) != len(request.Bindings) {
		return s.failWorkflowWorkloadSynchronization(ctx, apperror.New(apperror.KindConflict, "backend.workflow.workload_release_incomplete", nil, nil))
	}
	expectedByWorkflow := make(map[string]identitysdk.WorkflowWorkloadBindingSpec, len(request.Bindings))
	for _, expected := range request.Bindings {
		expectedByWorkflow[expected.WorkflowKey] = expected
	}
	seen := make(map[string]struct{}, len(result.Bindings))
	for _, binding := range result.Bindings {
		expected, found := expectedByWorkflow[binding.WorkflowKey]
		_, duplicate := seen[binding.WorkflowKey]
		if !found || duplicate || binding.Application != request.Application || binding.SubjectID != identitysdk.WorkflowWorkloadSubjectID(binding.WorkflowKey) || binding.DefinitionVersionID != expected.DefinitionVersionID || binding.DefinitionVersion != expected.DefinitionVersion || binding.RoleKey != expected.RoleKey || !slices.Equal(binding.ActionKeys, expected.ActionKeys) || binding.ReleaseID != request.ReleaseID || binding.ReleaseDigest != request.ReleaseDigest || binding.Status != identitysdk.WorkflowWorkloadBindingActive {
			return s.failWorkflowWorkloadSynchronization(ctx, apperror.New(apperror.KindConflict, "backend.workflow.workload_release_mismatch", nil, map[string]string{"workflow": binding.WorkflowKey}))
		}
		seen[binding.WorkflowKey] = struct{}{}
		resolution := identitysdk.WorkflowWorkloadResolution{
			WorkflowKey: binding.WorkflowKey, DefinitionVersionID: binding.DefinitionVersionID, DefinitionVersion: binding.DefinitionVersion,
			ReleaseID: binding.ReleaseID, ReleaseDigest: binding.ReleaseDigest,
		}
		resolved, resolveErr := s.principals.Resolve(requestcontext.WithWorkspaceID(ctx, string(request.Application.WorkspaceID)), identitysdk.PrincipalResolutionRequest{
			SubjectID: identitysdk.WorkflowWorkloadSubjectID(binding.WorkflowKey), RoleKey: binding.RoleKey, Workload: &resolution,
		})
		if resolveErr != nil {
			return s.failWorkflowWorkloadSynchronization(ctx, fmt.Errorf("resolve workflow workload %s: %w", binding.WorkflowKey, resolveErr))
		}
		resolved.Principal.AccessBundle = &resolved.AccessBundle
		principal := principalmodel.NewPrincipalFromIdentity(resolved.Principal, "")
		if !principal.Known || principal.WorkspaceID != string(request.Application.WorkspaceID) || principal.UserID != string(binding.SubjectID) || principal.RoleKey != binding.RoleKey || principal.Workload == nil || principal.Workload.WorkflowKey != binding.WorkflowKey || principal.Workload.DefinitionVersionID != binding.DefinitionVersionID || principal.Workload.DefinitionVersion != binding.DefinitionVersion || principal.Workload.ReleaseID != binding.ReleaseID || principal.Workload.ReleaseDigest != binding.ReleaseDigest || !principal.HasAllPermissions(binding.ActionKeys) {
			return s.failWorkflowWorkloadSynchronization(ctx, apperror.New(apperror.KindForbidden, "backend.workflow.workload_preflight_denied", nil, map[string]string{"workflow": binding.WorkflowKey, "role": binding.RoleKey}))
		}
	}
	s.workloadReleases.store(request.Application, result.Bindings)
	return nil
}

func (s *WorkflowApplicationService) failWorkflowWorkloadSynchronization(ctx context.Context, cause error) error {
	if restoreErr := s.RestoreWorkflowWorkloadBindings(context.WithoutCancel(ctx)); restoreErr != nil {
		return errors.Join(cause, fmt.Errorf("restore previous workflow workload release: %w", restoreErr))
	}
	return cause
}

// RestoreWorkflowWorkloadBindings reapplies the last locally committed release.
// Metadata reload abort paths call it after restoring the previous role catalog.
func (s *WorkflowApplicationService) RestoreWorkflowWorkloadBindings(ctx context.Context) error {
	if s == nil || s.workloads == nil || s.workloadReleases == nil {
		return nil
	}
	request := s.workloadReleases.requestForRestore()
	if !request.Application.WorkspaceID.Valid() || !request.Application.ApplicationKey.Valid() {
		request.Application = s.workloadApplication
	}
	_, err := s.workloads.ApplyWorkflowWorkloadBindings(ctx, request)
	return err
}
