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
)

// reconcileRuntimeIdentityAuthorization registers the Runtime application and
// submits one complete PermissionDefinition snapshot per canonical Action
// owner. Object fields, references and facts stay in Runtime metadata and are
// intentionally absent from the Identity contract.
func reconcileRuntimeIdentityAuthorization(ctx context.Context, binding identitysdk.Binding, snapshot appschemamodel.ApplicationSchemaSnapshot, moduleActions []actioncontract.ActionDefinition, previousRegistry *actioncontract.Registry, workspaceID, applicationKey string, redirectURLs []string) (*actioncontract.Registry, error) {
	registry, err := runtimeAuthorizationActionRegistry(snapshot, applicationKey, moduleActions)
	if err != nil {
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
			PermissionKey: permission.Key, ResourceKey: permission.ResourceKey, ActionKey: permission.ActionKey,
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
	contributed := make([]actioncontract.ActionDefinition, 0, len(moduleActions)+len(runtimeModuleInventoryActions(applicationKey)))
	for index := range moduleActions {
		contributed = append(contributed, actioncontract.CloneDefinition(moduleActions[index]))
	}
	contributed = append(contributed, runtimeModuleInventoryActions(applicationKey)...)
	return actionservice.BuildAuthorizationRegistry(actionservice.AuthorizationRegistryInput{
		Snapshot: snapshot, ApplicationKey: applicationKey, ContributedActions: contributed, EndpointContracts: endpointmodel.EndpointContracts,
	})
}
