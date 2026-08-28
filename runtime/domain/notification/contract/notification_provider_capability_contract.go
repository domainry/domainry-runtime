package contract

import "sort"

type NotificationProviderCapability struct {
	Channel                  string `json:"channel"`
	Provider                 string `json:"provider,omitempty"`
	SupportsHTML             bool   `json:"supports_html"`
	SupportsMarkdown         bool   `json:"supports_markdown"`
	SupportsFacts            bool   `json:"supports_facts"`
	SupportsURLActions       bool   `json:"supports_url_actions"`
	SupportsProviderTemplate bool   `json:"supports_provider_template"`
	MaxFacts                 int    `json:"max_facts"`
	MaxActions               int    `json:"max_actions"`
}

var notificationProviderCapabilities = map[string]NotificationProviderCapability{
	"email/":                          {Channel: "email", SupportsHTML: true, SupportsFacts: true, SupportsURLActions: true, MaxFacts: 10, MaxActions: 5},
	"whatsapp/meta_cloud_api":         {Channel: "whatsapp", Provider: "meta_cloud_api", SupportsFacts: true, SupportsURLActions: true, SupportsProviderTemplate: true, MaxFacts: 10, MaxActions: 5},
	"collaboration/feishu":            notificationCollaborationCapability("feishu", 5),
	"collaboration/dingtalk":          notificationCollaborationCapability("dingtalk", 5),
	"collaboration/enterprise_wechat": notificationCollaborationCapability("enterprise_wechat", 3),
	"collaboration/slack":             notificationCollaborationCapability("slack", 5),
	"collaboration/teams":             notificationCollaborationCapability("teams", 5),
	"collaboration/microsoft_365":     notificationCollaborationCapability("microsoft_365", 5),
	"collaboration/discord":           notificationCollaborationCapability("discord", 5),
	"collaboration/google_workspace":  notificationCollaborationCapability("google_workspace", 5),
	"collaboration/line":              notificationCollaborationCapability("line", 5),
	"collaboration/line_works":        notificationCollaborationCapability("line_works", 5),
}

func notificationCollaborationCapability(provider string, maxActions int) NotificationProviderCapability {
	return NotificationProviderCapability{Channel: "collaboration", Provider: provider, SupportsMarkdown: true, SupportsFacts: true, SupportsURLActions: true, MaxFacts: 10, MaxActions: maxActions}
}

func NotificationProviderCapabilityFor(channel, provider string) (NotificationProviderCapability, bool) {
	value, ok := notificationProviderCapabilities[channel+"/"+provider]
	return value, ok
}

func NotificationProviderCapabilities() []NotificationProviderCapability {
	result := make([]NotificationProviderCapability, 0, len(notificationProviderCapabilities))
	for _, value := range notificationProviderCapabilities {
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
