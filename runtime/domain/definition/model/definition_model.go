package definitionmodel

import (
	"strings"

	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"
)

type FieldValidation struct {
	MinLength int      `json:"min_length,omitempty"`
	MaxLength int      `json:"max_length,omitempty"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	Pattern   string   `json:"pattern,omitempty"`
	Options   []string `json:"options,omitempty"`
	Target    string   `json:"target,omitempty"`
}

type FieldSchema struct {
	Key          string                             `json:"key"`
	Name         string                             `json:"name"`
	Description  string                             `json:"description,omitempty"`
	Type         string                             `json:"type"`
	I18n         localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Config       map[string]any                     `json:"config,omitempty"`
	Validation   FieldValidation                    `json:"validation,omitempty"`
	Options      any                                `json:"options,omitempty"`
	Required     bool                               `json:"required"`
	Unique       bool                               `json:"unique,omitempty"`
	Default      any                                `json:"default,omitempty"`
	DefaultValue any                                `json:"default_value,omitempty"`
	// DisabledAt is set when the field has been soft-disabled.
	// ensureObjectStorage skips disabled fields (no physical DROP).
	DisabledAt string `json:"disabled_at,omitempty"`
	// Upgrade declares how rows that predate this field are treated when a
	// later definition version adds the field to a populated object.
	Upgrade *FieldUpgradeRule `json:"upgrade,omitempty"`
}

const (
	// FieldUpgradeBackfill writes BackfillValue into every existing row whose
	// column is still NULL when the field is added.
	FieldUpgradeBackfill = "backfill"
	// FieldUpgradeExempt keeps existing rows valid without the field: the
	// required check is skipped while the stored value stays empty.
	FieldUpgradeExempt = "exempt"
)

// FieldUpgradeRule is the definition-time contract for existing rows when a
// field is introduced by a definition version upgrade.
type FieldUpgradeRule struct {
	ExistingRows  string `json:"existing_rows"`
	BackfillValue any    `json:"backfill_value,omitempty"`
}

// FieldExemptsExistingRows reports whether the field declares the exempt rule.
func (f FieldSchema) FieldExemptsExistingRows() bool {
	return f.Upgrade != nil && strings.TrimSpace(f.Upgrade.ExistingRows) == FieldUpgradeExempt
}

// FieldBackfillValue returns the value written into rows that predate the
// field: an explicit backfill rule wins, otherwise the declared default.
func (f FieldSchema) FieldBackfillValue() any {
	if f.Upgrade != nil && strings.TrimSpace(f.Upgrade.ExistingRows) == FieldUpgradeBackfill && f.Upgrade.BackfillValue != nil {
		return f.Upgrade.BackfillValue
	}
	if f.DefaultValue != nil {
		return f.DefaultValue
	}
	return f.Default
}

type ValidationSchema struct {
	Key       string                             `json:"key"`
	ObjectKey string                             `json:"object_key"`
	Type      string                             `json:"type"`
	FieldKey  string                             `json:"field_key,omitempty"`
	Fields    []string                           `json:"fields,omitempty"`
	Severity  string                             `json:"severity,omitempty"`
	Message   string                             `json:"message,omitempty"`
	I18n      localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Config    map[string]any                     `json:"config,omitempty"`
}

type ObjectSchema struct {
	Key         string                             `json:"key"`
	Name        string                             `json:"name"`
	Description string                             `json:"description"`
	I18n        localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Fields      []FieldSchema                      `json:"fields"`
	Validations []ValidationSchema                 `json:"validations,omitempty"`
	// Capabilities is the source declaration for the generic record API.
	// Nil means the Runtime standard set; a non-nil value can only remove
	// operations the object does not support. The authorization compiler turns
	// the effective set into exact object Action/Permission pairs.
	Capabilities    *ObjectCapabilitySet   `json:"capabilities,omitempty"`
	LifecyclePolicy *ObjectLifecyclePolicy `json:"lifecycle_policy,omitempty"`
	LedgerPolicy    *ObjectLedgerPolicy    `json:"ledger_policy,omitempty"`
	// ExportAssurancePolicy applies the shared assurance grant contract to
	// object exports. Record-bound selector fields are intentionally not used.
	ExportAssurancePolicy *ActionAssurancePolicy `json:"export_assurance_policy,omitempty"`
	UX                    map[string]any         `json:"ux,omitempty"`
	Config                map[string]any         `json:"config,omitempty"`
}

// ObjectCapabilitySet declares which generic record operations are real for
// one ObjectSchema. It is deliberately closed and boolean so an authored false
// cannot be confused with a missing or differently named operation.
type ObjectCapabilitySet struct {
	Create bool `json:"create"`
	Read   bool `json:"read"`
	Update bool `json:"update"`
	Delete bool `json:"delete"`
	Export bool `json:"export"`
}

// StandardObjectCapabilities is the default for an ordinary mutable object.
// Export is included because Runtime publishes a generic export API.
func StandardObjectCapabilities() ObjectCapabilitySet {
	return ObjectCapabilitySet{Create: true, Read: true, Update: true, Delete: true, Export: true}
}

// EffectiveObjectCapabilities applies source declarations and lifecycle
// invariants in one place. Append-only objects can be created and read but can
// never expose generic update/delete, even if malformed source data asks for
// them. An explicit capability set may further remove any endpoint.
func EffectiveObjectCapabilities(object ObjectSchema) ObjectCapabilitySet {
	capabilities := StandardObjectCapabilities()
	if object.Capabilities != nil {
		capabilities = *object.Capabilities
	}
	if object.LifecyclePolicy != nil && strings.TrimSpace(object.LifecyclePolicy.Mode) == ObjectLifecycleAppendOnly {
		capabilities.Update = false
		capabilities.Delete = false
	}
	return capabilities
}

const (
	ObjectLifecycleMutable             = "mutable"
	ObjectLifecycleSoftDeleteOnly      = "soft_delete_only"
	ObjectLifecycleAppendOnly          = "append_only"
	ObjectLifecycleImmutableAfterState = "immutable_after_state"
)

// ObjectLifecyclePolicy is a business-neutral record mutation contract.
// StateField and ImmutableStates are only meaningful for immutable_after_state.
type ObjectLifecyclePolicy struct {
	Mode            string   `json:"mode"`
	StateField      string   `json:"state_field,omitempty"`
	ImmutableStates []string `json:"immutable_states,omitempty"`
}

const (
	ObjectLedgerIntegritySHA256Chain = "sha256_chain"
	ObjectLedgerSignatureNone        = "none"
	ObjectLedgerSignatureHMACSHA256  = "hmac_sha256"
)

// ObjectLedgerPolicy opts an append-only object into the canonical Runtime
// ledger field contract and integrity verification behavior.
type ObjectLedgerPolicy struct {
	Integrity string `json:"integrity"`
	Signature string `json:"signature,omitempty"`
}

type ActionSchema struct {
	Key                       string                                 `json:"key"`
	ObjectKey                 string                                 `json:"object_key"`
	Label                     string                                 `json:"label"`
	I18n                      localizationmodel.LocalizedTextMap     `json:"i18n,omitempty"`
	Kind                      string                                 `json:"kind"`
	RiskLevel                 string                                 `json:"risk_level,omitempty"`
	Preconditions             []string                               `json:"preconditions"`
	AuditEvent                string                                 `json:"audit_event"`
	InputType                 string                                 `json:"input_type,omitempty"`
	OutputType                string                                 `json:"output_type,omitempty"`
	InputContractSHA256       string                                 `json:"input_contract_sha256,omitempty"`
	OutputContractSHA256      string                                 `json:"output_contract_sha256,omitempty"`
	PayloadFields             []ActionPayloadField                   `json:"payload_fields,omitempty"`
	OutputFields              []ActionOutputField                    `json:"output_fields,omitempty"`
	Defaults                  map[string]any                         `json:"defaults,omitempty"`
	OptimisticConcurrency     bool                                   `json:"optimistic_concurrency,omitempty"`
	ConcurrencyField          string                                 `json:"concurrency_field,omitempty"`
	AssurancePolicy           *ActionAssurancePolicy                 `json:"assurance_policy,omitempty"`
	EffectSet                 *ActionEffectSet                       `json:"effect_set,omitempty"`
	FileOperations            []string                               `json:"file_operations,omitempty"`
	TargetOrganization        *ActionTargetOrganizationPolicy        `json:"target_organization,omitempty"`
	OrganizationUnitDelivery  *ActionOrganizationUnitDeliveryPolicy  `json:"organization_unit_delivery,omitempty"`
	StoreOrganizationMutation *ActionStoreOrganizationMutationPolicy `json:"store_organization_mutation,omitempty"`
}

type ActionOrganizationUnitDeliveryPolicy struct {
	Operations   []string `json:"operations"`
	NodeTypes    []string `json:"node_types"`
	ParentSource string   `json:"parent_source,omitempty"`
}

type ActionStoreOrganizationMutationPolicy struct {
	Operations []string `json:"operations"`
}

const DefaultBusinessActionAuditEvent = "business_action_executed"

// EffectiveActionAuditEvent returns the stable technical event used when an
// Action does not need a business-specific subscription event. The Action key
// remains part of audit metadata, so the default does not collapse distinct
// business operations into an indistinguishable audit record.
func EffectiveActionAuditEvent(action ActionSchema) string {
	if event := strings.TrimSpace(action.AuditEvent); event != "" {
		return event
	}
	return DefaultBusinessActionAuditEvent
}

// ActionPermissionSubject decomposes the canonical Action key for SDK metadata
// that still represents a permission as resource plus operation. Authorization
// must compare the complete Action key directly; this helper is not a mapping.
func ActionPermissionSubject(action ActionSchema) (string, string) {
	key := strings.TrimSpace(action.Key)
	objectKey := strings.TrimSpace(action.ObjectKey)
	if objectKey != "" && strings.HasPrefix(key, objectKey+".") {
		return objectKey, strings.TrimSpace(strings.TrimPrefix(key, objectKey+"."))
	}
	separator := strings.LastIndex(key, ".")
	if separator <= 0 || separator == len(key)-1 {
		return "", key
	}
	return strings.TrimSpace(key[:separator]), strings.TrimSpace(key[separator+1:])
}

const (
	ActionKindObjectCreate      = "object_create"
	ActionKindObjectOperation   = "object_operation"
	ActionKindBulkOperation     = "bulk_operation"
	ActionKindRecordUpdate      = "record_update"
	ActionKindRecordDelete      = "record_delete"
	ActionKindRecordRestore     = "record_restore"
	ActionKindTransitionState   = "transition_state"
	ActionKindConditionalUpdate = "conditional_update"
	ActionKindRecordOperation   = "record_operation"
)

func ActionKindValues() []string {
	return []string{
		ActionKindBulkOperation,
		ActionKindConditionalUpdate,
		ActionKindObjectCreate,
		ActionKindObjectOperation,
		ActionKindRecordDelete,
		ActionKindRecordOperation,
		ActionKindRecordRestore,
		ActionKindRecordUpdate,
		ActionKindTransitionState,
	}
}

// ActionEffectSet is the immutable, compiler-owned record access envelope of a
// published Action. Runtime derives it from the fixed System Operation or
// generated Business Handler capability contract.
type ActionEffectSet struct {
	Read  []ActionObjectEffect `json:"read"`
	Write []ActionObjectEffect `json:"write"`
}

type ActionObjectEffect struct {
	ObjectKey  string   `json:"object_key"`
	Fields     []string `json:"fields"`
	Operations []string `json:"operations,omitempty"`
}

type ActionTargetOrganizationPolicy struct {
	Source string `json:"source"`
	Input  string `json:"input,omitempty"`
}

const (
	ActionTargetOrganizationSourceExplicit                      = "explicit"
	ActionTargetOrganizationSourceExplicitOrSoleAuthorizedStore = "explicit_or_sole_authorized_store"
	ActionTargetOrganizationSourceRecordOwner                   = "record_owner"
	ActionTargetOrganizationSourceProvisionedStore              = "provisioned_store"
	ActionTargetOrganizationSourceDeliveredOrganizationUnit     = "delivered_organization_unit"
	ActionTargetOrganizationInputInvocation                     = "target_organization_id"
)

const (
	ActionAssuranceNormalLogin      = "normal_login"
	ActionAssuranceRecentReauth     = "recent_reauth"
	ActionAssuranceOTP              = "otp"
	ActionAssuranceMakerChecker     = "maker_checker"
	ActionAssuranceWorkflowApproval = "workflow_approval"
)

type ActionAssurancePolicy struct {
	RequiredMethods           []string `json:"required_methods"`
	RecentReauthMaxAgeSeconds int      `json:"recent_reauth_max_age_seconds,omitempty"`
	ApprovalVersionField      string   `json:"approval_version_field,omitempty"`
	ApprovalHashField         string   `json:"approval_hash_field,omitempty"`
	MakerField                string   `json:"maker_field,omitempty"`
}

const ObjectExportAssuranceActionPrefix = "record.export:"

// ObjectExportAssuranceActionKey is the stable synthetic action binding used
// by trusted assurance issuers and the Runtime export verifier.
func ObjectExportAssuranceActionKey(objectKey string) string {
	return ObjectExportAssuranceActionPrefix + objectKey
}

const (
	// ActionPayloadTypeObject is the only composite payload field type. It is
	// legal only with a non-empty Fields list.
	ActionPayloadTypeObject = "object"
	// ActionPayloadMaxDepth bounds nesting; the top-level payload_fields list is
	// depth 1.
	ActionPayloadMaxDepth = 4
	// ActionPayloadMaxLeafFields bounds the total number of scalar leaves in one
	// Action payload contract, counted across every nesting level.
	ActionPayloadMaxLeafFields = 200
	// ActionPayloadMaxItems is the absolute item ceiling of one repeated field
	// and the default max_items when the definition declares none.
	ActionPayloadMaxItems = 200
)

// ActionPayloadField is one node of the Action payload contract. Scalar leaves
// carry a Runtime field type; Type "object" with Fields declares a nested
// object; Repeated wraps either shape in an array. Action.Defaults only apply
// to top-level keys; nested defaults use DefaultValue on the nested field.
type ActionPayloadField struct {
	Key             string                             `json:"key"`
	Name            string                             `json:"name,omitempty"`
	Description     string                             `json:"description,omitempty"`
	Type            string                             `json:"type,omitempty"`
	Options         []string                           `json:"options,omitempty"`
	I18n            localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Required        bool                               `json:"required,omitempty"`
	Repeated        bool                               `json:"repeated,omitempty"`
	Fields          []ActionPayloadField               `json:"fields,omitempty"`
	MinItems        *int                               `json:"min_items,omitempty"`
	MaxItems        *int                               `json:"max_items,omitempty"`
	SourceObjectKey string                             `json:"source_object_key,omitempty"`
	SourceFieldKey  string                             `json:"source_field_key,omitempty"`
	TargetObjectKey string                             `json:"target_object_key,omitempty"`
	DefaultValue    any                                `json:"default_value,omitempty"`
}

// IsObject reports whether the field declares a nested object shape.
func (f ActionPayloadField) IsObject() bool {
	return strings.TrimSpace(f.Type) == ActionPayloadTypeObject
}

// ActionPayloadFieldIsStructured reports whether any payload field, at any
// nesting level, uses the structured (repeated or nested object) contract.
// Legacy scalar-only Actions keep the flat Record normalization path.
func ActionPayloadFieldIsStructured(action ActionSchema) bool {
	return actionPayloadFieldsStructured(action.PayloadFields)
}

func actionPayloadFieldsStructured(fields []ActionPayloadField) bool {
	for _, field := range fields {
		if field.Repeated || len(field.Fields) > 0 || field.IsObject() {
			return true
		}
	}
	return false
}

// ActionOutputField preserves the source-field lineage of a generated Handler
// result. Runtime uses this closed contract to re-apply caller field security
// after trusted business code has completed its internal reads.
type ActionOutputField struct {
	Key                                 string   `json:"key"`
	Type                                string   `json:"type"`
	SourceObjectKey                     string   `json:"source_object_key,omitempty"`
	SourceFieldKey                      string   `json:"source_field_key,omitempty"`
	Required                            bool     `json:"required,omitempty"`
	Repeated                            bool     `json:"repeated,omitempty"`
	StoreOrganizationSnapshotObjectKeys []string `json:"store_organization_snapshot_object_keys,omitempty"`
}

type WorkflowSchema struct {
	DefinitionVersionID string                             `json:"-"`
	PublishedVersion    int                                `json:"-"`
	Key                 string                             `json:"key"`
	Name                string                             `json:"name"`
	I18n                localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Trigger             map[string]any                     `json:"trigger"`
	Condition           map[string]any                     `json:"condition"`
	Action              map[string]any                     `json:"action"`
	TriggerContract     *WorkflowTriggerContract           `json:"trigger_contract,omitempty"`
	ConditionContract   *WorkflowConditionContract         `json:"condition_contract,omitempty"`
	ActionContract      *WorkflowActionContract            `json:"action_contract,omitempty"`
	InputFields         []WorkflowInputField               `json:"input_fields,omitempty"`
	Enabled             bool                               `json:"enabled"`
	RunAs               string                             `json:"run_as,omitempty"`
	IdempotencyKeys     []string                           `json:"idempotency_keys,omitempty"`
	Retry               *WorkflowRetryPolicy               `json:"retry,omitempty"`
	DeadLetterPolicy    map[string]any                     `json:"dead_letter_policy,omitempty"`
	TimeoutSeconds      int                                `json:"timeout_seconds,omitempty"`
	AuditEvent          string                             `json:"audit_event,omitempty"`
	Graph               *WorkflowGraphSchema               `json:"graph,omitempty"`
}

type WorkflowInputField struct {
	Key          string                             `json:"key"`
	Name         string                             `json:"name,omitempty"`
	Description  string                             `json:"description,omitempty"`
	Type         string                             `json:"type"`
	Options      []string                           `json:"options,omitempty"`
	Required     bool                               `json:"required,omitempty"`
	DefaultValue any                                `json:"default_value,omitempty"`
	I18n         localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
}

// WorkflowGraphSchema is the persisted Graph V2 visual and execution contract.
// Runtime behavior is derived from typed trigger, condition, node and action
// contracts; the untyped summary maps are descriptive metadata only.
type WorkflowGraphSchema struct {
	Version  int                 `json:"version"`
	Nodes    []WorkflowGraphNode `json:"nodes"`
	Edges    []WorkflowGraphEdge `json:"edges"`
	Viewport map[string]float64  `json:"viewport,omitempty"`
}

type WorkflowGraphNode struct {
	ID       string                             `json:"id"`
	Type     string                             `json:"type"`
	Name     string                             `json:"name"`
	I18n     localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Position map[string]any                     `json:"position,omitempty"`
	Config   map[string]any                     `json:"config,omitempty"`
	Contract *WorkflowNodeContract              `json:"contract,omitempty"`
}

type WorkflowNodeContract struct {
	Condition *WorkflowConditionContract          `json:"condition,omitempty"`
	Approval  *WorkflowApprovalNodeContract       `json:"approval,omitempty"`
	Action    *WorkflowBusinessActionNodeContract `json:"action,omitempty"`
	CC        *WorkflowCCNodeContract             `json:"cc,omitempty"`
	Timer     *WorkflowTimerNodeContract          `json:"timer,omitempty"`
	AgentTask *WorkflowAgentTaskNodeContract      `json:"agent_task,omitempty"`
}

type WorkflowAgentTaskNodeContract struct {
	TaskKey         string                    `json:"task_key"`
	TaskVersion     string                    `json:"task_version"`
	Identity        WorkflowAgentTaskIdentity `json:"identity"`
	Input           map[string]any            `json:"input"`
	OutputVariable  string                    `json:"output_variable"`
	ExecutionMode   string                    `json:"execution_mode"`
	TimeoutSeconds  int                       `json:"timeout_seconds,omitempty"`
	Retry           *WorkflowRetryPolicy      `json:"retry,omitempty"`
	OnError         string                    `json:"on_error,omitempty"`
	AllowedObjects  []string                  `json:"allowed_objects,omitempty"`
	AllowedActions  []string                  `json:"allowed_actions,omitempty"`
	AllowedOutcomes []string                  `json:"allowed_outcomes,omitempty"`
}

type WorkflowAgentTaskIdentity struct {
	Mode         string `json:"mode"`
	PrincipalKey string `json:"principal_key,omitempty"`
}

type WorkflowTimerNodeContract struct {
	TimerKey            string `json:"timer_key,omitempty"`
	Purpose             string `json:"purpose,omitempty"`
	At                  string `json:"at,omitempty"`
	DurationSeconds     int64  `json:"duration_seconds,omitempty"`
	SourceField         string `json:"source_field,omitempty"`
	OffsetSeconds       int    `json:"offset_seconds,omitempty"`
	Timezone            string `json:"timezone,omitempty"`
	BusinessCalendarKey string `json:"business_calendar_key,omitempty"`
}

type WorkflowApprovalNodeContract struct {
	Mode                string                     `json:"mode"`
	RequiredApprovals   int                        `json:"required_approvals,omitempty"`
	Title               string                     `json:"title,omitempty"`
	Resolvers           []WorkflowAssigneeResolver `json:"resolvers"`
	ResolverMode        string                     `json:"resolver_mode,omitempty"`
	EmptyAssigneePolicy string                     `json:"empty_assignee_policy,omitempty"`
	DueSeconds          int                        `json:"due_seconds,omitempty"`
	ReminderActionKey   string                     `json:"reminder_action_key,omitempty"`
	ReminderInput       map[string]any             `json:"reminder_input,omitempty"`
	EscalationSeconds   int                        `json:"escalation_seconds,omitempty"`
	EscalationResolvers []WorkflowAssigneeResolver `json:"escalation_resolvers,omitempty"`
}

type WorkflowAssigneeResolver struct {
	Type      string   `json:"type"`
	Priority  int      `json:"priority,omitempty"`
	UserIDs   []string `json:"user_ids,omitempty"`
	RoleKey   string   `json:"role_key,omitempty"`
	Field     string   `json:"field,omitempty"`
	UserField string   `json:"user_field,omitempty"`
}

type WorkflowBusinessActionNodeContract struct {
	ActionKey      string               `json:"action_key"`
	ObjectKey      string               `json:"object_key,omitempty"`
	RecordID       string               `json:"record_id,omitempty"`
	Input          map[string]any       `json:"input,omitempty"`
	OutputVariable string               `json:"output_variable,omitempty"`
	TimeoutSeconds int                  `json:"timeout_seconds,omitempty"`
	Retry          *WorkflowRetryPolicy `json:"retry,omitempty"`
	OnError        string               `json:"on_error,omitempty"`
}

type WorkflowCCNodeContract struct {
	NotificationActionKey string                     `json:"notification_action_key"`
	Resolvers             []WorkflowAssigneeResolver `json:"resolvers"`
	Input                 map[string]any             `json:"input,omitempty"`
}

type WorkflowGraphEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Branch string `json:"branch,omitempty"`
	Label  string `json:"label,omitempty"`
}

type WorkflowTriggerContract struct {
	Type       string   `json:"type,omitempty"`
	ObjectKey  string   `json:"object_key,omitempty"`
	ObjectKeys []string `json:"object_keys,omitempty"`
	FieldKey   string   `json:"field_key,omitempty"`
	Offset     string   `json:"offset,omitempty"`
	Event      string   `json:"event,omitempty"`
}

type WorkflowConditionContract struct {
	Type       string                      `json:"type,omitempty"`
	Field      string                      `json:"field,omitempty"`
	Value      any                         `json:"value,omitempty"`
	Expression string                      `json:"expression,omitempty"`
	Operator   string                      `json:"operator,omitempty"`
	Conditions []WorkflowConditionContract `json:"conditions,omitempty"`
	Condition  *WorkflowConditionContract  `json:"condition,omitempty"`
	Fields     map[string]any              `json:"fields,omitempty"`
}

type WorkflowActionContract struct {
	Type      string         `json:"type,omitempty"`
	ObjectKey string         `json:"object_key,omitempty"`
	ActionKey string         `json:"action_key,omitempty"`
	Patch     map[string]any `json:"patch,omitempty"`
	Binding   string         `json:"binding,omitempty"`
}

type WorkflowRetryPolicy struct {
	MaxAttempts  int `json:"max_attempts,omitempty"`
	DelaySeconds int `json:"delay_seconds,omitempty"`
}
