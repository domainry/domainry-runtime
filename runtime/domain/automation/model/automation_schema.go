package automationmodel

import recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

import localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"

// AutomationRuleSchema defines a stateless object lifecycle rule. The ordered
// Actions slice is executable; Layout only preserves editor presentation.
type AutomationRuleSchema struct {
	Key          string                             `json:"key"`
	Name         string                             `json:"name"`
	I18n         localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	ObjectKey    string                             `json:"object_key"`
	Enabled      bool                               `json:"enabled"`
	Priority     int                                `json:"priority,omitempty"`
	Trigger      AutomationTriggerSchema            `json:"trigger"`
	Conditions   AutomationConditionGroup           `json:"conditions,omitempty"`
	Instructions []AutomationInstructionSchema      `json:"instructions"`
	Execution    AutomationExecutionPolicy          `json:"execution,omitempty"`
	AuditEvent   string                             `json:"audit_event,omitempty"`
	Layout       *AutomationLayoutSchema            `json:"layout,omitempty"`
}

type AutomationTriggerSchema struct {
	Phase         string   `json:"phase"`
	Operation     string   `json:"operation"`
	ChangedFields []string `json:"changed_fields,omitempty"`
	FromState     string   `json:"from_state,omitempty"`
	ToState       string   `json:"to_state,omitempty"`
	Source        string   `json:"source,omitempty"`
}

type AutomationConditionGroup struct {
	Mode    string                      `json:"mode,omitempty"`
	Clauses []AutomationConditionClause `json:"clauses,omitempty"`
	Groups  []AutomationConditionGroup  `json:"groups,omitempty"`
}

type AutomationConditionClause struct {
	Reference string `json:"reference"`
	Operator  string `json:"operator"`
	Value     any    `json:"value,omitempty"`
}

type AutomationInstructionSchema struct {
	Key           string                             `json:"key"`
	Type          string                             `json:"type"`
	Name          string                             `json:"name,omitempty"`
	I18n          localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Mode          string                             `json:"mode,omitempty"`
	ConnectorKey  string                             `json:"connector_key,omitempty"`
	ConnectionKey string                             `json:"connection_key,omitempty"`
	Operation     string                             `json:"operation,omitempty"`
	Input         map[string]any                     `json:"input,omitempty"`
	ResultAlias   string                             `json:"result_alias,omitempty"`
	OnError       string                             `json:"on_error,omitempty"`
	Config        map[string]any                     `json:"config,omitempty"`
}

type AutomationExecutionPolicy struct {
	Mode               string   `json:"mode,omitempty"`
	RunAs              string   `json:"run_as,omitempty"`
	ResultNotification string   `json:"result_notification,omitempty"`
	TimeoutSeconds     int      `json:"timeout_seconds,omitempty"`
	MaxDepth           int      `json:"max_depth,omitempty"`
	IdempotencyKeys    []string `json:"idempotency_keys,omitempty"`
}

// AutomationLifecycleEvent is the immutable envelope persisted for after-write
// automation. Version identifies the domain-record revision that produced it.
type AutomationLifecycleEvent struct {
	ID              string             `json:"id"`
	RuleKey         string             `json:"rule_key"`
	ObjectKey       string             `json:"object_key"`
	Operation       string             `json:"operation"`
	RecordID        string             `json:"record_id"`
	RecordVersion   string             `json:"record_version"`
	Before          map[string]any     `json:"before,omitempty"`
	Record          recordmodel.Record `json:"record"`
	ActorUserID     string             `json:"actor_user_id,omitempty"`
	ActorRoleKey    string             `json:"actor_role_key,omitempty"`
	RequestID       string             `json:"request_id,omitempty"`
	CorrelationID   string             `json:"correlation_id"`
	CausationID     string             `json:"causation_id"`
	IdentityPolicy  string             `json:"identity_policy"`
	AutomationDepth int                `json:"automation_depth,omitempty"`
	VisitedRuleKeys []string           `json:"visited_rule_keys,omitempty"`
	OccurredAt      string             `json:"occurred_at"`
}

type AutomationLayoutSchema struct {
	Version  int                               `json:"version"`
	Nodes    map[string]AutomationNodePosition `json:"nodes,omitempty"`
	Viewport map[string]float64                `json:"viewport,omitempty"`
}

type AutomationNodePosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
