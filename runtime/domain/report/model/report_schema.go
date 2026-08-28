package reportmodel

import (
	"encoding/json"

	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"
)

type ReportSchema struct {
	Key                  string                             `json:"key"`
	Name                 string                             `json:"name,omitempty"`
	I18n                 localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Dataset              ReportDatasetSchema                `json:"dataset,omitempty"`
	ObjectSQLV1          *ReportObjectSQLSchema             `json:"object_sql_v1,omitempty"`
	RequiredPermissions  []string                           `json:"required_permissions,omitempty"`
	EvidenceRequirements []ReportEvidenceRequirement        `json:"evidence_requirements,omitempty"`
	Materialization      *ReportMaterializationPolicy       `json:"materialization,omitempty"`
	ExportScope          *ReportExportScopeSchema           `json:"export_scope,omitempty"`
}

// MarshalJSON preserves the legacy Dataset representation while ensuring an
// object_sql_v1 Report does not regrow an empty dataset during manifest
// publication or round-trip hashing.
func (r ReportSchema) MarshalJSON() ([]byte, error) {
	type reportSchemaAlias ReportSchema
	if r.ObjectSQLV1 == nil || ReportDatasetDefined(r.Dataset) {
		return json.Marshal(reportSchemaAlias(r))
	}
	return json.Marshal(struct {
		Key                  string                             `json:"key"`
		Name                 string                             `json:"name,omitempty"`
		I18n                 localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
		ObjectSQLV1          *ReportObjectSQLSchema             `json:"object_sql_v1"`
		RequiredPermissions  []string                           `json:"required_permissions,omitempty"`
		EvidenceRequirements []ReportEvidenceRequirement        `json:"evidence_requirements,omitempty"`
		Materialization      *ReportMaterializationPolicy       `json:"materialization,omitempty"`
		ExportScope          *ReportExportScopeSchema           `json:"export_scope,omitempty"`
	}{
		Key: r.Key, Name: r.Name, I18n: r.I18n, ObjectSQLV1: r.ObjectSQLV1,
		RequiredPermissions:  r.RequiredPermissions,
		EvidenceRequirements: r.EvidenceRequirements, Materialization: r.Materialization,
		ExportScope: r.ExportScope,
	})
}

// ReportExportScopeSchema is a report-owned allowlist. It carries only typed
// fields, closed operators, and declared joins; requests never supply SQL or
// another executable query fragment.
type ReportExportScopeSchema struct {
	Query *ReportExportQueryScopeSchema `json:"query,omitempty"`
	Tags  *ReportExportTagScopeSchema   `json:"tags,omitempty"`
}

type ReportExportQueryScopeSchema struct {
	Mode       string                       `json:"mode"`
	Predicates []ReportExportQueryPredicate `json:"predicates"`
}

type ReportExportQueryPredicate struct {
	Field    ReportDatasetField `json:"field"`
	Operator string             `json:"operator"`
}

type ReportExportTagScopeSchema struct {
	Join              ReportDatasetJoin     `json:"join"`
	FamilyJoin        *ReportDatasetJoin    `json:"family_join,omitempty"`
	TargetField       ReportDatasetField    `json:"target_field"`
	TagField          ReportDatasetField    `json:"tag_field"`
	FixedFilters      []ReportDatasetFilter `json:"fixed_filters,omitempty"`
	AllowedMatchModes []string              `json:"allowed_match_modes,omitempty"`
	DefaultMatchMode  string                `json:"default_match_mode,omitempty"`
	Match             string                `json:"match,omitempty"`
}

// ReportMaterializationPolicy enables an authorization-scoped materialized
// result in addition to realtime execution. Refresh scheduling is owned by the
// Scheduler definition; this policy owns freshness and retry semantics.
type ReportMaterializationPolicy struct {
	MaximumLagSeconds  int `json:"maximum_lag_seconds"`
	ConsistencyRetries int `json:"consistency_retries,omitempty"`
}

type ReportEvidenceRequirement struct {
	ObjectKey              string   `json:"object_key"`
	MinimumRecords         int      `json:"minimum_records"`
	RequiredNonEmptyFields []string `json:"required_non_empty_fields,omitempty"`
}

type ReportOperationStateExampleSchema struct {
	Key           string                             `json:"key"`
	Name          string                             `json:"name,omitempty"`
	I18n          localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	ObjectKey     string                             `json:"object_key,omitempty"`
	FieldKey      string                             `json:"field_key,omitempty"`
	TargetValue   any                                `json:"target_value,omitempty"`
	ProofKind     string                             `json:"proof_kind,omitempty"`
	SeedKey       string                             `json:"seed_key,omitempty"`
	SourceActions []string                           `json:"source_actions,omitempty"`
	OperatorNote  string                             `json:"operator_note,omitempty"`
	Config        map[string]any                     `json:"config,omitempty"`
}

type ReportSensitiveFieldPolicySchema struct {
	Key              string                             `json:"key"`
	Name             string                             `json:"name,omitempty"`
	I18n             localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	ObjectKey        string                             `json:"object_key,omitempty"`
	Fields           []string                           `json:"fields,omitempty"`
	Sensitivity      string                             `json:"sensitivity,omitempty"`
	ReadPolicy       string                             `json:"read_policy,omitempty"`
	WritePolicy      string                             `json:"write_policy,omitempty"`
	ExportPolicy     string                             `json:"export_policy,omitempty"`
	ApprovalRequired bool                               `json:"approval_required,omitempty"`
	Watermark        bool                               `json:"watermark,omitempty"`
	AuditEvent       string                             `json:"audit_event,omitempty"`
	Reason           string                             `json:"reason,omitempty"`
	Config           map[string]any                     `json:"config,omitempty"`
}

type ReportExportControlSchema struct {
	Key                      string                             `json:"key"`
	Name                     string                             `json:"name,omitempty"`
	I18n                     localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	ReportKey                string                             `json:"report_key,omitempty"`
	SourceObjects            []string                           `json:"source_objects,omitempty"`
	SensitiveSourceObjects   []string                           `json:"sensitive_source_objects,omitempty"`
	SensitiveFieldPolicyKeys []string                           `json:"sensitive_field_policy_keys,omitempty"`
	ObjectExportPolicyKeys   []string                           `json:"object_export_policy_keys,omitempty"`
	ApprovalRequired         bool                               `json:"approval_required,omitempty"`
	MaskingRequired          bool                               `json:"masking_required,omitempty"`
	Watermark                bool                               `json:"watermark,omitempty"`
	AuditObject              string                             `json:"audit_object,omitempty"`
	DownloadObject           string                             `json:"download_object,omitempty"`
	RecordMapping            ReportExportRecordMappingSchema    `json:"record_mapping"`
	ExportAction             string                             `json:"export_action,omitempty"`
	AllowedQueryKeys         []string                           `json:"allowed_query_keys,omitempty"`
	AllowedTags              []string                           `json:"allowed_tags,omitempty"`
	MaxRows                  int                                `json:"max_rows,omitempty"`
	Reason                   string                             `json:"reason,omitempty"`
	Config                   map[string]any                     `json:"config,omitempty"`
}

// ReportExportRecordMappingSchema binds the generic governed export workflow
// to project-owned audit/download objects without assuming business field
// names. Required fields are validated against the referenced object schemas;
// optional download fields are written only when explicitly declared.
type ReportExportRecordMappingSchema struct {
	AuditReportKeyField      string   `json:"audit_report_key_field"`
	AuditRequesterField      string   `json:"audit_requester_field"`
	AuditStatusField         string   `json:"audit_status_field"`
	AuditPreparedStatuses    []string `json:"audit_prepared_statuses"`
	AuditPreparedStatus      string   `json:"audit_prepared_status"`
	AuditDownloadedStatus    string   `json:"audit_downloaded_status"`
	AuditDeniedStatus        string   `json:"audit_denied_status"`
	AuditExpiredStatus       string   `json:"audit_expired_status"`
	AuditRowCountField       string   `json:"audit_row_count_field"`
	AuditScopeHashField      string   `json:"audit_scope_hash_field"`
	DownloadAuditField       string   `json:"download_audit_field"`
	DownloadFilenameField    string   `json:"download_filename_field"`
	DownloadContentHashField string   `json:"download_content_hash_field"`
	DownloadExpiresAtField   string   `json:"download_expires_at_field"`
	DownloadTokenField       string   `json:"download_token_field,omitempty"`
	DownloadWatermarkedField string   `json:"download_watermarked_field,omitempty"`
	DownloadNumberField      string   `json:"download_number_field,omitempty"`
}
