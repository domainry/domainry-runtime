package runtimeext

import (
	"context"
	"errors"
	"strings"
)

type TargetOrganizationSource string

const (
	TargetOrganizationSourceExplicit                      TargetOrganizationSource = "explicit"
	TargetOrganizationSourceExplicitOrSoleAuthorizedStore TargetOrganizationSource = "explicit_or_sole_authorized_store"
	TargetOrganizationSourceRecordOwner                   TargetOrganizationSource = "record_owner"
	TargetOrganizationSourceProvisionedStore              TargetOrganizationSource = "provisioned_store"
	TargetOrganizationInputInvocation                     string                   = "target_organization_id"
)

// ActionTargetOrganizationCapability is generated with one Action wrapper.
// It declares where Runtime obtains the fixed organization for the whole UoW;
// it does not give project code an organization lookup or ownership setter.
type ActionTargetOrganizationCapability struct {
	Source TargetOrganizationSource
	Input  string
}

func (value ActionTargetOrganizationCapability) Valid() bool {
	switch value.Source {
	case TargetOrganizationSourceExplicit, TargetOrganizationSourceExplicitOrSoleAuthorizedStore:
		return strings.TrimSpace(value.Input) == TargetOrganizationInputInvocation
	case TargetOrganizationSourceRecordOwner, TargetOrganizationSourceProvisionedStore:
		return strings.TrimSpace(value.Input) == ""
	default:
		return false
	}
}

// TargetOrganization is intentionally opaque. Identity owns its hierarchy,
// names, status and Workspace membership; handlers only receive its stable ID.
type TargetOrganization struct {
	ID string
}

type StoreOrganizationProvisionRequest struct {
	Code      string
	Name      string
	SortOrder int
}

func (request StoreOrganizationProvisionRequest) Valid() bool {
	return strings.TrimSpace(request.Code) != "" &&
		strings.TrimSpace(request.Name) != "" &&
		request.SortOrder >= 0
}

type StoreOrganizationProvisionResult struct {
	Target   TargetOrganization
	Replayed bool
}

type StoreOrganizationRenameRequest struct {
	Name            string
	ExpectedVersion int64
}

func (request StoreOrganizationRenameRequest) Valid() bool {
	return strings.TrimSpace(request.Name) != "" && request.ExpectedVersion > 0
}

type StoreOrganizationDisableRequest struct{ ExpectedVersion int64 }

func (request StoreOrganizationDisableRequest) Valid() bool { return request.ExpectedVersion > 0 }

type StoreOrganizationMutationResult struct {
	Target   TargetOrganization
	Name     string
	Status   string
	Version  int64
	Replayed bool
}

type StoreOrganizationMutationOperation string

const (
	StoreOrganizationMutationRename  StoreOrganizationMutationOperation = "rename"
	StoreOrganizationMutationDisable StoreOrganizationMutationOperation = "disable"
)

func (operation StoreOrganizationMutationOperation) Valid() bool {
	return operation == StoreOrganizationMutationRename || operation == StoreOrganizationMutationDisable
}

// ActionStoreOrganizationMutationCapability is frozen per Handler. The
// target Organization remains Runtime-owned; this grant selects only the exact
// permitted mutation operations.
type ActionStoreOrganizationMutationCapability struct {
	Operations []StoreOrganizationMutationOperation
}

func (capability ActionStoreOrganizationMutationCapability) Valid() bool {
	if len(capability.Operations) == 0 {
		return false
	}
	seen := map[StoreOrganizationMutationOperation]bool{}
	for _, operation := range capability.Operations {
		if !operation.Valid() || seen[operation] {
			return false
		}
		seen[operation] = true
	}
	return true
}

type TargetOrganizationExecution interface {
	TargetOrganization() (TargetOrganization, bool)
}

type StoreOrganizationProvisionExecution interface {
	ProvisionStoreOrganization(context.Context, StoreOrganizationProvisionRequest) (StoreOrganizationProvisionResult, error)
}

type StoreOrganizationMutationExecution interface {
	RenameStoreOrganization(context.Context, StoreOrganizationRenameRequest) (StoreOrganizationMutationResult, error)
	DisableStoreOrganization(context.Context, StoreOrganizationDisableRequest) (StoreOrganizationMutationResult, error)
}

var (
	ErrTargetOrganizationUnavailable = errors.New("target organization is unavailable")
	ErrStoreProvisionUnavailable     = errors.New("store organization provisioning is unavailable")
	ErrStoreMutationUnavailable      = errors.New("store organization mutation is unavailable")
)

func ResolveTargetOrganization(execution ActionExecution) (TargetOrganization, error) {
	capability, ok := execution.(TargetOrganizationExecution)
	if !ok {
		return TargetOrganization{}, ErrTargetOrganizationUnavailable
	}
	target, found := capability.TargetOrganization()
	if !found || strings.TrimSpace(target.ID) == "" {
		return TargetOrganization{}, ErrTargetOrganizationUnavailable
	}
	return target, nil
}

func RenameStoreOrganization(ctx context.Context, execution ActionExecution, request StoreOrganizationRenameRequest) (StoreOrganizationMutationResult, error) {
	capability, ok := execution.(StoreOrganizationMutationExecution)
	if !ok {
		return StoreOrganizationMutationResult{}, ErrStoreMutationUnavailable
	}
	return capability.RenameStoreOrganization(ctx, request)
}

func DisableStoreOrganization(ctx context.Context, execution ActionExecution, request StoreOrganizationDisableRequest) (StoreOrganizationMutationResult, error) {
	capability, ok := execution.(StoreOrganizationMutationExecution)
	if !ok {
		return StoreOrganizationMutationResult{}, ErrStoreMutationUnavailable
	}
	return capability.DisableStoreOrganization(ctx, request)
}

func ProvisionStoreOrganization(ctx context.Context, execution ActionExecution, request StoreOrganizationProvisionRequest) (StoreOrganizationProvisionResult, error) {
	capability, ok := execution.(StoreOrganizationProvisionExecution)
	if !ok {
		return StoreOrganizationProvisionResult{}, ErrStoreProvisionUnavailable
	}
	return capability.ProvisionStoreOrganization(ctx, request)
}
