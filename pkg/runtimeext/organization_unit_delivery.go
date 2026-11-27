package runtimeext

import (
	"context"
	"errors"
	"strings"

	organizationunit "github.com/domainry/domainry-identity-sdk/organizationunit"
)

type OrganizationUnitDeliveryOperation string

const (
	OrganizationUnitDeliveryCreate  OrganizationUnitDeliveryOperation = "create"
	OrganizationUnitDeliveryResolve OrganizationUnitDeliveryOperation = "resolve"
	OrganizationUnitDeliveryRename  OrganizationUnitDeliveryOperation = "rename"
	OrganizationUnitDeliveryDisable OrganizationUnitDeliveryOperation = "disable"
)

func (operation OrganizationUnitDeliveryOperation) Valid() bool {
	return operation == OrganizationUnitDeliveryCreate || operation == OrganizationUnitDeliveryResolve || operation == OrganizationUnitDeliveryRename || operation == OrganizationUnitDeliveryDisable
}

// OrganizationUnitNodeType is a source-owned non-root organization type key.
// Runtime reserves company for its host-owned root and otherwise treats the
// product's organization vocabulary as opaque data.
type OrganizationUnitNodeType string

func (nodeType OrganizationUnitNodeType) Valid() bool {
	return organizationunit.NodeType(nodeType).ValidDeliveryChild()
}

type OrganizationUnitParentSource string

const (
	// OrganizationUnitParentSourceWorkspaceCompany creates below the host-owned
	// company root. Its required delivered_organization_unit target source makes
	// the new child the authoritative target for the remainder of this Action.
	OrganizationUnitParentSourceWorkspaceCompany OrganizationUnitParentSource = "workspace_company"
	// OrganizationUnitParentSourceTargetOrganization creates below the Action's
	// already-authorized explicit or record-owner target. That parent remains the
	// authoritative target for the complete UoW; the new child is returned only
	// as an opaque reference and can become an owner in a later target/resolve Action.
	OrganizationUnitParentSourceTargetOrganization OrganizationUnitParentSource = "target_organization"
)

func (source OrganizationUnitParentSource) Valid() bool {
	return source == OrganizationUnitParentSourceWorkspaceCompany || source == OrganizationUnitParentSourceTargetOrganization
}

// OrganizationUnitDeliveryCapability is compiled into one Handler. It closes
// the operation, accepted node types, and parent selection before Runtime ever
// invokes project code.
type OrganizationUnitDeliveryCapability struct {
	Operations      []OrganizationUnitDeliveryOperation
	NodeTypes       []OrganizationUnitNodeType
	ParentSource    OrganizationUnitParentSource
	RetargetCreated bool
}

func (capability OrganizationUnitDeliveryCapability) Valid() bool {
	if len(capability.Operations) == 0 || len(capability.NodeTypes) == 0 {
		return false
	}
	operations := map[OrganizationUnitDeliveryOperation]bool{}
	for _, operation := range capability.Operations {
		if !operation.Valid() || operations[operation] {
			return false
		}
		operations[operation] = true
	}
	seen := map[OrganizationUnitNodeType]bool{}
	for _, nodeType := range capability.NodeTypes {
		if !nodeType.Valid() || seen[nodeType] {
			return false
		}
		seen[nodeType] = true
	}
	if operations[OrganizationUnitDeliveryCreate] {
		if !capability.ParentSource.Valid() {
			return false
		}
	} else if strings.TrimSpace(string(capability.ParentSource)) != "" || capability.RetargetCreated {
		return false
	}
	return true
}

func (capability OrganizationUnitDeliveryCapability) Allows(operation OrganizationUnitDeliveryOperation, nodeType OrganizationUnitNodeType) bool {
	if !capability.Valid() {
		return false
	}
	operationAllowed := false
	for _, allowed := range capability.Operations {
		operationAllowed = operationAllowed || allowed == operation
	}
	if !operationAllowed {
		return false
	}
	for _, allowed := range capability.NodeTypes {
		if allowed == nodeType {
			return true
		}
	}
	return false
}

// OrganizationUnitDeliveryRequest carries only source-owned create facts.
// Workspace, parent identity, bearer identity, status, hierarchy, and record
// ownership are supplied or derived by Runtime and Identity.
type OrganizationUnitDeliveryRequest struct {
	Code      string
	Name      string
	NodeType  OrganizationUnitNodeType
	SortOrder int
}

func (request OrganizationUnitDeliveryRequest) Valid() bool {
	return strings.TrimSpace(request.Code) != "" && strings.TrimSpace(request.Name) != "" && request.NodeType.Valid() && request.SortOrder >= 0
}

// OrganizationUnit is the typed opaque reference returned to generated code.
// It exposes no Workspace, hierarchy, bearer, or record-owner authority. In a
// target_organization create, receiving this reference does not retarget the
// current Action or permit the Handler to assign records to the new child.
type OrganizationUnit struct {
	ID       string
	NodeType OrganizationUnitNodeType
}

type OrganizationUnitDeliveryResult struct {
	Organization OrganizationUnit
	Name         string
	Status       string
	Version      int64
	Replayed     bool
}

// OrganizationUnitResolveRequest selects only one compiler-granted node type.
// Runtime supplies the fixed Action target ID.
type OrganizationUnitResolveRequest struct {
	NodeType OrganizationUnitNodeType
}

func (request OrganizationUnitResolveRequest) Valid() bool { return request.NodeType.Valid() }

type OrganizationUnitMutationRequest struct {
	Operation       OrganizationUnitDeliveryOperation
	NodeType        OrganizationUnitNodeType
	Name            string
	ExpectedVersion int64
}

func (request OrganizationUnitMutationRequest) Valid() bool {
	if request.ExpectedVersion < 1 || !request.NodeType.Valid() {
		return false
	}
	switch request.Operation {
	case OrganizationUnitDeliveryRename:
		return strings.TrimSpace(request.Name) != ""
	case OrganizationUnitDeliveryDisable:
		return strings.TrimSpace(request.Name) == ""
	default:
		return false
	}
}

type OrganizationUnitDeliveryExecution interface {
	CreateOrganizationUnit(context.Context, OrganizationUnitDeliveryRequest) (OrganizationUnitDeliveryResult, error)
	ResolveOrganizationUnit(context.Context, OrganizationUnitResolveRequest) (OrganizationUnitDeliveryResult, error)
	MutateOrganizationUnit(context.Context, OrganizationUnitMutationRequest) (OrganizationUnitDeliveryResult, error)
}

var ErrOrganizationUnitDeliveryUnavailable = errors.New("organization unit delivery is unavailable")

func CreateOrganizationUnit(ctx context.Context, execution ActionExecution, request OrganizationUnitDeliveryRequest) (OrganizationUnitDeliveryResult, error) {
	capability, ok := execution.(OrganizationUnitDeliveryExecution)
	if !ok {
		return OrganizationUnitDeliveryResult{}, ErrOrganizationUnitDeliveryUnavailable
	}
	return capability.CreateOrganizationUnit(ctx, request)
}

func ResolveOrganizationUnit(ctx context.Context, execution ActionExecution, request OrganizationUnitResolveRequest) (OrganizationUnitDeliveryResult, error) {
	capability, ok := execution.(OrganizationUnitDeliveryExecution)
	if !ok {
		return OrganizationUnitDeliveryResult{}, ErrOrganizationUnitDeliveryUnavailable
	}
	return capability.ResolveOrganizationUnit(ctx, request)
}

func MutateOrganizationUnit(ctx context.Context, execution ActionExecution, request OrganizationUnitMutationRequest) (OrganizationUnitDeliveryResult, error) {
	capability, ok := execution.(OrganizationUnitDeliveryExecution)
	if !ok {
		return OrganizationUnitDeliveryResult{}, ErrOrganizationUnitDeliveryUnavailable
	}
	return capability.MutateOrganizationUnit(ctx, request)
}
