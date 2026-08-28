package validation

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

const (
	notificationInboxTitleLimit     = 240
	notificationInboxBodyLimit      = 4000
	notificationInboxFactLimit      = 20
	notificationInboxActionLimit    = 5
	notificationInboxRecipientLimit = 500
)

var notificationInboxSeverities = map[string]bool{"info": true, "warning": true, "critical": true}
var notificationInboxSurfaces = map[string]bool{"business_workspace": true, "consumer_portal": true}
var notificationInboxActionStates = map[string]bool{
	notificationmodel.NotificationActionNone: true, notificationmodel.NotificationActionOpen: true,
	notificationmodel.NotificationActionCompleted: true, notificationmodel.NotificationActionExpired: true,
	notificationmodel.NotificationActionCancelled: true,
}
var notificationInboxAlertStates = map[string]bool{
	"": true, notificationmodel.NotificationAlertFiring: true, notificationmodel.NotificationAlertResolved: true,
}

func NotificationValidateInboxEvent(value notificationmodel.NotificationEvent) (notificationmodel.NotificationEvent, error) {
	value.ID = strings.TrimSpace(value.ID)
	value.WorkspaceID = strings.TrimSpace(value.WorkspaceID)
	value.Source, value.SourceEventID = strings.TrimSpace(value.Source), strings.TrimSpace(value.SourceEventID)
	value.EventType, value.Category = strings.TrimSpace(value.EventType), strings.TrimSpace(value.Category)
	value.Severity, value.Surface = strings.TrimSpace(value.Severity), strings.TrimSpace(value.Surface)
	value.SubjectType, value.SubjectID, value.SubjectVersion = strings.TrimSpace(value.SubjectType), strings.TrimSpace(value.SubjectID), strings.TrimSpace(value.SubjectVersion)
	value.GroupKey, value.DedupeKey = strings.TrimSpace(value.GroupKey), strings.TrimSpace(value.DedupeKey)
	value.ActionState, value.AlertState = strings.TrimSpace(value.ActionState), strings.TrimSpace(value.AlertState)
	value.ExpiresAt, value.OccurredAt = strings.TrimSpace(value.ExpiresAt), strings.TrimSpace(value.OccurredAt)
	value.Snapshot.Title, value.Snapshot.Body = strings.TrimSpace(value.Snapshot.Title), strings.TrimSpace(value.Snapshot.Body)
	value.AudienceResolverKeys = notificationUniqueInboxRecipients(value.AudienceResolverKeys)
	if len(value.AudienceResolverKeys) > 8 {
		return value, notificationBadRequest("backend.notification.inbox_audience_resolvers_invalid")
	}
	for _, resolverKey := range value.AudienceResolverKeys {
		if !stableTemplateKeyPattern.MatchString(resolverKey) {
			return value, notificationBadRequest("backend.notification.inbox_audience_resolvers_invalid")
		}
	}
	if value.ID == "" || value.WorkspaceID == "" || value.SourceEventID == "" {
		return value, notificationBadRequest("backend.notification.inbox_event_identity_required")
	}
	for field, text := range map[string]string{"source": value.Source, "event_type": value.EventType, "category": value.Category} {
		if !stableTemplateKeyPattern.MatchString(text) {
			return value, notificationBadRequest("backend.notification.inbox_event_key_invalid", "field", field, "value", text)
		}
	}
	if !notificationInboxSeverities[value.Severity] {
		return value, notificationBadRequest("backend.notification.inbox_event_severity_invalid", "severity", value.Severity)
	}
	if !notificationInboxSurfaces[value.Surface] {
		return value, notificationBadRequest("backend.notification.inbox_event_surface_invalid", "surface", value.Surface)
	}
	if value.ActionState == "" {
		value.ActionState = notificationmodel.NotificationActionNone
	}
	if !notificationInboxActionStates[value.ActionState] {
		return value, notificationBadRequest("backend.notification.inbox_action_state_invalid", "action_state", value.ActionState)
	}
	if !notificationInboxAlertStates[value.AlertState] || (value.AlertState != "" && value.GroupKey == "") {
		return value, notificationBadRequest("backend.notification.inbox_alert_state_invalid", "alert_state", value.AlertState)
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, value.OccurredAt)
	if err != nil {
		return value, notificationBadRequest("backend.notification.inbox_event_time_invalid", "field", "occurred_at")
	}
	value.OccurredAt = notificationmodel.NotificationTimestamp(occurredAt)
	if value.ExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339Nano, value.ExpiresAt)
		if err != nil {
			return value, notificationBadRequest("backend.notification.inbox_event_time_invalid", "field", "expires_at")
		}
		value.ExpiresAt = notificationmodel.NotificationTimestamp(expiresAt)
	}
	if value.Snapshot.Title == "" || utf8.RuneCountInString(value.Snapshot.Title) > notificationInboxTitleLimit {
		return value, notificationBadRequest("backend.notification.inbox_title_invalid")
	}
	if notificationInboxLooksLikeLink(value.Snapshot.Title) {
		return value, notificationBadRequest("backend.notification.inbox_title_link_forbidden")
	}
	if value.Snapshot.Body == "" || utf8.RuneCountInString(value.Snapshot.Body) > notificationInboxBodyLimit {
		return value, notificationBadRequest("backend.notification.inbox_body_invalid")
	}
	if len(value.Snapshot.Facts) > notificationInboxFactLimit || len(value.Snapshot.Actions) > notificationInboxActionLimit {
		return value, notificationBadRequest("backend.notification.inbox_components_limit")
	}
	for _, fact := range value.Snapshot.Facts {
		if strings.TrimSpace(fact.Key) == "" || strings.TrimSpace(fact.Value) == "" {
			return value, notificationBadRequest("backend.notification.inbox_fact_invalid")
		}
	}
	for index, action := range value.Snapshot.Actions {
		validatedAction, err := notificationValidateInboxAction(action)
		if err != nil {
			return value, err
		}
		value.Snapshot.Actions[index] = validatedAction
	}
	for locale, snapshot := range value.LocalizedSnapshots {
		if strings.TrimSpace(locale) == "" {
			return value, notificationBadRequest("backend.notification.inbox_snapshot_locale_invalid")
		}
		candidate := value
		candidate.Snapshot, candidate.LocalizedSnapshots = snapshot, nil
		validated, err := NotificationValidateInboxEvent(candidate)
		if err != nil {
			return value, err
		}
		value.LocalizedSnapshots[locale] = validated.Snapshot
	}
	value.RecipientUserIDs = notificationUniqueInboxRecipients(value.RecipientUserIDs)
	if (len(value.RecipientUserIDs) == 0 && len(value.AudienceResolverKeys) == 0) || len(value.RecipientUserIDs) > notificationInboxRecipientLimit {
		return value, notificationBadRequest("backend.notification.inbox_recipients_invalid")
	}
	value.Status = notificationmodel.NotificationEventQueued
	value.AttemptCount, value.NextAttemptAt, value.LastErrorCode = 0, "", ""
	value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken = "", "", 0
	return value, nil
}

func notificationValidateInboxAction(action notificationmodel.NotificationInboxActionRef) (notificationmodel.NotificationInboxActionRef, error) {
	key, kind, label := strings.TrimSpace(action.Key), strings.TrimSpace(action.Kind), strings.TrimSpace(action.Label)
	resourceType, resourceID := strings.TrimSpace(action.ResourceType), strings.TrimSpace(action.ResourceID)
	if !stableTemplateKeyPattern.MatchString(key) || label == "" || notificationInboxLooksLikeLink(label) || (kind != "route" && kind != "business_action") {
		return action, notificationBadRequest("backend.notification.inbox_action_invalid", "action_key", key)
	}
	if !stableTemplateKeyPattern.MatchString(resourceType) || resourceID == "" || notificationInboxLooksLikeLink(resourceID) {
		return action, notificationBadRequest("backend.notification.inbox_action_resource_invalid", "action_key", key)
	}
	style := strings.TrimSpace(action.Style)
	if style != "" && style != "primary" && style != "secondary" && style != "danger" {
		return action, notificationBadRequest("backend.notification.inbox_action_style_invalid", "action_key", key)
	}
	action.Key, action.Kind, action.Label = key, kind, label
	action.ResourceType, action.ResourceID, action.Style = resourceType, resourceID, style
	return action, nil
}

func notificationInboxLooksLikeLink(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(value, "://") || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "www.")
}

func notificationUniqueInboxRecipients(values []string) []string {
	seen, result := map[string]bool{}, make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func NotificationValidateInboxQuery(value notificationmodel.NotificationInboxQuery) (notificationmodel.NotificationInboxQuery, error) {
	value.WorkspaceID, value.ViewerUserID, value.Surface = strings.TrimSpace(value.WorkspaceID), strings.TrimSpace(value.ViewerUserID), strings.TrimSpace(value.Surface)
	value.Scope, value.RecipientUserID = strings.TrimSpace(value.Scope), strings.TrimSpace(value.RecipientUserID)
	value.Mailbox, value.Query = strings.TrimSpace(value.Mailbox), strings.TrimSpace(value.Query)
	if value.WorkspaceID == "" || value.ViewerUserID == "" || !notificationInboxSurfaces[value.Surface] {
		return value, notificationBadRequest("backend.notification.inbox_scope_invalid")
	}
	if value.Scope == "" {
		value.Scope = notificationmodel.NotificationInboxScopeMine
	}
	if value.Scope != notificationmodel.NotificationInboxScopeMine && value.Scope != notificationmodel.NotificationInboxScopeTeam && value.Scope != notificationmodel.NotificationInboxScopeDelegated {
		return value, notificationBadRequest("backend.notification.inbox_audience_scope_invalid")
	}
	value.ReportingUserIDs = notificationUniqueInboxRecipients(value.ReportingUserIDs)
	value.DelegatedUserIDs = notificationUniqueInboxRecipients(value.DelegatedUserIDs)
	if value.Scope == notificationmodel.NotificationInboxScopeTeam {
		if len(value.ReportingUserIDs) == 0 || (value.RecipientUserID != "" && !notificationInboxContains(value.ReportingUserIDs, value.RecipientUserID)) {
			return value, notificationBadRequest("backend.notification.inbox_team_scope_denied")
		}
		value.RecipientUserIDs = append([]string(nil), value.ReportingUserIDs...)
		if value.RecipientUserID != "" {
			value.RecipientUserIDs = []string{value.RecipientUserID}
		}
	} else if value.Scope == notificationmodel.NotificationInboxScopeDelegated {
		if len(value.DelegatedUserIDs) == 0 || (value.RecipientUserID != "" && !notificationInboxContains(value.DelegatedUserIDs, value.RecipientUserID)) {
			return value, notificationBadRequest("backend.notification.inbox_delegated_scope_denied")
		}
		value.RecipientUserIDs = append([]string(nil), value.DelegatedUserIDs...)
		if value.RecipientUserID != "" {
			value.RecipientUserIDs = []string{value.RecipientUserID}
		}
	} else if value.RecipientUserID != "" && value.RecipientUserID != value.ViewerUserID {
		return value, notificationBadRequest("backend.notification.inbox_team_filter_invalid")
	} else {
		value.RecipientUserID = value.ViewerUserID
		value.RecipientUserIDs = []string{value.ViewerUserID}
	}
	if value.Mailbox == "" {
		value.Mailbox = notificationmodel.NotificationMailboxInbox
	}
	switch value.Mailbox {
	case notificationmodel.NotificationMailboxInbox, notificationmodel.NotificationMailboxUnread,
		notificationmodel.NotificationMailboxActionRequired, notificationmodel.NotificationMailboxArchived:
	default:
		return value, notificationBadRequest("backend.notification.inbox_mailbox_invalid", "mailbox", value.Mailbox)
	}
	if value.Limit <= 0 {
		value.Limit = 50
	}
	if value.Limit > 100 {
		value.Limit = 100
	}
	if len(value.Query) > 200 {
		return value, notificationBadRequest("backend.notification.inbox_query_invalid")
	}
	var err error
	if value.From, err = notificationNormalizeInboxQueryTime("from", value.From); err != nil {
		return value, err
	}
	if value.To, err = notificationNormalizeInboxQueryTime("to", value.To); err != nil {
		return value, err
	}
	return value, nil
}

func notificationInboxContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func notificationNormalizeInboxQueryTime(field, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", notificationBadRequest("backend.notification.inbox_query_time_invalid", "field", field)
	}
	return notificationmodel.NotificationTimestamp(parsed), nil
}

func NotificationValidateInboxSavedView(value notificationmodel.NotificationInboxSavedView) (notificationmodel.NotificationInboxSavedView, error) {
	value.Key, value.Name = strings.TrimSpace(value.Key), strings.TrimSpace(value.Name)
	value.TeamMemberID = strings.TrimSpace(value.TeamMemberID)
	if !stableTemplateKeyPattern.MatchString(value.Key) || value.Name == "" || utf8.RuneCountInString(value.Name) > 80 {
		return value, notificationBadRequest("backend.notification.inbox_saved_view_invalid")
	}
	reportingID := value.TeamMemberID
	if reportingID == "" {
		reportingID = "saved-view-report"
	}
	query, err := NotificationValidateInboxQuery(notificationmodel.NotificationInboxQuery{
		WorkspaceID: "saved-view", ViewerUserID: "saved-view", Surface: "business_workspace", Scope: value.Scope,
		RecipientUserID: value.TeamMemberID, ReportingUserIDs: []string{reportingID},
		Mailbox: value.Mailbox, Query: value.Query, Categories: value.Categories, Sources: value.Sources,
		Severities: value.Severities, ActionStates: value.ActionStates, From: value.From, To: value.To, Limit: 1,
	})
	if err != nil {
		return value, fmt.Errorf("saved view: %w", err)
	}
	value.Mailbox, value.Query, value.Scope = query.Mailbox, query.Query, query.Scope
	return value, nil
}
