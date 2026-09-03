package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	actioncontract "github.com/domainry/domainry-foundation/action"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	actionservice "github.com/domainry/domainry-runtime/runtime/domain/action/service"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	operationsprojection "github.com/domainry/domainry-runtime/runtime/domain/operations/projection"
)

// reconcileRuntimeIdentityAuthorization registers the Runtime application and
// submits one complete PermissionDefinition snapshot per canonical Action
// owner. Object fields, references and facts stay in Runtime metadata and are
// intentionally absent from the Identity contract.
func reconcileRuntimeIdentityAuthorization(ctx context.Context, binding identitysdk.Binding, snapshot appschemamodel.ApplicationSchemaSnapshot, moduleActions []actioncontract.ActionDefinition, previousRegistry *actioncontract.Registry, roles []manifestmodel.RoleSchema, workspaceID, applicationKey string, redirectURLs []string) (*actioncontract.Registry, error) {
	registry, err := runtimeAuthorizationActionRegistry(snapshot, applicationKey, moduleActions)
	if err != nil {
		return nil, err
	}
	if err := validateRuntimeAuthorizationReferences(snapshot, roles, registry); err != nil {
		return nil, err
	}
	if binding == nil {
		return registry, nil
	}
	application := identitysdk.ApplicationRef{
		WorkspaceID:    identitysdk.WorkspaceID(strings.TrimSpace(workspaceID)),
		ApplicationKey: identitysdk.ApplicationKey(strings.TrimSpace(applicationKey)),
	}
	if _, err := binding.Applications().Register(ctx, identitysdk.ApplicationRegistration{Application: application, RedirectURLs: append([]string(nil), redirectURLs...)}); err != nil {
		return nil, fmt.Errorf("register Runtime Identity application: %w", err)
	}
	if err := reconcileRuntimePermissionRegistries(ctx, binding.Permissions(), application, previousRegistry, registry); err != nil {
		return nil, err
	}
	return registry, nil
}

// validateRuntimeAuthorizationReferences runs only after the complete Runtime
// Action registry has been assembled. At this point application metadata,
// embedded modules and HTTP endpoint adapters are all present, so the Runtime
// can reject orphan grants without asking the Blueprint model to duplicate a
// Permission resource catalog it cannot know completely.
func validateRuntimeAuthorizationReferences(snapshot appschemamodel.ApplicationSchemaSnapshot, roles []manifestmodel.RoleSchema, registry *actioncontract.Registry) error {
	known := map[string]bool{}
	if registry != nil {
		for _, permission := range registry.PermissionDefinitions() {
			known[strings.TrimSpace(permission.Key)] = true
		}
	}
	type permissionReference struct {
		path, key, sourceKind string
	}
	references := []permissionReference{}
	for roleIndex, role := range roles {
		for permissionIndex, permission := range role.Permissions {
			references = append(references, permissionReference{path: fmt.Sprintf("roles[%d].permissions[%d].permission_key", roleIndex, permissionIndex), key: permission.PermissionKey, sourceKind: "role"})
		}
	}
	for reportIndex, report := range snapshot.Reports {
		for permissionIndex, permission := range report.RequiredPermissions {
			references = append(references, permissionReference{path: fmt.Sprintf("reports[%d].required_permissions[%d]", reportIndex, permissionIndex), key: permission, sourceKind: "report"})
		}
	}
	for entrypointIndex, entrypoint := range snapshot.AgentEntrypoints {
		for permissionIndex, permission := range entrypoint.RequiredPermissions {
			references = append(references, permissionReference{path: fmt.Sprintf("agent_entrypoints[%d].required_permissions[%d]", entrypointIndex, permissionIndex), key: permission, sourceKind: "agent_entrypoint"})
		}
	}
	for bindingIndex, profile := range snapshot.IdentityProfileExtensions {
		for permissionIndex, permission := range profile.RequiredPermissions {
			references = append(references, permissionReference{path: fmt.Sprintf("identity_profile_extensions[%d].required_permissions[%d]", bindingIndex, permissionIndex), key: permission, sourceKind: "identity_profile_extension"})
		}
	}
	unknown := []AuthorizationReferenceDiagnostic{}
	for _, reference := range references {
		key := strings.TrimSpace(reference.key)
		if key == "" || !known[key] {
			unknown = append(unknown, AuthorizationReferenceDiagnostic{Code: "runtime.authorization.permission_unknown", Path: reference.path, PermissionKey: key, SourceKind: reference.sourceKind})
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Slice(unknown, func(i, j int) bool {
		if unknown[i].Path != unknown[j].Path {
			return unknown[i].Path < unknown[j].Path
		}
		return unknown[i].PermissionKey < unknown[j].PermissionKey
	})
	return &AuthorizationReferenceError{Diagnostics: unknown, KnownPermissionCount: len(known)}
}

type AuthorizationReferenceDiagnostic struct {
	Code          string `json:"code"`
	Path          string `json:"path"`
	PermissionKey string `json:"permission_key"`
	SourceKind    string `json:"source_kind"`
}

type AuthorizationReferenceError struct {
	Diagnostics          []AuthorizationReferenceDiagnostic `json:"diagnostics"`
	KnownPermissionCount int                                `json:"known_permission_count"`
}

func (e *AuthorizationReferenceError) Error() string {
	unknown := make([]string, 0, len(e.Diagnostics))
	for _, diagnostic := range e.Diagnostics {
		unknown = append(unknown, fmt.Sprintf("%s=%q", diagnostic.Path, diagnostic.PermissionKey))
	}
	return "Runtime authorization references unknown generated permissions: " + strings.Join(unknown, ", ")
}

func (e *AuthorizationReferenceError) Diagnostic() map[string]any {
	return map[string]any{
		"contract_version":       "domainry-runtime-authorization-diagnostic-v1",
		"code":                   "runtime.authorization.references_invalid",
		"known_permission_count": e.KnownPermissionCount,
		"diagnostics":            append([]AuthorizationReferenceDiagnostic(nil), e.Diagnostics...),
	}
}

func reconcileRuntimePermissionRegistries(ctx context.Context, permissions identitysdk.PermissionRegistry, application identitysdk.ApplicationRef, previousRegistry, nextRegistry *actioncontract.Registry) error {
	if permissions == nil {
		return errors.New("Runtime Identity permission registry is unavailable")
	}
	snapshotReader, ok := permissions.(identitysdk.PermissionSnapshotReader)
	if !ok {
		return errors.New("Runtime Identity permission snapshot reader is unavailable")
	}
	nextDefinitionsByOwner := runtimePermissionDefinitionsByOwner(nextRegistry)
	previousOwners := runtimePermissionDefinitionsByOwner(previousRegistry)
	owners := make([]string, 0, len(nextDefinitionsByOwner))
	for owner := range nextDefinitionsByOwner {
		owners = append(owners, owner)
	}
	for owner := range previousOwners {
		if _, retained := nextDefinitionsByOwner[owner]; !retained {
			owners = append(owners, owner)
			nextDefinitionsByOwner[owner] = []identitysdk.PermissionDefinition{}
		}
	}
	sort.Strings(owners)
	previousDefinitionsByOwner := make(map[string][]identitysdk.PermissionDefinition, len(owners))
	previousSnapshotHashes := make(map[string]string, len(owners))
	for _, owner := range owners {
		request := identitysdk.PermissionSourceSnapshotRequest{Application: application, SourceOwner: owner}
		snapshot, err := snapshotReader.CurrentSourceSnapshot(ctx, request)
		if err != nil {
			return fmt.Errorf("read current Runtime Identity permissions for %q: %w", owner, err)
		}
		if err := snapshot.ValidateFor(request); err != nil {
			return fmt.Errorf("validate current Runtime Identity permissions for %q: %w", owner, err)
		}
		previousSnapshotHashes[owner] = strings.TrimSpace(snapshot.SnapshotHash)
		previousDefinitionsByOwner[owner] = append([]identitysdk.PermissionDefinition(nil), snapshot.Definitions...)
	}
	applied := make([]identitysdk.PermissionReconcileRequest, 0, len(owners))
	for _, owner := range owners {
		request, err := identitysdk.NewPermissionReconcileRequest(application, owner, previousSnapshotHashes[owner], nextDefinitionsByOwner[owner])
		if err != nil {
			return fmt.Errorf("build Runtime Identity permission snapshot for %q: %w", owner, err)
		}
		receipt, err := permissions.Reconcile(ctx, request)
		if err != nil {
			forwardErr := fmt.Errorf("reconcile Runtime Identity permissions for %q: %w", owner, err)
			return errors.Join(forwardErr, compensateRuntimePermissionReconcileBounded(ctx, permissions, application, previousDefinitionsByOwner, applied))
		}
		applied = append(applied, request)
		if err := receipt.ValidateFor(request); err != nil {
			return errors.Join(err, compensateRuntimePermissionReconcileBounded(ctx, permissions, application, previousDefinitionsByOwner, applied))
		}
	}
	return nil
}

func compensateRuntimePermissionReconcileBounded(ctx context.Context, permissions identitysdk.PermissionRegistry, application identitysdk.ApplicationRef, previousDefinitionsByOwner map[string][]identitysdk.PermissionDefinition, applied []identitysdk.PermissionReconcileRequest) error {
	compensationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return compensateRuntimePermissionReconcile(compensationCtx, permissions, application, previousDefinitionsByOwner, applied)
}

func compensateRuntimePermissionReconcile(ctx context.Context, permissions identitysdk.PermissionRegistry, application identitysdk.ApplicationRef, previousDefinitionsByOwner map[string][]identitysdk.PermissionDefinition, applied []identitysdk.PermissionReconcileRequest) error {
	var result error
	for index := len(applied) - 1; index >= 0; index-- {
		forward := applied[index]
		rollback, err := identitysdk.NewPermissionReconcileRequest(application, forward.SourceOwner, forward.SnapshotHash, previousDefinitionsByOwner[forward.SourceOwner])
		if err != nil {
			result = errors.Join(result, fmt.Errorf("build Runtime Identity permission compensation for %q: %w", forward.SourceOwner, err))
			continue
		}
		receipt, err := permissions.Reconcile(ctx, rollback)
		if err != nil {
			result = errors.Join(result, fmt.Errorf("compensate Runtime Identity permissions for %q: %w", forward.SourceOwner, err))
			continue
		}
		if err := receipt.ValidateFor(rollback); err != nil {
			result = errors.Join(result, fmt.Errorf("validate Runtime Identity permission compensation for %q: %w", forward.SourceOwner, err))
		}
	}
	return result
}

func runtimePermissionDefinitionsByOwner(registry *actioncontract.Registry) map[string][]identitysdk.PermissionDefinition {
	result := map[string][]identitysdk.PermissionDefinition{}
	if registry == nil {
		return result
	}
	for _, definition := range registry.Definitions() {
		if definition.Permission == nil {
			continue
		}
		permission := definition.Permission
		owner := strings.TrimSpace(permission.Owner)
		result[owner] = append(result[owner], identitysdk.PermissionDefinition{
			PermissionKey: permission.Key, ResourceKey: permission.ResourceKey, OperationKey: permission.OperationKey,
			Label: permission.Label, Description: permission.Description, Category: permission.Category,
			SourceKind: definition.SourceKind,
		})
	}
	return result
}

// runtimeAuthorizationRegistrySnapshot atomically publishes only frozen
// registries. Candidate registries remain private until Identity reconcile has
// acknowledged their PermissionDefinition snapshots.
type runtimeAuthorizationRegistrySnapshot struct {
	current atomic.Pointer[actioncontract.Registry]
}

func (snapshot *runtimeAuthorizationRegistrySnapshot) Store(registry *actioncontract.Registry) {
	if snapshot == nil || registry == nil || !registry.Frozen() {
		return
	}
	snapshot.current.Store(registry)
}

func (snapshot *runtimeAuthorizationRegistrySnapshot) Load() *actioncontract.Registry {
	if snapshot == nil {
		return nil
	}
	return snapshot.current.Load()
}

func (snapshot *runtimeAuthorizationRegistrySnapshot) QueryPermissionUsages(ctx context.Context, request actioncontract.PermissionUsageRequest) (actioncontract.PermissionUsageSnapshot, error) {
	registry := snapshot.Load()
	if registry == nil {
		return actioncontract.PermissionUsageSnapshot{}, errors.New("Runtime authorization Action registry is unavailable")
	}
	return registry.QueryPermissionUsages(ctx, request)
}

var _ actioncontract.PermissionUsageProvider = (*runtimeAuthorizationRegistrySnapshot)(nil)

func runtimeAuthorizationActionRegistry(snapshot appschemamodel.ApplicationSchemaSnapshot, applicationKey string, moduleActions []actioncontract.ActionDefinition) (*actioncontract.Registry, error) {
	operationsActions, err := operationsprojection.OperationsAuthorizationActions()
	if err != nil {
		return nil, err
	}
	contributed := make([]actioncontract.ActionDefinition, 0, len(moduleActions)+len(runtimeModuleInventoryActions(applicationKey))+len(operationsActions))
	for index := range moduleActions {
		contributed = append(contributed, actioncontract.CloneDefinition(moduleActions[index]))
	}
	contributed = append(contributed, runtimeModuleInventoryActions(applicationKey)...)
	contributed = append(contributed, operationsActions...)
	knownActionKeys := make(map[string]bool, len(contributed)+len(endpointmodel.EndpointContracts))
	for _, action := range contributed {
		knownActionKeys[action.Key] = true
	}
	for _, endpoint := range endpointmodel.EndpointContracts {
		knownActionKeys[endpoint.ActionKey] = true
	}
	nonHTTPBindings := operationsprojection.OperationsAuthorizationBindings()
	for actionKey := range nonHTTPBindings {
		if !knownActionKeys[actionKey] {
			delete(nonHTTPBindings, actionKey)
		}
	}
	return actionservice.BuildAuthorizationRegistry(actionservice.AuthorizationRegistryInput{
		Snapshot: snapshot, ApplicationKey: applicationKey, ContributedActions: contributed, EndpointContracts: endpointmodel.EndpointContracts,
		NonHTTPBindings: nonHTTPBindings,
	})
}
