package localization

import "testing"

func TestNormalizeAddedLocales(t *testing.T) {
	tests := map[string]string{
		"  ":      DefaultLocale,
		"zh-TW":   "zh-TW",
		"zh-Hant": "zh-TW",
		"es":      "es-ES",
		"es-ES":   "es-ES",
		"tr":      "tr-TR",
		"tr-TR":   "tr-TR",
	}
	for input, want := range tests {
		if got := NormalizeLocale(input); got != want {
			t.Fatalf("NormalizeLocale(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAddedLocalesAreLoaded(t *testing.T) {
	supported := map[string]bool{}
	for _, locale := range SupportedLocales() {
		supported[locale] = true
	}
	for _, locale := range []string{"zh-TW", "es-ES", "tr-TR"} {
		if !supported[locale] {
			t.Fatalf("locale %q is not loaded", locale)
		}
		if _, ok := Resources(locale, ""); !ok {
			t.Fatalf("locale %q has no resources", locale)
		}
	}
}

func TestNotificationInboxResourcesExistForEverySupportedLocale(t *testing.T) {
	required := []string{
		"notification.action.scheduler.job.open", "notification.action.workflow.task.open", "notification.action.integration.secret.open", "notification.action.integration.connection.open",
		"notification.inbox.action.acknowledgeAlert", "notification.inbox.alert.firing", "notification.inbox.alert.acknowledged", "notification.inbox.alert.resolved",
		"notification.fact.due.label", "notification.fact.due.value", "notification.fact.errorCode.label", "notification.fact.errorCode.value", "notification.fact.scheduled.label", "notification.fact.scheduled.value", "notification.fact.expiresAt.label", "notification.fact.expiresAt.value", "notification.fact.daysRemaining.label", "notification.fact.daysRemaining.value", "notification.fact.quotaUsed.label", "notification.fact.quotaUsed.value", "notification.fact.balanceBand.label", "notification.fact.balanceBand.value", "notification.fact.observedAt.label", "notification.fact.observedAt.value",
		"notification.scheduler.job.failed.title", "notification.scheduler.job.failed.body", "notification.scheduler.job.missedDeadline.title", "notification.scheduler.job.missedDeadline.body", "notification.scheduler.job.recovered.title", "notification.scheduler.job.recovered.body", "notification.scheduler.job.repeatedFailure.title", "notification.scheduler.job.repeatedFailure.body",
		"notification.workflow.task.assigned.title", "notification.workflow.task.assigned.body", "notification.workflow.task.reminded.title", "notification.workflow.task.reminded.body", "notification.workflow.task.completed.title", "notification.workflow.task.completed.body", "notification.workflow.task.cancelled.title", "notification.workflow.task.cancelled.body",
		"notification.integration.credential.expiring.title", "notification.integration.credential.expiring.body", "notification.integration.credential.expired.title", "notification.integration.credential.expired.body", "notification.integration.credential.refreshFailed.title", "notification.integration.credential.refreshFailed.body", "notification.integration.credential.recovered.title", "notification.integration.credential.recovered.body",
		"notification.integration.quota.warning.title", "notification.integration.quota.warning.body", "notification.integration.quota.exhausted.title", "notification.integration.quota.exhausted.body", "notification.integration.quota.recovered.title", "notification.integration.quota.recovered.body", "notification.integration.billing.paymentRequired.title", "notification.integration.billing.paymentRequired.body", "notification.integration.billing.recovered.title", "notification.integration.billing.recovered.body",
		"notification.inbox.title", "notification.inbox.scope.mine", "notification.inbox.scope.reporting", "notification.inbox.reporting.all",
		"notification.inbox.mailbox.inbox", "notification.inbox.mailbox.unread", "notification.inbox.mailbox.actionRequired", "notification.inbox.mailbox.archived",
		"notification.inbox.search.placeholder", "notification.inbox.category.all", "notification.inbox.empty.default", "notification.inbox.empty.search", "notification.inbox.detail.select",
		"notification.inbox.action.markRead", "notification.inbox.action.markUnread", "notification.inbox.action.archive", "notification.inbox.action.restore", "notification.inbox.action.markAllRead", "notification.inbox.action.viewAll",
		"notification.inbox.recipient", "notification.inbox.occurrenceCount", "notification.inbox.reporting.readOnly", "notification.inbox.close",
		"notification.inbox.source.all", "notification.inbox.severity.all", "notification.inbox.actionState.all", "notification.inbox.filters", "notification.inbox.filters.clear", "notification.inbox.filters.from", "notification.inbox.filters.to",
		"notification.inbox.savedViews", "notification.inbox.savedViews.save", "notification.inbox.savedViews.namePlaceholder", "notification.inbox.savedViews.delete", "notification.inbox.action.loadMore", "notification.inbox.loading", "notification.inbox.error.load",
		"notification.inbox.actionState.none", "notification.inbox.actionState.open", "notification.inbox.actionState.completed", "notification.inbox.actionState.expired", "notification.inbox.actionState.cancelled",
		"notification.inbox.severity.info", "notification.inbox.severity.warning", "notification.inbox.severity.critical",
		"notification.category.approval", "notification.category.scheduler", "notification.category.integration", "notification.category.system", "notification.category.security", "notification.category.business",
	}
	for _, locale := range SupportedLocales() {
		for _, key := range required {
			if value, ok := Lookup(locale, key); !ok || value == "" {
				t.Errorf("locale %s is missing notification resource %s", locale, key)
			}
		}
	}
}
