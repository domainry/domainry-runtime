package contract

type RecordFeaturePermissionSnapshot struct {
	RoleKey   string                             `json:"role_key"`
	UserID    string                             `json:"user_id,omitempty"`
	Objects   []RecordFeatureObjectPermissions   `json:"objects"`
	Actions   []RecordFeatureActionPermission    `json:"actions"`
	Functions []RecordFeatureFunctionPermission  `json:"function_permissions,omitempty"`
	Workflows []RecordFeaturePermissionDecision  `json:"workflows"`
	Data      []RecordDataScopePermission        `json:"data_permissions,omitempty"`
	Fields    []RecordFieldPermissionSnapshot    `json:"field_permissions,omitempty"`
	Exports   []RecordExportPermissionSnapshot   `json:"export_permissions,omitempty"`
	Approvals []RecordApprovalPermissionSnapshot `json:"approval_permissions,omitempty"`
}

type RecordFeatureObjectPermissions struct {
	ObjectKey string                            `json:"object_key"`
	Actions   []RecordFeaturePermissionDecision `json:"actions"`
}

type RecordFeatureActionPermission struct {
	Key               string   `json:"key"`
	ObjectKey         string   `json:"object_key"`
	Label             string   `json:"label,omitempty"`
	Kind              string   `json:"kind"`
	PermissionKey     string   `json:"permission_key"`
	DataScope         string   `json:"data_scope"`
	Allowed           bool     `json:"allowed"`
	Reason            string   `json:"reason"`
	AssuranceRequired []string `json:"assurance_required"`
}

type RecordFeatureFunctionPermission struct {
	Key      string                          `json:"key"`
	Decision RecordFeaturePermissionDecision `json:"decision"`
}

type RecordFeaturePermissionDecision struct {
	Key           string `json:"key"`
	ObjectKey     string `json:"object_key,omitempty"`
	Action        string `json:"action,omitempty"`
	PermissionKey string `json:"permission_key,omitempty"`
	DataScope     string `json:"data_scope,omitempty"`
	Allowed       bool   `json:"allowed"`
	Reason        string `json:"reason"`
}

type RecordDataScopePermission struct {
	ObjectKey  string                  `json:"object_key"`
	OwnerField string                  `json:"owner_field,omitempty"`
	OrgIDField string                  `json:"org_id_field,omitempty"`
	Context    RecordDataScopeContext  `json:"context,omitempty"`
	Read       RecordDataScopeDecision `json:"read"`
	Write      RecordDataScopeDecision `json:"write"`
}

type RecordDataScopeContext struct {
	OrgID                 string   `json:"org_id,omitempty"`
	OrgScopeIDs           []string `json:"org_scope_ids,omitempty"`
	ReportingScopeUserIDs []string `json:"reporting_scope_user_ids,omitempty"`
}

type RecordDataScopeDecision struct {
	Action  string `json:"action"`
	Scope   string `json:"scope"`
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
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
	ObjectKey string                        `json:"object_key"`
	Allowed   bool                          `json:"allowed"`
	Reason    string                        `json:"reason"`
	DataScope string                        `json:"data_scope"`
	Fields    []RecordExportFieldPermission `json:"fields"`
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
