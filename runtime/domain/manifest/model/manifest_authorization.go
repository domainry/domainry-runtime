package manifestmodel

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"
)

// RoleSchema transports application-authored policy to Identity. Runtime does
// not derive a human AccessBundle from it; the SDK bundle remains authoritative.
type RoleSchema struct {
	// PlatformRoleExtension carries project grants for a protected platform role.
	// Publication consumes it only in the installation Workspace.
	PlatformRoleExtension bool                               `json:"platform_role_extension,omitempty"`
	Key                   string                             `json:"key"`
	Name                  string                             `json:"name"`
	I18n                  localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Permissions           []RolePermission                   `json:"permissions"`
	FieldPermissions      []RoleFieldPermission              `json:"field_permissions,omitempty"`
	ReferencePermissions  []RoleReferencePermission          `json:"reference_permissions,omitempty"`
	ExportRules           []RoleExportRule                   `json:"export_rules,omitempty"`
	Audience              string                             `json:"audience,omitempty"`
	RequiredBindingKey    string                             `json:"required_binding_key,omitempty"`
	AssignmentMode        string                             `json:"assignment_mode,omitempty"`
	RiskLevel             string                             `json:"risk_level,omitempty"`
	ConflictRoleKeys      []string                           `json:"conflict_role_keys,omitempty"`
	GrantableRoleKeys     []string                           `json:"grantable_role_keys,omitempty"`
	PermissionSetKeys     []string                           `json:"permission_set_keys,omitempty"`
	PermissionSetGroups   []string                           `json:"permission_set_group_keys,omitempty"`
	GuardrailKeys         []string                           `json:"guardrail_keys,omitempty"`
	Guardrails            []RoleGuardrailPolicy              `json:"guardrails,omitempty"`
	ProvisionToWorkspaces bool                               `json:"provision_to_workspaces,omitempty"`
}

type RoleGuardrailPolicy struct {
	Key                  string                 `json:"key"`
	Name                 string                 `json:"name"`
	Description          string                 `json:"description,omitempty"`
	DeniedPermissionKeys []string               `json:"denied_permission_keys,omitempty"`
	DataRestrictions     []RoleDataRestriction  `json:"data_restrictions,omitempty"`
	FieldRestrictions    []RoleFieldRestriction `json:"field_restrictions,omitempty"`
}

type RoleDataRestriction struct {
	ObjectKey string   `json:"object_key"`
	Actions   []string `json:"actions"`
	Reason    string   `json:"reason,omitempty"`
}
type RoleFieldRestriction struct {
	ObjectKey string   `json:"object_key"`
	FieldKey  string   `json:"field_key"`
	Actions   []string `json:"actions"`
	Reason    string   `json:"reason,omitempty"`
}

type RolePermission struct {
	PermissionKey string                         `json:"permission_key"`
	DataScope     identitysdk.DataScope          `json:"data_scope,omitempty"`
	DataPolicy    *identitysdk.ProjectDataPolicy `json:"data_policy,omitempty"`
	AuditDenial   bool                           `json:"audit_denial,omitempty"`
}
type RoleFieldPermission struct {
	ObjectKey   string `json:"object_key"`
	FieldKey    string `json:"field_key"`
	Read        bool   `json:"read"`
	Write       bool   `json:"write"`
	Export      bool   `json:"export"`
	Masked      bool   `json:"masked,omitempty"`
	Reason      string `json:"reason,omitempty"`
	AuditDenial bool   `json:"audit_denial,omitempty"`
}
type RoleReferencePermission struct {
	SourceObjectKey  string   `json:"source_object_key"`
	RelationFieldKey string   `json:"relation_field_key"`
	TargetObjectKey  string   `json:"target_object_key"`
	DisplayFields    []string `json:"display_fields,omitempty"`
	Mode             string   `json:"mode,omitempty"`
	Reason           string   `json:"reason,omitempty"`
}
type RoleExportRule struct {
	ObjectKey string   `json:"object_key"`
	Mode      string   `json:"mode"`
	Fields    []string `json:"fields"`
}
