package contract

import (
	sourcenotification "github.com/domainry/domainry-notification"
	sourcedelivery "github.com/domainry/domainry-notification/delivery"
	sourceinbox "github.com/domainry/domainry-notification/inbox"
	sourcetemplate "github.com/domainry/domainry-notification/template"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

// ModuleInboxEvent projects the Plane transport model into the module's
// durable event contract. It is shared by application publishing and
// producer-owned transaction writers.
func ModuleInboxEvent(value notificationmodel.NotificationEvent) sourceinbox.Event {
	localized := make(map[string]sourceinbox.Snapshot, len(value.LocalizedSnapshots))
	for locale, snapshot := range value.LocalizedSnapshots {
		localized[locale] = moduleInboxSnapshot(snapshot)
	}
	plans := make([]sourcedelivery.Plan, len(value.ChannelPlans))
	for i, plan := range value.ChannelPlans {
		plans[i] = moduleDeliveryPlan(plan)
	}
	recipients := make([]sourcenotification.UserID, len(value.RecipientUserIDs))
	for i, id := range value.RecipientUserIDs {
		recipients[i] = sourcenotification.UserID(id)
	}
	return sourceinbox.Event{ID: value.ID, WorkspaceID: sourcenotification.WorkspaceID(value.WorkspaceID), Source: value.Source, SourceEventID: value.SourceEventID, EventType: value.EventType, Category: value.Category, Severity: value.Severity, Surface: sourcenotification.Surface(value.Surface), RecipientUserIDs: recipients, AudienceResolverKeys: append([]string(nil), value.AudienceResolverKeys...), SubjectType: value.SubjectType, SubjectID: value.SubjectID, SubjectVersion: value.SubjectVersion, GroupKey: value.GroupKey, DedupeKey: value.DedupeKey, ActionState: sourceinbox.ActionState(value.ActionState), AlertState: sourceinbox.AlertState(value.AlertState), ExpiresAt: value.ExpiresAt, OccurredAt: value.OccurredAt, CorrelationID: value.CorrelationID, TraceID: value.TraceID, Snapshot: moduleInboxSnapshot(value.Snapshot), LocalizedSnapshots: localized, ChannelPlans: plans, Status: sourceinbox.EventStatus(value.Status), AttemptCount: value.AttemptCount, NextAttemptAt: value.NextAttemptAt, LastErrorCode: value.LastErrorCode, LeaseOwner: value.LeaseOwner, LeaseExpiresAt: value.LeaseExpiresAt, FencingToken: value.FencingToken, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func moduleInboxSnapshot(value notificationmodel.NotificationInboxSnapshot) sourceinbox.Snapshot {
	facts := make([]sourcetemplate.Fact, len(value.Facts))
	for i, fact := range value.Facts {
		facts[i] = sourcetemplate.Fact{Key: fact.Key, Value: fact.Value}
	}
	actions := make([]sourceinbox.ActionRef, len(value.Actions))
	for i, action := range value.Actions {
		actions[i] = sourceinbox.ActionRef{Key: action.Key, Kind: action.Kind, Label: action.Label, ResourceType: action.ResourceType, ResourceID: action.ResourceID, Style: action.Style}
	}
	return sourceinbox.Snapshot{Title: value.Title, Body: value.Body, Facts: facts, Actions: actions, TemplateKey: value.TemplateKey, TemplateVersion: value.TemplateVersion, TemplateLocale: value.TemplateLocale, TemplateContentHash: value.TemplateContentHash}
}

func moduleDeliveryPlan(value notificationmodel.NotificationChannelPlan) sourcedelivery.Plan {
	recipients := make([]sourcenotification.UserID, len(value.RecipientUserIDs))
	for i, id := range value.RecipientUserIDs {
		recipients[i] = sourcenotification.UserID(id)
	}
	variables := map[string]any(nil)
	if value.Variables != nil {
		variables = make(map[string]any, len(value.Variables))
		for key, item := range value.Variables {
			variables[key] = item
		}
	}
	return sourcedelivery.Plan{ID: value.ID, WorkspaceID: sourcenotification.WorkspaceID(value.WorkspaceID), EventID: value.EventID, Channel: value.Channel, TemplateKey: value.TemplateKey, ConnectorKey: value.ConnectorKey, ConnectionKey: value.ConnectionKey, Operation: value.Operation, RecipientUserIDs: recipients, Locale: value.Locale, Variables: variables, DedupeKey: value.DedupeKey, Mandatory: value.Mandatory, DeliveryMode: value.DeliveryMode, DigestKey: value.DigestKey, DigestMaximumItems: value.DigestMaximumItems, DigestItemTitle: value.DigestItemTitle, DigestItemBody: value.DigestItemBody, EscalationStep: value.EscalationStep, CancelWhenActionTerminal: value.CancelWhenActionTerminal, Status: value.Status, AttemptCount: value.AttemptCount, NextAttemptAt: value.NextAttemptAt, LastErrorCode: value.LastErrorCode, OutboxMessageID: value.OutboxMessageID, LeaseOwner: value.LeaseOwner, LeaseExpiresAt: value.LeaseExpiresAt, FencingToken: value.FencingToken, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}
