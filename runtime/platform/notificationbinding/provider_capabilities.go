// Package notificationbinding contains Runtime-owned adapters around the
// deployment-neutral Notification SDK. It must not contain Notification
// domain behavior or persistence.
package notificationbinding

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	contract "github.com/domainry/domainry-notification-sdk/contract"
)

var providerCapabilities = map[string]contract.NotificationTemplateCapability{
	"email/":                          {Channel: "email", SupportsHTML: true, SupportsFacts: true, SupportsURLActions: true, MaxFacts: 10, MaxActions: 5},
	"whatsapp/meta_cloud_api":         {Channel: "whatsapp", Provider: "meta_cloud_api", SupportsFacts: true, SupportsURLActions: true, SupportsProviderTemplate: true, MaxFacts: 10, MaxActions: 5},
	"collaboration/feishu":            collaborationCapability("feishu", 5),
	"collaboration/dingtalk":          collaborationCapability("dingtalk", 5),
	"collaboration/enterprise_wechat": collaborationCapability("enterprise_wechat", 3),
	"collaboration/slack":             collaborationCapability("slack", 5),
	"collaboration/teams":             collaborationCapability("teams", 5),
	"collaboration/microsoft_365":     collaborationCapability("microsoft_365", 5),
	"collaboration/discord":           collaborationCapability("discord", 5),
	"collaboration/google_workspace":  collaborationCapability("google_workspace", 5),
	"collaboration/line":              collaborationCapability("line", 5),
	"collaboration/line_works":        collaborationCapability("line_works", 5),
}

func collaborationCapability(provider string, maxActions int) contract.NotificationTemplateCapability {
	return contract.NotificationTemplateCapability{Channel: "collaboration", Provider: provider, SupportsMarkdown: true, SupportsFacts: true, SupportsURLActions: true, MaxFacts: 10, MaxActions: maxActions}
}

func ProviderCapabilityFor(channel, provider string) (contract.NotificationTemplateCapability, bool) {
	value, ok := providerCapabilities[channel+"/"+provider]
	return value, ok
}

func ProviderCapabilities() []contract.NotificationTemplateCapability {
	result := make([]contract.NotificationTemplateCapability, 0, len(providerCapabilities))
	for _, value := range providerCapabilities {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Channel == result[j].Channel {
			return result[i].Provider < result[j].Provider
		}
		return result[i].Channel < result[j].Channel
	})
	return result
}

func TemplateCapabilityCatalog() (*contract.NotificationTemplateCapabilities, error) {
	providers := make([]contract.NotificationTemplateProvider, 0, len(providerCapabilities))
	for _, value := range ProviderCapabilities() {
		provider := contract.NotificationTemplateProvider{Capability: value}
		if value.SupportsProviderTemplate {
			provider.ValidateProviderTemplate = validateWhatsAppProviderTemplate
		}
		providers = append(providers, provider)
	}
	return contract.NewNotificationTemplateCapabilities(providers)
}

var providerTemplateName = regexp.MustCompile(`^[a-z0-9_]+$`)
var providerTemplateLanguage = regexp.MustCompile(`^[a-z]{2,3}([_-][A-Z]{2})?$`)
var providerButtonIndex = regexp.MustCompile(`^[0-9]$`)

func validateWhatsAppProviderTemplate(value *contract.NotificationProviderTemplate) error {
	if value == nil || !providerTemplateName.MatchString(strings.TrimSpace(value.Name)) || !providerTemplateLanguage.MatchString(strings.TrimSpace(value.Language)) || len(value.Components) > 12 {
		return fmt.Errorf("invalid WhatsApp provider template")
	}
	for _, component := range value.Components {
		typeName, subtype, index := strings.TrimSpace(component.Type), strings.TrimSpace(component.SubType), strings.TrimSpace(component.Index)
		if (typeName == "header" || typeName == "body") && (subtype != "" || index != "") {
			return fmt.Errorf("invalid WhatsApp provider template component")
		}
		if typeName == "button" && ((subtype != "url" && subtype != "quick_reply") || !providerButtonIndex.MatchString(index)) {
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
