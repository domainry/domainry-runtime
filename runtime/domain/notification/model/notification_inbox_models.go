package notificationmodel

const (
	NotificationEventQueued       = "queued"
	NotificationEventProcessing   = "processing"
	NotificationEventMaterialized = "materialized"
	NotificationEventFailed       = "failed"

	NotificationMailboxInbox          = "inbox"
	NotificationMailboxUnread         = "unread"
	NotificationMailboxActionRequired = "action_required"
	NotificationMailboxArchived       = "archived"

	NotificationActionNone      = "none"
	NotificationActionOpen      = "open"
	NotificationActionCompleted = "completed"
	NotificationActionExpired   = "expired"
	NotificationActionCancelled = "cancelled"

	NotificationInboxScopeMine      = "mine"
	NotificationInboxScopeTeam      = "team"
	NotificationInboxScopeDelegated = "delegated"

	NotificationAlertFiring       = "firing"
	NotificationAlertAcknowledged = "acknowledged"
	NotificationAlertResolved     = "resolved"
)

// NotificationInboxActionRef is a semantic, server-authorized action. It is
// intentionally not an arbitrary URL or HTTP request.
type NotificationInboxActionRef struct {
	Key          string `json:"key"`
	Kind         string `json:"kind"`
	Label        string `json:"label"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Style        string `json:"style,omitempty"`
}

// NotificationInboxResolvedAction is safe navigation output for the current
// Product Surface. RouteKey is interpreted by the project-owned router; a
// persisted message never supplies a literal URL or API request.
type NotificationInboxResolvedAction struct {
	Key            string            `json:"key"`
	Label          string            `json:"label"`
	Style          string            `json:"style,omitempty"`
	NavigationKind string            `json:"navigation_kind"`
	RouteKey       string            `json:"route_key"`
	RouteParams    map[string]string `json:"route_params"`
	Status         string            `json:"status"`
}

type NotificationInboxActionDescriptor struct {
	Key           string            `json:"key"`
	Kind          string            `json:"kind"`
	ResourceType  string            `json:"resource_type"`
	SurfaceRoutes map[string]string `json:"surface_routes"`
}

// NotificationInboxSnapshot is the immutable, already-sanitized in-app
// projection compiled for one event. Template evidence remains attached so
// later template edits cannot rewrite historical messages.
type NotificationInboxSnapshot struct {
	Title               string                       `json:"title"`
	Body                string                       `json:"body"`
	Facts               []NotificationTemplateFact   `json:"facts,omitempty"`
	Actions             []NotificationInboxActionRef `json:"actions,omitempty"`
	TemplateKey         string                       `json:"template_key,omitempty"`
	TemplateVersion     int                          `json:"template_version,omitempty"`
	TemplateLocale      string                       `json:"template_locale,omitempty"`
	TemplateContentHash string                       `json:"template_content_hash,omitempty"`
}

// NotificationEvent is the durable intent emitted by a source owner. The
// source identity is unique inside a workspace and makes publication safe to
// retry. Recipients are explicit user identities or the result of a trusted
// resolver before the event crosses into this owner.
type NotificationEvent struct {
	ID                   string                               `json:"id"`
	WorkspaceID          string                               `json:"workspace_id"`
	Source               string                               `json:"source"`
	SourceEventID        string                               `json:"source_event_id"`
	EventType            string                               `json:"event_type"`
	Category             string                               `json:"category"`
	Severity             string                               `json:"severity"`
	Surface              string                               `json:"surface"`
	RecipientUserIDs     []string                             `json:"recipient_user_ids"`
	AudienceResolverKeys []string                             `json:"audience_resolver_keys,omitempty"`
	SubjectType          string                               `json:"subject_type,omitempty"`
	SubjectID            string                               `json:"subject_id,omitempty"`
	SubjectVersion       string                               `json:"subject_version,omitempty"`
	GroupKey             string                               `json:"group_key,omitempty"`
	DedupeKey            string                               `json:"dedupe_key,omitempty"`
	ActionState          string                               `json:"action_state"`
	AlertState           string                               `json:"alert_state,omitempty"`
	ExpiresAt            string                               `json:"expires_at,omitempty"`
	OccurredAt           string                               `json:"occurred_at"`
	CorrelationID        string                               `json:"correlation_id,omitempty"`
	TraceID              string                               `json:"trace_id,omitempty"`
	Snapshot             NotificationInboxSnapshot            `json:"snapshot"`
	LocalizedSnapshots   map[string]NotificationInboxSnapshot `json:"localized_snapshots,omitempty"`
	ChannelPlans         []NotificationChannelPlan            `json:"channel_plans,omitempty"`
	Status               string                               `json:"status"`
	AttemptCount         int                                  `json:"attempt_count"`
	NextAttemptAt        string                               `json:"next_attempt_at,omitempty"`
	LastErrorCode        string                               `json:"last_error_code,omitempty"`
	LeaseOwner           string                               `json:"lease_owner,omitempty"`
	LeaseExpiresAt       string                               `json:"lease_expires_at,omitempty"`
	FencingToken         int64                                `json:"fencing_token"`
	CreatedAt            string                               `json:"created_at"`
	UpdatedAt            string                               `json:"updated_at"`
}

// NotificationEventFailure is append-only, non-sensitive processing evidence.
// It deliberately stores only stable catalog/error identifiers and never the
// event payload, rendered copy, recipients, trace data, or provider response.
type NotificationEventFailure struct {
	ID            string `json:"id"`
	WorkspaceID   string `json:"workspace_id"`
	EventID       string `json:"event_id"`
	EventType     string `json:"event_type"`
	Source        string `json:"source"`
	SourceEventID string `json:"source_event_id"`
	Stage         string `json:"stage"`
	ErrorCode     string `json:"error_code"`
	Attempt       int    `json:"attempt"`
	Disposition   string `json:"disposition"`
	Retryable     bool   `json:"retryable"`
	NextAttemptAt string `json:"next_attempt_at,omitempty"`
	FencingToken  int64  `json:"fencing_token"`
	OccurredAt    string `json:"occurred_at"`
}

// NotificationAlertGroup is the durable lifecycle of a continuing alert for
// one recipient and Product Surface. Inbox rows mirror this state for fast
// mailbox queries, but acknowledgements and module-owned recovery transitions
// are governed by this record.
type NotificationAlertGroup struct {
	WorkspaceID     string `json:"workspace_id"`
	RecipientUserID string `json:"recipient_user_id"`
	Surface         string `json:"surface"`
	GroupKey        string `json:"group_key"`
	State           string `json:"state"`
	OccurrenceCount int    `json:"occurrence_count"`
	FirstOccurredAt string `json:"first_occurred_at"`
	LastOccurredAt  string `json:"last_occurred_at"`
	AcknowledgedAt  string `json:"acknowledged_at,omitempty"`
	AcknowledgedBy  string `json:"acknowledged_by,omitempty"`
	ResolvedAt      string `json:"resolved_at,omitempty"`
	LastEventID     string `json:"last_event_id"`
	UpdatedAt       string `json:"updated_at"`
}

// NotificationChannelPlan is an independently retryable external projection
// of one notification event. In-app materialization has its own lifecycle and
// is never rolled back because an email or collaboration connector fails.
type NotificationChannelPlan struct {
	ID                       string         `json:"id"`
	WorkspaceID              string         `json:"workspace_id"`
	EventID                  string         `json:"event_id"`
	Channel                  string         `json:"channel"`
	TemplateKey              string         `json:"template_key"`
	ConnectorKey             string         `json:"connector_key"`
	ConnectionKey            string         `json:"connection_key,omitempty"`
	Operation                string         `json:"operation"`
	RecipientUserIDs         []string       `json:"recipient_user_ids"`
	Locale                   string         `json:"locale,omitempty"`
	Variables                map[string]any `json:"variables,omitempty"`
	DedupeKey                string         `json:"dedupe_key,omitempty"`
	Mandatory                bool           `json:"mandatory,omitempty"`
	DeliveryMode             string         `json:"delivery_mode,omitempty"`
	DigestKey                string         `json:"digest_key,omitempty"`
	DigestMaximumItems       int            `json:"digest_maximum_items,omitempty"`
	DigestItemTitle          string         `json:"digest_item_title,omitempty"`
	DigestItemBody           string         `json:"digest_item_body,omitempty"`
	EscalationStep           int            `json:"escalation_step,omitempty"`
	CancelWhenActionTerminal bool           `json:"cancel_when_action_terminal,omitempty"`
	Status                   string         `json:"status"`
	AttemptCount             int            `json:"attempt_count"`
	NextAttemptAt            string         `json:"next_attempt_at,omitempty"`
	LastErrorCode            string         `json:"last_error_code,omitempty"`
	OutboxMessageID          string         `json:"outbox_message_id,omitempty"`
	LeaseOwner               string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt           string         `json:"lease_expires_at,omitempty"`
	FencingToken             int64          `json:"fencing_token"`
	CreatedAt                string         `json:"created_at"`
	UpdatedAt                string         `json:"updated_at"`
}

type NotificationInboxItem struct {
	ID                  string                       `json:"id"`
	WorkspaceID         string                       `json:"workspace_id"`
	RecipientUserID     string                       `json:"recipient_user_id"`
	Surface             string                       `json:"surface"`
	EventID             string                       `json:"event_id"`
	EventType           string                       `json:"event_type"`
	Source              string                       `json:"source"`
	Category            string                       `json:"category"`
	Severity            string                       `json:"severity"`
	Title               string                       `json:"title"`
	Body                string                       `json:"body"`
	Facts               []NotificationTemplateFact   `json:"facts,omitempty"`
	Actions             []NotificationInboxActionRef `json:"actions,omitempty"`
	TemplateKey         string                       `json:"template_key,omitempty"`
	TemplateVersion     int                          `json:"template_version,omitempty"`
	TemplateLocale      string                       `json:"template_locale,omitempty"`
	TemplateContentHash string                       `json:"template_content_hash,omitempty"`
	SubjectType         string                       `json:"subject_type,omitempty"`
	SubjectID           string                       `json:"subject_id,omitempty"`
	SubjectVersion      string                       `json:"subject_version,omitempty"`
	ActionState         string                       `json:"action_state"`
	AlertState          string                       `json:"alert_state,omitempty"`
	GroupKey            string                       `json:"group_key,omitempty"`
	OccurrenceCount     int                          `json:"occurrence_count"`
	FirstOccurredAt     string                       `json:"first_occurred_at"`
	LastOccurredAt      string                       `json:"last_occurred_at"`
	ReadAt              string                       `json:"read_at,omitempty"`
	ArchivedAt          string                       `json:"archived_at,omitempty"`
	ExpiresAt           string                       `json:"expires_at,omitempty"`
	CreatedAt           string                       `json:"created_at"`
	UpdatedAt           string                       `json:"updated_at"`
}

type NotificationInboxQuery struct {
	WorkspaceID      string
	ViewerUserID     string
	RecipientUserID  string
	RecipientUserIDs []string
	ReportingUserIDs []string
	DelegatedUserIDs []string
	Surface          string
	Scope            string
	Mailbox          string
	Query            string
	Categories       []string
	Sources          []string
	Severities       []string
	ActionStates     []string
	From             string
	To               string
	BeforeUpdatedAt  string
	BeforeID         string
	Limit            int
}

// NotificationInboxDelegation grants a delegate read-only access to the
// owner's inbox for one Product Surface. It never copies notifications and
// never grants authority to mutate the owner's mailbox or business resource.
type NotificationInboxDelegation struct {
	ID             string `json:"id"`
	WorkspaceID    string `json:"workspace_id"`
	OwnerUserID    string `json:"owner_user_id"`
	DelegateUserID string `json:"delegate_user_id"`
	Surface        string `json:"surface"`
	StartsAt       string `json:"starts_at,omitempty"`
	EndsAt         string `json:"ends_at,omitempty"`
	Enabled        bool   `json:"enabled"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type NotificationInboxPage struct {
	Items      []NotificationInboxItem `json:"items"`
	NextCursor string                  `json:"next_cursor,omitempty"`
	HasMore    bool                    `json:"has_more"`
}

type NotificationInboxFacet struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type NotificationInboxFacets struct {
	Unread         int                      `json:"unread"`
	ActionRequired int                      `json:"action_required"`
	Categories     []NotificationInboxFacet `json:"categories"`
	Sources        []NotificationInboxFacet `json:"sources"`
	Severities     []NotificationInboxFacet `json:"severities"`
}

type NotificationInboxSavedView struct {
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	Mailbox      string   `json:"mailbox"`
	Scope        string   `json:"scope"`
	TeamMemberID string   `json:"team_member_id,omitempty"`
	Query        string   `json:"query,omitempty"`
	Categories   []string `json:"categories,omitempty"`
	Sources      []string `json:"sources,omitempty"`
	Severities   []string `json:"severities,omitempty"`
	ActionStates []string `json:"action_states,omitempty"`
	From         string   `json:"from,omitempty"`
	To           string   `json:"to,omitempty"`
	CreatedAt    string   `json:"created_at,omitempty"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
}
