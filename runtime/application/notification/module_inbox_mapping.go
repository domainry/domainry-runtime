package notification

import (
	"errors"
	"fmt"

	sourcenotification "github.com/domainry/domainry-notification"
	sourcedelivery "github.com/domainry/domainry-notification/delivery"
	sourceinbox "github.com/domainry/domainry-notification/inbox"
	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func moduleWorkspaceID(value string) sourcenotification.WorkspaceID {
	return sourcenotification.WorkspaceID(value)
}

func moduleUserID(value string) sourcenotification.UserID { return sourcenotification.UserID(value) }

func moduleSurface(value surfacemodel.ProductSurface) sourcenotification.Surface {
	return sourcenotification.Surface(value)
}

func moduleUserIDs(values []string) []sourcenotification.UserID {
	result := make([]sourcenotification.UserID, len(values))
	for index, value := range values {
		result[index] = sourcenotification.UserID(value)
	}
	return result
}

func planeUserIDs(values []sourcenotification.UserID) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.String()
	}
	return result
}

func moduleInboxQuery(value notificationmodel.NotificationInboxQuery) sourceinbox.Query {
	actionStates := make([]sourceinbox.ActionState, len(value.ActionStates))
	for index, state := range value.ActionStates {
		actionStates[index] = sourceinbox.ActionState(state)
	}
	return sourceinbox.Query{
		WorkspaceID: moduleWorkspaceID(value.WorkspaceID), ViewerUserID: moduleUserID(value.ViewerUserID), RecipientUserID: moduleUserID(value.RecipientUserID),
		RecipientUserIDs: moduleUserIDs(value.RecipientUserIDs), ReportingUserIDs: moduleUserIDs(value.ReportingUserIDs), DelegatedUserIDs: moduleUserIDs(value.DelegatedUserIDs),
		Surface: sourcenotification.Surface(value.Surface), Scope: sourceinbox.Scope(value.Scope), Mailbox: sourceinbox.Mailbox(value.Mailbox), Query: value.Query,
		Categories: append([]string(nil), value.Categories...), Sources: append([]string(nil), value.Sources...), Severities: append([]string(nil), value.Severities...),
		ActionStates: actionStates, From: value.From, To: value.To, BeforeUpdatedAt: value.BeforeUpdatedAt, BeforeID: value.BeforeID, Limit: value.Limit,
	}
}

func moduleInboxIntent(value notificationmodel.NotificationIntent) sourceinbox.Intent {
	return sourceinbox.Intent{
		ID: value.ID, WorkspaceID: moduleWorkspaceID(value.WorkspaceID), SourceEventID: value.SourceEventID, EventType: value.EventType,
		Severity: value.Severity, Surface: sourcenotification.Surface(value.Surface), RecipientUserIDs: moduleUserIDs(value.RecipientUserIDs),
		AudienceResolverKeys: append([]string(nil), value.AudienceResolverKeys...), SubjectType: value.SubjectType, SubjectID: value.SubjectID, SubjectVersion: value.SubjectVersion,
		GroupKey: value.GroupKey, DedupeKey: value.DedupeKey, ActionState: sourceinbox.ActionState(value.ActionState), AlertState: sourceinbox.AlertState(value.AlertState),
		ExpiresAt: value.ExpiresAt, OccurredAt: value.OccurredAt, Locale: value.Locale, Variables: cloneModuleVariables(value.Variables),
		CorrelationID: value.CorrelationID, TraceID: value.TraceID,
	}
}

func moduleInboxEvent(value notificationmodel.NotificationEvent) sourceinbox.Event {
	return notificationcontract.ModuleInboxEvent(value)
}

func planeInboxEvent(value sourceinbox.Event) notificationmodel.NotificationEvent {
	localized := make(map[string]notificationmodel.NotificationInboxSnapshot, len(value.LocalizedSnapshots))
	for locale, snapshot := range value.LocalizedSnapshots {
		localized[locale] = planeInboxSnapshot(snapshot)
	}
	plans := make([]notificationmodel.NotificationChannelPlan, len(value.ChannelPlans))
	for index, plan := range value.ChannelPlans {
		plans[index] = planeDeliveryPlan(plan)
	}
	return notificationmodel.NotificationEvent{
		ID: value.ID, WorkspaceID: value.WorkspaceID.String(), Source: value.Source, SourceEventID: value.SourceEventID, EventType: value.EventType,
		Category: value.Category, Severity: value.Severity, Surface: string(value.Surface), RecipientUserIDs: planeUserIDs(value.RecipientUserIDs),
		AudienceResolverKeys: append([]string(nil), value.AudienceResolverKeys...), SubjectType: value.SubjectType, SubjectID: value.SubjectID, SubjectVersion: value.SubjectVersion,
		GroupKey: value.GroupKey, DedupeKey: value.DedupeKey, ActionState: string(value.ActionState), AlertState: string(value.AlertState),
		ExpiresAt: value.ExpiresAt, OccurredAt: value.OccurredAt, CorrelationID: value.CorrelationID, TraceID: value.TraceID,
		Snapshot: planeInboxSnapshot(value.Snapshot), LocalizedSnapshots: localized, ChannelPlans: plans, Status: string(value.Status),
		AttemptCount: value.AttemptCount, NextAttemptAt: value.NextAttemptAt, LastErrorCode: value.LastErrorCode, LeaseOwner: value.LeaseOwner,
		LeaseExpiresAt: value.LeaseExpiresAt, FencingToken: value.FencingToken, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func planeInboxSnapshot(value sourceinbox.Snapshot) notificationmodel.NotificationInboxSnapshot {
	facts := make([]notificationmodel.NotificationTemplateFact, len(value.Facts))
	for index, fact := range value.Facts {
		facts[index] = notificationmodel.NotificationTemplateFact{Key: fact.Key, Value: fact.Value}
	}
	actions := make([]notificationmodel.NotificationInboxActionRef, len(value.Actions))
	for index, action := range value.Actions {
		actions[index] = notificationmodel.NotificationInboxActionRef{Key: action.Key, Kind: action.Kind, Label: action.Label, ResourceType: action.ResourceType, ResourceID: action.ResourceID, Style: action.Style}
	}
	return notificationmodel.NotificationInboxSnapshot{
		Title: value.Title, Body: value.Body, Facts: facts, Actions: actions, TemplateKey: value.TemplateKey,
		TemplateVersion: value.TemplateVersion, TemplateLocale: value.TemplateLocale, TemplateContentHash: value.TemplateContentHash,
	}
}

func planeDeliveryPlan(value sourcedelivery.Plan) notificationmodel.NotificationChannelPlan {
	return notificationmodel.NotificationChannelPlan{
		ID: value.ID, WorkspaceID: value.WorkspaceID.String(), EventID: value.EventID, Channel: value.Channel, TemplateKey: value.TemplateKey,
		ConnectorKey: value.ConnectorKey, ConnectionKey: value.ConnectionKey, Operation: value.Operation, RecipientUserIDs: planeUserIDs(value.RecipientUserIDs),
		Locale: value.Locale, Variables: cloneModuleVariables(value.Variables), DedupeKey: value.DedupeKey, Mandatory: value.Mandatory,
		DeliveryMode: value.DeliveryMode, DigestKey: value.DigestKey, DigestMaximumItems: value.DigestMaximumItems,
		DigestItemTitle: value.DigestItemTitle, DigestItemBody: value.DigestItemBody, EscalationStep: value.EscalationStep,
		CancelWhenActionTerminal: value.CancelWhenActionTerminal, Status: value.Status, AttemptCount: value.AttemptCount,
		NextAttemptAt: value.NextAttemptAt, LastErrorCode: value.LastErrorCode, OutboxMessageID: value.OutboxMessageID,
		LeaseOwner: value.LeaseOwner, LeaseExpiresAt: value.LeaseExpiresAt, FencingToken: value.FencingToken, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func cloneModuleVariables(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func planeInboxItem(value sourceinbox.Item) notificationmodel.NotificationInboxItem {
	facts := make([]notificationmodel.NotificationTemplateFact, len(value.Facts))
	for index, fact := range value.Facts {
		facts[index] = notificationmodel.NotificationTemplateFact{Key: fact.Key, Value: fact.Value}
	}
	actions := make([]notificationmodel.NotificationInboxActionRef, len(value.Actions))
	for index, action := range value.Actions {
		actions[index] = notificationmodel.NotificationInboxActionRef{Key: action.Key, Kind: action.Kind, Label: action.Label, ResourceType: action.ResourceType, ResourceID: action.ResourceID, Style: action.Style}
	}
	return notificationmodel.NotificationInboxItem{
		ID: value.ID, WorkspaceID: value.WorkspaceID.String(), RecipientUserID: value.RecipientUserID.String(), Surface: string(value.Surface),
		EventID: value.EventID, EventType: value.EventType, Source: value.Source, Category: value.Category, Severity: value.Severity,
		Title: value.Title, Body: value.Body, Facts: facts, Actions: actions,
		TemplateKey: value.TemplateKey, TemplateVersion: value.TemplateVersion, TemplateLocale: value.TemplateLocale, TemplateContentHash: value.TemplateContentHash,
		SubjectType: value.SubjectType, SubjectID: value.SubjectID, SubjectVersion: value.SubjectVersion,
		ActionState: string(value.ActionState), AlertState: string(value.AlertState), GroupKey: value.GroupKey, OccurrenceCount: value.OccurrenceCount,
		FirstOccurredAt: value.FirstOccurredAt, LastOccurredAt: value.LastOccurredAt, ReadAt: value.ReadAt, ArchivedAt: value.ArchivedAt,
		ExpiresAt: value.ExpiresAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func planeInboxPage(value sourceinbox.Page) notificationmodel.NotificationInboxPage {
	items := make([]notificationmodel.NotificationInboxItem, len(value.Items))
	for index, item := range value.Items {
		items[index] = planeInboxItem(item)
	}
	return notificationmodel.NotificationInboxPage{Items: items, NextCursor: value.NextCursor, HasMore: value.HasMore}
}

func planeResolvedAction(value sourceinbox.ResolvedAction) notificationmodel.NotificationInboxResolvedAction {
	params := make(map[string]string, len(value.RouteParams))
	for key, parameter := range value.RouteParams {
		params[key] = parameter
	}
	return notificationmodel.NotificationInboxResolvedAction{
		Key: value.Key, Label: value.Label, Style: value.Style, NavigationKind: value.NavigationKind,
		RouteKey: value.RouteKey, RouteParams: params, Status: value.Status,
	}
}

func planeInboxFacets(value sourceinbox.Facets) notificationmodel.NotificationInboxFacets {
	return notificationmodel.NotificationInboxFacets{
		Unread: value.Unread, ActionRequired: value.ActionRequired,
		Categories: planeInboxFacetList(value.Categories), Sources: planeInboxFacetList(value.Sources), Severities: planeInboxFacetList(value.Severities),
	}
}

func planeInboxFacetList(values []sourceinbox.Facet) []notificationmodel.NotificationInboxFacet {
	result := make([]notificationmodel.NotificationInboxFacet, len(values))
	for index, value := range values {
		result[index] = notificationmodel.NotificationInboxFacet{Key: value.Key, Count: value.Count}
	}
	return result
}

func moduleDelegation(value notificationmodel.NotificationInboxDelegation) sourceinbox.Delegation {
	return sourceinbox.Delegation{
		ID: value.ID, WorkspaceID: moduleWorkspaceID(value.WorkspaceID), OwnerUserID: moduleUserID(value.OwnerUserID), DelegateUserID: moduleUserID(value.DelegateUserID),
		Surface: sourcenotification.Surface(value.Surface), StartsAt: value.StartsAt, EndsAt: value.EndsAt, Enabled: value.Enabled, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func planeDelegation(value sourceinbox.Delegation) notificationmodel.NotificationInboxDelegation {
	return notificationmodel.NotificationInboxDelegation{
		ID: value.ID, WorkspaceID: value.WorkspaceID.String(), OwnerUserID: value.OwnerUserID.String(), DelegateUserID: value.DelegateUserID.String(),
		Surface: string(value.Surface), StartsAt: value.StartsAt, EndsAt: value.EndsAt, Enabled: value.Enabled, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func planeDelegations(values []sourceinbox.Delegation) []notificationmodel.NotificationInboxDelegation {
	result := make([]notificationmodel.NotificationInboxDelegation, len(values))
	for index, value := range values {
		result[index] = planeDelegation(value)
	}
	return result
}

func moduleSavedView(value notificationmodel.NotificationInboxSavedView) sourceinbox.SavedView {
	actionStates := make([]sourceinbox.ActionState, len(value.ActionStates))
	for index, state := range value.ActionStates {
		actionStates[index] = sourceinbox.ActionState(state)
	}
	return sourceinbox.SavedView{
		Key: value.Key, Name: value.Name, Mailbox: sourceinbox.Mailbox(value.Mailbox), Scope: sourceinbox.Scope(value.Scope), TeamMemberID: moduleUserID(value.TeamMemberID), Query: value.Query,
		Categories: append([]string(nil), value.Categories...), Sources: append([]string(nil), value.Sources...), Severities: append([]string(nil), value.Severities...),
		ActionStates: actionStates, From: value.From, To: value.To, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func planeSavedView(value sourceinbox.SavedView) notificationmodel.NotificationInboxSavedView {
	actionStates := make([]string, len(value.ActionStates))
	for index, state := range value.ActionStates {
		actionStates[index] = string(state)
	}
	return notificationmodel.NotificationInboxSavedView{
		Key: value.Key, Name: value.Name, Mailbox: string(value.Mailbox), Scope: string(value.Scope), TeamMemberID: value.TeamMemberID.String(), Query: value.Query,
		Categories: append([]string(nil), value.Categories...), Sources: append([]string(nil), value.Sources...), Severities: append([]string(nil), value.Severities...),
		ActionStates: actionStates, From: value.From, To: value.To, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func planeSavedViews(values []sourceinbox.SavedView) []notificationmodel.NotificationInboxSavedView {
	result := make([]notificationmodel.NotificationInboxSavedView, len(values))
	for index, value := range values {
		result[index] = planeSavedView(value)
	}
	return result
}

func planeGovernanceMetrics(value sourceinbox.GovernanceMetrics) notificationmodel.NotificationInboxGovernanceMetrics {
	return notificationmodel.NotificationInboxGovernanceMetrics{
		Since: value.Since, GeneratedAt: value.GeneratedAt, Summary: planeAggregate(value.Summary),
		Failures: notificationmodel.NotificationEventFailureMetrics{
			Total: value.Failures.Total, RetryScheduled: value.Failures.RetryScheduled, DeadLetter: value.Failures.DeadLetter,
			ByStage: planeFailureAggregates(value.Failures.ByStage), ByErrorCode: planeFailureAggregates(value.Failures.ByErrorCode),
		},
		ByEventType: planeAggregates(value.ByEventType), ByCategory: planeAggregates(value.ByCategory), BySeverity: planeAggregates(value.BySeverity),
		BySource: planeAggregates(value.BySource), BySurface: planeAggregates(value.BySurface),
	}
}

func planeAggregate(value sourceinbox.Aggregate) notificationmodel.NotificationInboxAggregate {
	return notificationmodel.NotificationInboxAggregate{Key: value.Key, Items: value.Items, Occurrences: value.Occurrences, Unread: value.Unread, ActionRequired: value.ActionRequired, ActiveAlerts: value.ActiveAlerts}
}

func planeAggregates(values []sourceinbox.Aggregate) []notificationmodel.NotificationInboxAggregate {
	result := make([]notificationmodel.NotificationInboxAggregate, len(values))
	for index, value := range values {
		result[index] = planeAggregate(value)
	}
	return result
}

func planeFailureAggregates(values []sourceinbox.FailureAggregate) []notificationmodel.NotificationEventFailureAggregate {
	result := make([]notificationmodel.NotificationEventFailureAggregate, len(values))
	for index, value := range values {
		result[index] = notificationmodel.NotificationEventFailureAggregate{Key: value.Key, Count: value.Count}
	}
	return result
}

func mapNotificationModuleError(err error) error {
	if err == nil {
		return nil
	}
	kind := map[sourcenotification.ErrorKind]apperror.ErrorKind{
		sourcenotification.ErrorInvalid: apperror.KindBadRequest, sourcenotification.ErrorForbidden: apperror.KindForbidden,
		sourcenotification.ErrorNotFound: apperror.KindNotFound, sourcenotification.ErrorConflict: apperror.KindConflict,
		sourcenotification.ErrorUnavailable: apperror.KindUnavailable, sourcenotification.ErrorInternal: apperror.KindInternal,
	}[sourcenotification.ErrorKindOf(err)]
	if kind == "" {
		kind = apperror.KindInternal
	}
	params := map[string]string{}
	var sourceError *sourcenotification.Error
	if errors.As(err, &sourceError) {
		for key, value := range sourceError.Params {
			params[key] = fmt.Sprint(value)
		}
	}
	return &apperror.AppError{Kind: kind, Code: sourcenotification.ErrorCode(err), Params: params, Err: err}
}
