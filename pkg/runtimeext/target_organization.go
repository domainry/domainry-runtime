package runtimeext

import (
	"errors"
	"strings"
)

type TargetOrganizationSource string

const (
	TargetOrganizationSourceExplicit                             TargetOrganizationSource = "explicit"
	TargetOrganizationSourceExplicitOrSoleAuthorizedOrganization TargetOrganizationSource = "explicit_or_sole_authorized_organization"
	TargetOrganizationSourceRecordOwner                          TargetOrganizationSource = "record_owner"
	TargetOrganizationSourceDeliveredOrganizationUnit            TargetOrganizationSource = "delivered_organization_unit"
	TargetOrganizationInputInvocation                            string                   = "target_organization_id"
)

// ActionTargetOrganizationCapability is generated with one Action wrapper.
// It declares where Runtime obtains the fixed organization for the whole UoW;
// it does not give project code an organization lookup or ownership setter.
type ActionTargetOrganizationCapability struct {
	Source   TargetOrganizationSource
	Input    string
	NodeType OrganizationUnitNodeType
}

func (value ActionTargetOrganizationCapability) Valid() bool {
	switch value.Source {
	case TargetOrganizationSourceExplicit, TargetOrganizationSourceExplicitOrSoleAuthorizedOrganization:
		return strings.TrimSpace(value.Input) == TargetOrganizationInputInvocation && value.NodeType.Valid()
	case TargetOrganizationSourceRecordOwner, TargetOrganizationSourceDeliveredOrganizationUnit:
		return strings.TrimSpace(value.Input) == "" && strings.TrimSpace(string(value.NodeType)) == ""
	default:
		return false
	}
}

// TargetOrganization is intentionally opaque. Identity owns its hierarchy,
// names, status and Workspace membership; handlers only receive its stable ID.
type TargetOrganization struct {
	ID string
}

type TargetOrganizationExecution interface {
	TargetOrganization() (TargetOrganization, bool)
}

var ErrTargetOrganizationUnavailable = errors.New("target organization is unavailable")

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
