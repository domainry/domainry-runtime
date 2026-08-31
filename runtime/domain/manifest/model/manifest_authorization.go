package manifestmodel

import localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"

// RoleSchema transports application-authored policy to Identity. Runtime does
// not derive a human AccessBundle from it; the SDK bundle remains authoritative.
type RoleSchema struct {
	Key                   string                             `json:"key"`
	Name                  string                             `json:"name"`
	I18n                  localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Permissions           []string                           `json:"permissions"`
	RecordScope           string                             `json:"record_scope"`
	DataPermissions       []RoleDataPermission               `json:"data_permissions,omitempty"`
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

type RoleDataPermission struct {
	ObjectKey   string                `json:"object_key"`
	Scope       string                `json:"scope"`
	Read        bool                  `json:"read"`
	Write       bool                  `json:"write"`
	Filter      string                `json:"filter,omitempty"`
	AuditDenial bool                  `json:"audit_denial,omitempty"`
	Predicate   *RolePolicyExpression `json:"predicate,omitempty"`
}
type RolePolicyExpression struct {
	Operator    string                      `json:"operator"`
	Path        []RolePolicyRelationSegment `json:"path,omitempty"`
	FieldKey    string                      `json:"field_key,omitempty"`
	ValueSource string                      `json:"value_source,omitempty"`
	ClaimKey    string                      `json:"claim_key,omitempty"`
	Values      []string                    `json:"values,omitempty"`
	Children    []RolePolicyExpression      `json:"children,omitempty"`
}
type RolePolicyRelationSegment struct {
	Direction        string `json:"direction"`
	RelationFieldKey string `json:"relation_field_key"`
	TargetObjectKey  string `json:"target_object_key"`
}
type RoleFieldPermission struct {
	ObjectKey string `json:"object_key"`
	FieldKey  string `json:"field_key"`
	Read      bool   `json:"read"`
	Write     bool   `json:"write"`
	Export    bool   `json:"export"`
	Masked    bool   `json:"masked,omitempty"`
	Reason    string `json:"reason,omitempty"`
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
