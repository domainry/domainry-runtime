package contract

import (
	sourcetemplate "github.com/domainry/domainry-notification-sdk/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

// ModuleTemplate and PlaneTemplate form the host anti-corruption projection
// shared by HTTP/application adapters and manifest restoration persistence.
func ModuleTemplate(value notificationmodel.NotificationTemplate) sourcetemplate.NotificationTemplate {
	variables := make([]sourcetemplate.NotificationTemplateVariable, len(value.Variables))
	for i, v := range value.Variables {
		variables[i] = sourcetemplate.NotificationTemplateVariable{Key: v.Key, Type: v.Type, Required: v.Required}
	}
	fallbacks := make([]sourcetemplate.NotificationFallback, len(value.Fallbacks))
	for i, v := range value.Fallbacks {
		fallbacks[i] = sourcetemplate.NotificationFallback{TemplateKey: v.TemplateKey, ConnectorKey: v.ConnectorKey, ConnectionKey: v.ConnectionKey, Operation: v.Operation}
	}
	locales := make(map[string]sourcetemplate.NotificationTemplateContent, len(value.Locales))
	for key, content := range value.Locales {
		locales[key] = moduleContent(content)
	}
	return sourcetemplate.NotificationTemplate{Key: value.Key, Name: value.Name, Channel: value.Channel, Provider: value.Provider, Status: value.Status, Version: value.Version, DefaultLocale: value.DefaultLocale, Variables: variables, Fallbacks: fallbacks, Locales: locales, ContentHash: value.ContentHash}
}

func PlaneTemplate(value sourcetemplate.NotificationTemplate) notificationmodel.NotificationTemplate {
	variables := make([]notificationmodel.NotificationTemplateVariable, len(value.Variables))
	for i, v := range value.Variables {
		variables[i] = notificationmodel.NotificationTemplateVariable{Key: v.Key, Type: v.Type, Required: v.Required}
	}
	fallbacks := make([]notificationmodel.NotificationFallback, len(value.Fallbacks))
	for i, v := range value.Fallbacks {
		fallbacks[i] = notificationmodel.NotificationFallback{TemplateKey: v.TemplateKey, ConnectorKey: v.ConnectorKey, ConnectionKey: v.ConnectionKey, Operation: v.Operation}
	}
	locales := make(map[string]notificationmodel.NotificationTemplateContent, len(value.Locales))
	for key, content := range value.Locales {
		locales[key] = planeContent(content)
	}
	return notificationmodel.NotificationTemplate{Key: value.Key, Name: value.Name, Channel: value.Channel, Provider: value.Provider, Status: value.Status, Version: value.Version, DefaultLocale: value.DefaultLocale, Variables: variables, Fallbacks: fallbacks, Locales: locales, ContentHash: value.ContentHash}
}

func PlaneTemplateRecord(value sourcetemplate.NotificationTemplateRecord) notificationmodel.NotificationTemplateRecord {
	result := notificationmodel.NotificationTemplateRecord{Key: value.Key, PublishedVersion: value.PublishedVersion, Status: value.Status, UpdatedBy: value.UpdatedBy, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
	if value.Draft != nil {
		draft := PlaneTemplate(*value.Draft)
		result.Draft = &draft
	}
	if value.Published != nil {
		published := PlaneTemplate(*value.Published)
		result.Published = &published
	}
	return result
}

func moduleContent(value notificationmodel.NotificationTemplateContent) sourcetemplate.NotificationTemplateContent {
	facts := make([]sourcetemplate.NotificationTemplateFact, len(value.Facts))
	for i, v := range value.Facts {
		facts[i] = sourcetemplate.NotificationTemplateFact{Key: v.Key, Value: v.Value}
	}
	actions := make([]sourcetemplate.NotificationTemplateAction, len(value.Actions))
	for i, v := range value.Actions {
		actions[i] = sourcetemplate.NotificationTemplateAction{Label: v.Label, URL: v.URL, Style: v.Style}
	}
	var provider *sourcetemplate.NotificationProviderTemplate
	if value.ProviderTemplate != nil {
		components := make([]sourcetemplate.NotificationProviderTemplateComponent, len(value.ProviderTemplate.Components))
		for i, v := range value.ProviderTemplate.Components {
			components[i] = sourcetemplate.NotificationProviderTemplateComponent{Type: v.Type, SubType: v.SubType, Index: v.Index, Parameters: append([]string(nil), v.Parameters...)}
		}
		provider = &sourcetemplate.NotificationProviderTemplate{Name: value.ProviderTemplate.Name, Language: value.ProviderTemplate.Language, Components: components}
	}
	return sourcetemplate.NotificationTemplateContent{Subject: value.Subject, Title: value.Title, Text: value.Text, HTML: value.HTML, Markdown: value.Markdown, Facts: facts, Actions: actions, ProviderTemplate: provider}
}

func planeContent(value sourcetemplate.NotificationTemplateContent) notificationmodel.NotificationTemplateContent {
	facts := make([]notificationmodel.NotificationTemplateFact, len(value.Facts))
	for i, v := range value.Facts {
		facts[i] = notificationmodel.NotificationTemplateFact{Key: v.Key, Value: v.Value}
	}
	actions := make([]notificationmodel.NotificationTemplateAction, len(value.Actions))
	for i, v := range value.Actions {
		actions[i] = notificationmodel.NotificationTemplateAction{Label: v.Label, URL: v.URL, Style: v.Style}
	}
	var provider *notificationmodel.NotificationProviderTemplate
	if value.ProviderTemplate != nil {
		components := make([]notificationmodel.NotificationProviderTemplateComponent, len(value.ProviderTemplate.Components))
		for i, v := range value.ProviderTemplate.Components {
			components[i] = notificationmodel.NotificationProviderTemplateComponent{Type: v.Type, SubType: v.SubType, Index: v.Index, Parameters: append([]string(nil), v.Parameters...)}
		}
		provider = &notificationmodel.NotificationProviderTemplate{Name: value.ProviderTemplate.Name, Language: value.ProviderTemplate.Language, Components: components}
	}
	return notificationmodel.NotificationTemplateContent{Subject: value.Subject, Title: value.Title, Text: value.Text, HTML: value.HTML, Markdown: value.Markdown, Facts: facts, Actions: actions, ProviderTemplate: provider}
}
