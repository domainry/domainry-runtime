package notificationmodel

type NotificationEventType struct {
	Key               string                                       `json:"key"`
	Source            string                                       `json:"source"`
	Category          string                                       `json:"category"`
	DefaultSeverity   string                                       `json:"default_severity"`
	Surfaces          []string                                     `json:"surfaces"`
	MandatoryInApp    bool                                         `json:"mandatory_in_app"`
	TemplateKey       string                                       `json:"template_key"`
	DefaultLocale     string                                       `json:"default_locale"`
	Locales           map[string]NotificationInboxEventTypeContent `json:"locales"`
	Variables         []NotificationTemplateVariable               `json:"variables,omitempty"`
	Actions           []NotificationInboxActionDescriptor          `json:"actions,omitempty"`
	AudienceResolvers []string                                     `json:"audience_resolvers,omitempty"`
	Version           int                                          `json:"version"`
	Status            string                                       `json:"status"`
}

type NotificationInboxEventTypeContent struct {
	Title        string                     `json:"title"`
	Body         string                     `json:"body"`
	Facts        []NotificationTemplateFact `json:"facts,omitempty"`
	ActionLabels map[string]string          `json:"action_labels,omitempty"`
}

// NotificationIntent is the only producer-facing contract. Producers emit
// typed facts and never supply rendered copy, URLs, routes, or HTTP requests.
type NotificationIntent struct {
	ID                   string         `json:"id"`
	WorkspaceID          string         `json:"workspace_id"`
	SourceEventID        string         `json:"source_event_id"`
	EventType            string         `json:"event_type"`
	Severity             string         `json:"severity,omitempty"`
	Surface              string         `json:"surface"`
	RecipientUserIDs     []string       `json:"recipient_user_ids"`
	AudienceResolverKeys []string       `json:"audience_resolver_keys,omitempty"`
	SubjectType          string         `json:"subject_type,omitempty"`
	SubjectID            string         `json:"subject_id,omitempty"`
	SubjectVersion       string         `json:"subject_version,omitempty"`
	GroupKey             string         `json:"group_key,omitempty"`
	DedupeKey            string         `json:"dedupe_key,omitempty"`
	ActionState          string         `json:"action_state,omitempty"`
	AlertState           string         `json:"alert_state,omitempty"`
	ExpiresAt            string         `json:"expires_at,omitempty"`
	OccurredAt           string         `json:"occurred_at"`
	Locale               string         `json:"locale,omitempty"`
	Variables            map[string]any `json:"variables,omitempty"`
	CorrelationID        string         `json:"correlation_id,omitempty"`
	TraceID              string         `json:"trace_id,omitempty"`
}

type NotificationRule struct {
	EventTypeKey             string                    `json:"event_type_key"`
	Enabled                  bool                      `json:"enabled"`
	AudienceResolvers        []string                  `json:"audience_resolvers,omitempty"`
	MandatoryInApp           bool                      `json:"mandatory_in_app"`
	MinimumSeverity          string                    `json:"minimum_severity"`
	DedupeWindowSeconds      int                       `json:"dedupe_window_seconds,omitempty"`
	AggregationWindowSeconds int                       `json:"aggregation_window_seconds,omitempty"`
	ReminderIntervalSeconds  int                       `json:"reminder_interval_seconds,omitempty"`
	MaximumReminders         int                       `json:"maximum_reminders,omitempty"`
	RecoveryEventTypeKey     string                    `json:"recovery_event_type_key,omitempty"`
	AutoResolveOnRecovery    bool                      `json:"auto_resolve_on_recovery,omitempty"`
	UserMutable              bool                      `json:"user_mutable,omitempty"`
	Channels                 []NotificationRuleChannel `json:"channels,omitempty"`
}

type NotificationRuleChannel struct {
	Channel                  string `json:"channel"`
	TemplateKey              string `json:"template_key,omitempty"`
	ConnectorKey             string `json:"connector_key,omitempty"`
	ConnectionKey            string `json:"connection_key,omitempty"`
	Operation                string `json:"operation,omitempty"`
	Mandatory                bool   `json:"mandatory,omitempty"`
	DelaySeconds             int    `json:"delay_seconds,omitempty"`
	EscalationStep           int    `json:"escalation_step,omitempty"`
	CancelWhenActionTerminal bool   `json:"cancel_when_action_terminal,omitempty"`
	DeliveryMode             string `json:"delivery_mode,omitempty"`
	DigestWindowSeconds      int    `json:"digest_window_seconds,omitempty"`
	DigestMaximumItems       int    `json:"digest_maximum_items,omitempty"`
}

// NotificationGovernanceCatalog is the immutable, effective notification
// contract compiled from built-ins and the versioned Runtime manifest.
type NotificationGovernanceCatalog struct {
	EventTypes []NotificationEventType `json:"event_types"`
	Rules      []NotificationRule      `json:"rules"`
}

// NotificationInboxAggregate contains only non-identifying operational
// counters. It intentionally excludes recipients, rendered content, subjects,
// variables, correlation IDs and trace IDs.
type NotificationInboxAggregate struct {
	Key            string `json:"key"`
	Items          int    `json:"items"`
	Occurrences    int    `json:"occurrences"`
	Unread         int    `json:"unread"`
	ActionRequired int    `json:"action_required"`
	ActiveAlerts   int    `json:"active_alerts"`
}

type NotificationInboxGovernanceMetrics struct {
	Since       string                          `json:"since"`
	GeneratedAt string                          `json:"generated_at"`
	Summary     NotificationInboxAggregate      `json:"summary"`
	Failures    NotificationEventFailureMetrics `json:"failures"`
	ByEventType []NotificationInboxAggregate    `json:"by_event_type"`
	ByCategory  []NotificationInboxAggregate    `json:"by_category"`
	BySeverity  []NotificationInboxAggregate    `json:"by_severity"`
	BySource    []NotificationInboxAggregate    `json:"by_source"`
	BySurface   []NotificationInboxAggregate    `json:"by_surface"`
}

type NotificationEventFailureAggregate struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// NotificationEventFailureMetrics exposes only non-identifying operational
// evidence. Individual event IDs, source IDs, recipients, content and traces
// stay outside the ordinary Admin governance response.
type NotificationEventFailureMetrics struct {
	Total          int                                 `json:"total"`
	RetryScheduled int                                 `json:"retry_scheduled"`
	DeadLetter     int                                 `json:"dead_letter"`
	ByStage        []NotificationEventFailureAggregate `json:"by_stage"`
	ByErrorCode    []NotificationEventFailureAggregate `json:"by_error_code"`
}
