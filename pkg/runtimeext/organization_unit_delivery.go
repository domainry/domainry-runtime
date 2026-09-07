package runtimeext

import (
	"context"
	"errors"
	"strings"
)

type OrganizationUnitDeliveryOperation string

const (
	OrganizationUnitDeliveryCreate  OrganizationUnitDeliveryOperation = "create"
	OrganizationUnitDeliveryResolve OrganizationUnitDeliveryOperation = "resolve"
)

func (operation OrganizationUnitDeliveryOperation) Valid() bool {
	return operation == OrganizationUnitDeliveryCreate || operation == OrganizationUnitDeliveryResolve
}

// OrganizationUnitNodeType is deliberately narrower than Identity's complete
// node vocabulary. Company is a host-provisioned root and store creation stays
// on the quota-governed provisioned_store contract.
type OrganizationUnitNodeType string

const (
	OrganizationUnitNodeTypeRegion     OrganizationUnitNodeType = "region"
	OrganizationUnitNodeTypeDepartment OrganizationUnitNodeType = "department"
	OrganizationUnitNodeTypeTeam       OrganizationUnitNodeType = "team"
	OrganizationUnitNodeTypeWarehouse  OrganizationUnitNodeType = "warehouse"
)

func (nodeType OrganizationUnitNodeType) Valid() bool {
	switch nodeType {
	case OrganizationUnitNodeTypeRegion, OrganizationUnitNodeTypeDepartment, OrganizationUnitNodeTypeTeam, OrganizationUnitNodeTypeWarehouse:
		return true
	default:
		return false
	}
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
	Operations   []OrganizationUnitDeliveryOperation
	NodeTypes    []OrganizationUnitNodeType
	ParentSource OrganizationUnitParentSource
}

func (capability OrganizationUnitDeliveryCapability) Valid() bool {
	if len(capability.Operations) != 1 || !capability.Operations[0].Valid() || len(capability.NodeTypes) == 0 {
		return false
	}
	seen := map[OrganizationUnitNodeType]bool{}
	for _, nodeType := range capability.NodeTypes {
		if !nodeType.Valid() || seen[nodeType] {
			return false
		}
		seen[nodeType] = true
	}
	if capability.Operations[0] == OrganizationUnitDeliveryCreate {
		return capability.ParentSource.Valid()
	}
	return strings.TrimSpace(string(capability.ParentSource)) == ""
}

func (capability OrganizationUnitDeliveryCapability) Allows(operation OrganizationUnitDeliveryOperation, nodeType OrganizationUnitNodeType) bool {
	if !capability.Valid() || capability.Operations[0] != operation {
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
	Version      int64
	Replayed     bool
}

// OrganizationUnitResolveRequest selects only one compiler-granted node type.
// Runtime supplies the fixed Action target ID.
type OrganizationUnitResolveRequest struct {
	NodeType OrganizationUnitNodeType
}

func (request OrganizationUnitResolveRequest) Valid() bool { return request.NodeType.Valid() }

type OrganizationUnitDeliveryExecution interface {
	CreateOrganizationUnit(context.Context, OrganizationUnitDeliveryRequest) (OrganizationUnitDeliveryResult, error)
	ResolveOrganizationUnit(context.Context, OrganizationUnitResolveRequest) (OrganizationUnitDeliveryResult, error)
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
