package contract

import identitysdk "github.com/domainry/domainry-identity-sdk"

type RecordFeaturePermissionSnapshot struct {
	RoleKey   string                             `json:"role_key"`
	UserID    string                             `json:"user_id,omitempty"`
	Objects   []RecordFeatureObjectPermissions   `json:"objects"`
	Actions   []RecordFeatureActionPermission    `json:"actions"`
	Functions []RecordFeatureFunctionPermission  `json:"function_permissions,omitempty"`
	Workflows []RecordFeaturePermissionDecision  `json:"workflows"`
	Fields    []RecordFieldPermissionSnapshot    `json:"field_permissions,omitempty"`
	Exports   []RecordExportPermissionSnapshot   `json:"export_permissions,omitempty"`
	Approvals []RecordApprovalPermissionSnapshot `json:"approval_permissions,omitempty"`
}

type RecordFeatureObjectPermissions struct {
	ObjectKey string                            `json:"object_key"`
	Actions   []RecordFeaturePermissionDecision `json:"actions"`
}

type RecordFeatureActionPermission struct {
	Key               string                  `json:"key"`
	ObjectKey         string                  `json:"object_key"`
	Label             string                  `json:"label,omitempty"`
	Kind              string                  `json:"kind"`
	PermissionKey     string                  `json:"permission_key"`
	DataScopes        []identitysdk.DataScope `json:"data_scopes,omitempty"`
	Allowed           bool                    `json:"allowed"`
	Reason            string                  `json:"reason"`
	AssuranceRequired []string                `json:"assurance_required"`
}

type RecordFeatureFunctionPermission struct {
	Key      string                          `json:"key"`
	Decision RecordFeaturePermissionDecision `json:"decision"`
}

type RecordFeaturePermissionDecision struct {
	Key           string                  `json:"key"`
	ObjectKey     string                  `json:"object_key,omitempty"`
	Action        string                  `json:"action,omitempty"`
	PermissionKey string                  `json:"permission_key,omitempty"`
	DataScopes    []identitysdk.DataScope `json:"data_scopes,omitempty"`
	Allowed       bool                    `json:"allowed"`
	Reason        string                  `json:"reason"`
}

type RecordFieldPermissionSnapshot struct {
	ObjectKey string                    `json:"object_key"`
	FieldKey  string                    `json:"field_key"`
	FieldType string                    `json:"field_type,omitempty"`
	Read      RecordFieldAccessDecision `json:"read"`
	Write     RecordFieldAccessDecision `json:"write"`
	Export    RecordFieldAccessDecision `json:"export"`
	Masked    bool                      `json:"masked,omitempty"`
	Source    string                    `json:"source"`
}

type RecordFieldAccessDecision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

type RecordExportPermissionSnapshot struct {
	ObjectKey  string                        `json:"object_key"`
	Allowed    bool                          `json:"allowed"`
	Reason     string                        `json:"reason"`
	DataScopes []identitysdk.DataScope       `json:"data_scopes,omitempty"`
	Fields     []RecordExportFieldPermission `json:"fields"`
}

type RecordExportFieldPermission struct {
	FieldKey  string                    `json:"field_key"`
	FieldType string                    `json:"field_type,omitempty"`
	Export    RecordFieldAccessDecision `json:"export"`
	Masked    bool                      `json:"masked,omitempty"`
}

type RecordApprovalPermissionSnapshot struct {
	ActionKey         string                          `json:"action_key"`
	ObjectKey         string                          `json:"object_key"`
	Label             string                          `json:"label,omitempty"`
	ApprovalOperation string                          `json:"approval_operation"`
	PermissionKey     string                          `json:"permission_key"`
	Decision          RecordFeaturePermissionDecision `json:"decision"`
}
