package contract

import (
	"fmt"
	"regexp"
	"strings"

	sourcetemplate "github.com/domainry/domainry-notification-sdk/contract"
)

// NotificationTemplateCapabilityCatalog composes the immutable module catalog
// from host-installed provider capabilities. Provider-owned connector
// contribution can replace this static host adapter when the Connector SDK
// exposes that boundary.
func NotificationTemplateCapabilityCatalog() (*sourcetemplate.NotificationTemplateCapabilities, error) {
	providers := make([]sourcetemplate.NotificationTemplateProvider, 0, len(notificationProviderCapabilities))
	for _, value := range NotificationProviderCapabilities() {
		provider := sourcetemplate.NotificationTemplateProvider{Capability: sourcetemplate.NotificationTemplateCapability{Channel: value.Channel, Provider: value.Provider, SupportsHTML: value.SupportsHTML, SupportsMarkdown: value.SupportsMarkdown, SupportsFacts: value.SupportsFacts, SupportsURLActions: value.SupportsURLActions, SupportsProviderTemplate: value.SupportsProviderTemplate, MaxFacts: value.MaxFacts, MaxActions: value.MaxActions}}
		if value.SupportsProviderTemplate {
			provider.ValidateProviderTemplate = validateWhatsAppProviderTemplate
		}
		providers = append(providers, provider)
	}
	return sourcetemplate.NewNotificationTemplateCapabilities(providers)
}

var notificationProviderTemplateName = regexp.MustCompile(`^[a-z0-9_]+$`)
var notificationProviderTemplateLanguage = regexp.MustCompile(`^[a-z]{2,3}([_-][A-Z]{2})?$`)
var notificationProviderButtonIndex = regexp.MustCompile(`^[0-9]$`)

func validateWhatsAppProviderTemplate(value *sourcetemplate.NotificationProviderTemplate) error {
	if value == nil || !notificationProviderTemplateName.MatchString(strings.TrimSpace(value.Name)) || !notificationProviderTemplateLanguage.MatchString(strings.TrimSpace(value.Language)) || len(value.Components) > 12 {
		return fmt.Errorf("invalid WhatsApp provider template")
	}
	for _, component := range value.Components {
		typeName, subtype, index := strings.TrimSpace(component.Type), strings.TrimSpace(component.SubType), strings.TrimSpace(component.Index)
		if (typeName == "header" || typeName == "body") && (subtype != "" || index != "") {
			return fmt.Errorf("invalid WhatsApp provider template component")
		}
		if typeName == "button" && ((subtype != "url" && subtype != "quick_reply") || !notificationProviderButtonIndex.MatchString(index)) {
			return fmt.Errorf("invalid WhatsApp provider template button")
		}
		if typeName != "header" && typeName != "body" && typeName != "button" {
			return fmt.Errorf("invalid WhatsApp provider template component")
		}
		if len(component.Parameters) == 0 || len(component.Parameters) > 10 {
			return fmt.Errorf("invalid WhatsApp provider template parameters")
		}
		for _, parameter := range component.Parameters {
			if strings.TrimSpace(parameter) == "" {
				return fmt.Errorf("invalid WhatsApp provider template parameter")
			}
		}
	}
	return nil
}
