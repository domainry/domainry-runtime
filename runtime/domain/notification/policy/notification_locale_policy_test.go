package policy

import (
	"testing"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

func TestNotificationSelectTemplateContentFallbackOrder(t *testing.T) {
	template := notificationmodel.NotificationTemplate{DefaultLocale: "en-US", Locales: map[string]notificationmodel.NotificationTemplateContent{
		"zh-CN": {Subject: "请求"}, "en-US": {Subject: "Request"}, "fr-FR": {Subject: "Demande"},
	}}
	for _, test := range []struct {
		requested, runtime string
		wantLocale         string
		wantSubject        string
	}{{" zh-CN ", "fr-FR", "zh-CN", "请求"}, {"missing", " fr-FR ", "fr-FR", "Demande"}, {"missing", "also-missing", "en-US", "Request"}, {"", "", "en-US", "Request"}} {
		locale, content := NotificationSelectTemplateContent(template, test.requested, test.runtime)
		if locale != test.wantLocale || content.Subject != test.wantSubject {
			t.Fatalf("selection %q/%q = %q/%#v", test.requested, test.runtime, locale, content)
		}
	}
	template.DefaultLocale = "missing"
	if locale, content := NotificationSelectTemplateContent(template, "", ""); locale != "" || content.Subject != "" {
		t.Fatalf("missing selection = %q/%#v", locale, content)
	}
}
