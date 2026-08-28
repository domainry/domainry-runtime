package notificationmodel

import notificationcontract "github.com/domainry/domainry-notification-sdk/contract"

const (
	NotificationEventQueued       = notificationcontract.NotificationEventQueued
	NotificationEventProcessing   = notificationcontract.NotificationEventProcessing
	NotificationEventMaterialized = notificationcontract.NotificationEventMaterialized
	NotificationEventFailed       = notificationcontract.NotificationEventFailed

	NotificationMailboxInbox          = notificationcontract.NotificationMailboxInbox
	NotificationMailboxUnread         = notificationcontract.NotificationMailboxUnread
	NotificationMailboxActionRequired = notificationcontract.NotificationMailboxActionRequired
	NotificationMailboxArchived       = notificationcontract.NotificationMailboxArchived

	NotificationActionNone      = notificationcontract.NotificationActionNone
	NotificationActionOpen      = notificationcontract.NotificationActionOpen
	NotificationActionCompleted = notificationcontract.NotificationActionCompleted
	NotificationActionExpired   = notificationcontract.NotificationActionExpired
	NotificationActionCancelled = notificationcontract.NotificationActionCancelled

	NotificationInboxScopeMine      = notificationcontract.NotificationInboxScopeMine
	NotificationInboxScopeTeam      = notificationcontract.NotificationInboxScopeTeam
	NotificationInboxScopeDelegated = notificationcontract.NotificationInboxScopeDelegated

	NotificationAlertFiring       = notificationcontract.NotificationAlertFiring
	NotificationAlertAcknowledged = notificationcontract.NotificationAlertAcknowledged
	NotificationAlertResolved     = notificationcontract.NotificationAlertResolved
)

type NotificationInboxActionRef = notificationcontract.NotificationInboxActionRef
type NotificationInboxResolvedAction = notificationcontract.NotificationInboxResolvedAction
type NotificationInboxActionDescriptor = notificationcontract.NotificationInboxActionDescriptor
type NotificationInboxSnapshot = notificationcontract.NotificationInboxSnapshot
type NotificationEvent = notificationcontract.NotificationEvent
type NotificationEventFailure = notificationcontract.NotificationEventFailure
type NotificationAlertGroup = notificationcontract.NotificationAlertGroup
type NotificationChannelPlan = notificationcontract.NotificationChannelPlan
type NotificationInboxItem = notificationcontract.NotificationInboxItem
type NotificationInboxDelegation = notificationcontract.NotificationInboxDelegation
type NotificationInboxPage = notificationcontract.NotificationInboxPage
type NotificationInboxFacet = notificationcontract.NotificationInboxFacet
type NotificationInboxFacets = notificationcontract.NotificationInboxFacets
type NotificationInboxSavedView = notificationcontract.NotificationInboxSavedView

// NotificationInboxQuery contains Runtime BFF authorization filters in addition
// to the portable user query. The facade projects only the portable fields into
// contract.NotificationInboxQuery after resolving team/delegation membership.
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
