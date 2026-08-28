package notification

import (
	sourcedelivery "github.com/domainry/domainry-notification/delivery"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

func moduleDeliveryPolicy(value notificationmodel.NotificationDeliveryPolicy) sourcedelivery.Policy {
	return sourcedelivery.Policy{
		Enabled: value.Enabled, QuietHoursEnabled: value.QuietHoursEnabled, QuietStart: value.QuietStart, QuietEnd: value.QuietEnd,
		Timezone: value.Timezone, MaxPerRecipientPerHour: value.MaxPerRecipientPerHour, DedupeWindowSeconds: value.DedupeWindowSeconds,
		FallbackChannels: append([]string(nil), value.FallbackChannels...), UpdatedBy: value.UpdatedBy, UpdatedAt: value.UpdatedAt,
	}
}

func planeDeliveryPolicy(value sourcedelivery.Policy) notificationmodel.NotificationDeliveryPolicy {
	return notificationmodel.NotificationDeliveryPolicy{
		Enabled: value.Enabled, QuietHoursEnabled: value.QuietHoursEnabled, QuietStart: value.QuietStart, QuietEnd: value.QuietEnd,
		Timezone: value.Timezone, MaxPerRecipientPerHour: value.MaxPerRecipientPerHour, DedupeWindowSeconds: value.DedupeWindowSeconds,
		FallbackChannels: append([]string(nil), value.FallbackChannels...), UpdatedBy: value.UpdatedBy, UpdatedAt: value.UpdatedAt,
	}
}

func moduleRecipientPreference(value notificationmodel.NotificationRecipientPreference) sourcedelivery.RecipientPreference {
	enabled := make(map[string]bool, len(value.EnabledChannels))
	for channel, channelEnabled := range value.EnabledChannels {
		enabled[channel] = channelEnabled
	}
	return sourcedelivery.RecipientPreference{
		RecipientKey: value.RecipientKey, EnabledChannels: enabled, MutedTemplateKeys: append([]string(nil), value.MutedTemplateKeys...),
		UpdatedBy: value.UpdatedBy, UpdatedAt: value.UpdatedAt,
	}
}

func planeRecipientPreference(value sourcedelivery.RecipientPreference) notificationmodel.NotificationRecipientPreference {
	enabled := make(map[string]bool, len(value.EnabledChannels))
	for channel, channelEnabled := range value.EnabledChannels {
		enabled[channel] = channelEnabled
	}
	return notificationmodel.NotificationRecipientPreference{
		RecipientKey: value.RecipientKey, EnabledChannels: enabled, MutedTemplateKeys: append([]string(nil), value.MutedTemplateKeys...),
		UpdatedBy: value.UpdatedBy, UpdatedAt: value.UpdatedAt,
	}
}

func planeRecipientPreferences(values []sourcedelivery.RecipientPreference) []notificationmodel.NotificationRecipientPreference {
	result := make([]notificationmodel.NotificationRecipientPreference, len(values))
	for index, value := range values {
		result[index] = planeRecipientPreference(value)
	}
	return result
}

func moduleDeliveryEvaluation(value notificationmodel.NotificationDeliveryEvaluationRequest) sourcedelivery.Evaluation {
	return sourcedelivery.Evaluation{
		WorkspaceID: moduleWorkspaceID(value.WorkspaceID), TemplateKey: value.TemplateKey, Channel: value.Channel,
		Recipients: moduleUserIDs(value.Recipients), DedupeKey: value.DedupeKey,
	}
}

func planeDeliveryDecision(value sourcedelivery.Decision) notificationmodel.NotificationDeliveryDecision {
	return notificationmodel.NotificationDeliveryDecision{DeliverAfter: value.DeliverAfter, FallbackOrder: append([]string(nil), value.FallbackOrder...)}
}
