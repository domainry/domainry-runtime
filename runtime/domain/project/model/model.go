package projectmodel

import (
	"encoding/json"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
)

const CurrentSchemaVersion = "1"

// Model is the complete JSON-owned project contract. It deliberately contains
// only storage shape, cross-cutting authorization, and the structural binding
// between an Identity principal and a business profile. Executable behavior is
// registered as Go project definitions.
type Model struct {
	SchemaVersion    string                                 `json:"schema_version"`
	Project          Project                                `json:"project"`
	Objects          map[string]Object                      `json:"objects"`
	Roles            map[string]Role                        `json:"roles"`
	IdentityProfiles map[string]profilebindingmodel.Binding `json:"identity_profiles,omitempty"`
}

// Object is the closed storage contract accepted from model.json. Fields use
// compact string declarations on the wire and expand into Field values during
// decoding. Runtime's older definition model contains generic Config/Options
// escape hatches; this project contract intentionally does not expose them.
type Object struct {
	Key                   string                                 `json:"key,omitempty"`
	Name                  string                                 `json:"name"`
	Description           string                                 `json:"description,omitempty"`
	I18n                  localizationmodel.LocalizedTextMap     `json:"i18n,omitempty"`
	Fields                map[string]Field                       `json:"fields"`
	UniqueConstraints     map[string]UniqueConstraint            `json:"unique_constraints,omitempty"`
	Capabilities          *definitionmodel.ObjectCapabilitySet   `json:"capabilities,omitempty"`
	LifecyclePolicy       *definitionmodel.ObjectLifecyclePolicy `json:"lifecycle_policy,omitempty"`
	LedgerPolicy          *definitionmodel.ObjectLedgerPolicy    `json:"ledger_policy,omitempty"`
	ExportAssurancePolicy *definitionmodel.ActionAssurancePolicy `json:"export_assurance_policy,omitempty"`
}

// Field is the normalized Runtime representation, not the JSON authoring shape.
type Field struct {
	Key         string                             `json:"key,omitempty"`
	Name        string                             `json:"name"`
	Description string                             `json:"description,omitempty"`
	Type        string                             `json:"type"`
	I18n        localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Validation  FieldValidation                    `json:"validation,omitempty"`
	Relation    *Relation                          `json:"relation,omitempty"`
	Required    bool                               `json:"required"`
	Unique      bool                               `json:"unique,omitempty"`
	Default     json.RawMessage                    `json:"default,omitempty"`
	Sensitive   bool                               `json:"sensitive,omitempty"`
}

type FieldValidation struct {
	MinLength int      `json:"min_length,omitempty"`
	MaxLength int      `json:"max_length,omitempty"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	Pattern   string   `json:"pattern,omitempty"`
	Options   []string `json:"options,omitempty"`
}

type Relation struct {
	TargetObjectKey string `json:"target_object_key"`
}

type UniqueConstraint struct {
	Key    string   `json:"key,omitempty"`
	Fields []string `json:"fields"`
}

type Project struct {
	Key                               string `json:"key"`
	Name                              string `json:"name"`
	DefaultLocale                     string `json:"default_locale,omitempty"`
	TimeZone                          string `json:"time_zone,omitempty"`
	InitialWorkspaceAdministratorRole string `json:"initial_workspace_administrator_role"`
}

// Role is an application-owned authorization policy. Permission keys may
// reference generic object operations or a frozen Go definition registry.
type Role struct {
	PlatformRoleExtension bool                               `json:"platform_role_extension,omitempty"`
	Key                   string                             `json:"key,omitempty"`
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
