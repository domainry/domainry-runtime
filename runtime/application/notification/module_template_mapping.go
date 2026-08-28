package notification

import (
	sourcetemplate "github.com/domainry/domainry-notification/template"
	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

func moduleTemplate(value notificationmodel.NotificationTemplate) sourcetemplate.Template {
	return notificationcontract.ModuleTemplate(value)
}

func planeTemplate(value sourcetemplate.Template) notificationmodel.NotificationTemplate {
	return notificationcontract.PlaneTemplate(value)
}
func planeProviderTemplate(value *sourcetemplate.ProviderTemplate) *notificationmodel.NotificationProviderTemplate {
	if value == nil {
		return nil
	}
	components := make([]notificationmodel.NotificationProviderTemplateComponent, len(value.Components))
	for i, v := range value.Components {
		components[i] = notificationmodel.NotificationProviderTemplateComponent{Type: v.Type, SubType: v.SubType, Index: v.Index, Parameters: append([]string(nil), v.Parameters...)}
	}
	return &notificationmodel.NotificationProviderTemplate{Name: value.Name, Language: value.Language, Components: components}
}

func planeTemplateRecord(value sourcetemplate.Record) notificationmodel.NotificationTemplateRecord {
	return notificationcontract.PlaneTemplateRecord(value)
}
func planeTemplateRecords(values []sourcetemplate.Record) []notificationmodel.NotificationTemplateRecord {
	result := make([]notificationmodel.NotificationTemplateRecord, len(values))
	for i, v := range values {
		result[i] = planeTemplateRecord(v)
	}
	return result
}
func planeTemplateVersions(values []sourcetemplate.Version) []notificationmodel.NotificationTemplateVersion {
	result := make([]notificationmodel.NotificationTemplateVersion, len(values))
	for i, v := range values {
		result[i] = notificationmodel.NotificationTemplateVersion{TemplateKey: v.TemplateKey, Version: v.Version, Template: planeTemplate(v.Template), ContentHash: v.ContentHash, PublishedBy: v.PublishedBy, PublishedAt: v.PublishedAt}
	}
	return result
}
func planePublicationRequest(value sourcetemplate.PublicationRequest) notificationmodel.NotificationPublicationRequest {
	return notificationmodel.NotificationPublicationRequest{ID: value.ID, TemplateKey: value.TemplateKey, Snapshot: planeTemplate(value.Snapshot), CandidateHash: value.CandidateHash, DraftUpdatedAt: value.DraftUpdatedAt, Status: string(value.Status), ScheduledFor: value.ScheduledFor, RequestedBy: value.RequestedBy, RequestedAt: value.RequestedAt, ReviewedBy: value.ReviewedBy, ReviewedAt: value.ReviewedAt, PublishedVersion: value.PublishedVersion, Failure: value.Failure, LeaseOwner: value.LeaseOwner, LeaseExpiresAt: value.LeaseExpiresAt, FencingToken: value.FencingToken, UpdatedAt: value.UpdatedAt}
}
func planePublicationRequests(values []sourcetemplate.PublicationRequest) []notificationmodel.NotificationPublicationRequest {
	result := make([]notificationmodel.NotificationPublicationRequest, len(values))
	for i, v := range values {
		result[i] = planePublicationRequest(v)
	}
	return result
}
func planeRenderedNotification(value sourcetemplate.Rendered) notificationmodel.RenderedNotification {
	facts := make([]notificationmodel.NotificationTemplateFact, len(value.Facts))
	for i, v := range value.Facts {
		facts[i] = notificationmodel.NotificationTemplateFact{Key: v.Key, Value: v.Value}
	}
	actions := make([]notificationmodel.NotificationTemplateAction, len(value.Actions))
	for i, v := range value.Actions {
		actions[i] = notificationmodel.NotificationTemplateAction{Label: v.Label, URL: v.URL, Style: v.Style}
	}
	fallbacks := make([]notificationmodel.NotificationFallback, len(value.Fallbacks))
	for i, v := range value.Fallbacks {
		fallbacks[i] = notificationmodel.NotificationFallback{TemplateKey: v.TemplateKey, ConnectorKey: v.ConnectorKey, ConnectionKey: v.ConnectionKey, Operation: v.Operation}
	}
	return notificationmodel.RenderedNotification{Channel: value.Channel, Provider: value.Provider, Recipients: append([]string(nil), value.Recipients...), Subject: value.Subject, Title: value.Title, Text: value.Text, HTML: value.HTML, Markdown: value.Markdown, Message: value.Message, Facts: facts, Actions: actions, ProviderTemplate: planeProviderTemplate(value.ProviderTemplate), TemplateKey: value.TemplateKey, TemplateVersion: value.TemplateVersion, TemplateLocale: value.TemplateLocale, TemplateContentHash: value.TemplateContentHash, VariablesHash: value.VariablesHash, Metadata: cloneModuleVariables(value.Metadata), Fallbacks: fallbacks}
}
