package projection

import (
	"strings"
	"testing"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

func TestNotificationPortableComponentsAndPublication(t *testing.T) {
	facts := []notificationmodel.NotificationTemplateFact{{Key: " <Key> ", Value: " value "}}
	actions := []notificationmodel.NotificationTemplateAction{{Label: " <Open> ", URL: "https://example.test/?a=1&b=2"}}
	text := NotificationAppendPortableTextComponents(" body ", facts, actions)
	if !strings.Contains(text, "<Key>: value") || !strings.Contains(text, "<Open>:") {
		t.Fatalf("portable text = %q", text)
	}
	plainText, unchangedHTML := NotificationAppendPortableEmailComponents("text", "<p>existing</p>", nil, nil)
	if plainText != "text" || unchangedHTML != "<p>existing</p>" {
		t.Fatal("empty email components changed content")
	}
	_, appendedHTML := NotificationAppendPortableEmailComponents("text", "<p>existing</p>", facts, actions)
	if !strings.Contains(appendedHTML, "&lt;Key&gt;") || !strings.Contains(appendedHTML, "&amp;") {
		t.Fatalf("appended HTML = %q", appendedHTML)
	}
	generatedText, generatedHTML := NotificationAppendPortableEmailComponents("line1\nline2", " ", facts, actions)
	if generatedText == "" || !strings.Contains(generatedHTML, "<br>") {
		t.Fatalf("generated email = %q / %q", generatedText, generatedHTML)
	}
	_, actionsOnly := NotificationAppendPortableEmailComponents("text", "<p>existing</p>", nil, actions)
	_, factsOnly := NotificationAppendPortableEmailComponents("text", "<p>existing</p>", facts, nil)
	if !strings.Contains(actionsOnly, "notification-actions") || !strings.Contains(factsOnly, "notification-facts") {
		t.Fatalf("single component HTML = %q / %q", actionsOnly, factsOnly)
	}
	if got := notificationNonEmptyStrings([]string{"", " a ", " ", "b"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("non-empty = %#v", got)
	}

	published := notificationmodel.NotificationTemplate{Key: "published"}
	records := []notificationmodel.NotificationTemplateRecord{{Status: "active", Published: &published}, {Status: "active"}, {Status: "draft", Published: &published}}
	if got := ActivePublishedTemplates(records); len(got) != 1 || got[0].Key != "published" {
		t.Fatalf("published = %#v", got)
	}
}
