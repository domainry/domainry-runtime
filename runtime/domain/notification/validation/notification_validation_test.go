package validation

import (
	"errors"
	"strings"
	"testing"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func validEmailTemplate() notificationmodel.NotificationTemplate {
	return notificationmodel.NotificationTemplate{
		Key: "order.created", Name: "Order created", Channel: "email", Status: "published", Version: 1, DefaultLocale: "en",
		Variables: []notificationmodel.NotificationTemplateVariable{{Key: "user", Type: "text", Required: true}},
		Locales:   map[string]notificationmodel.NotificationTemplateContent{"en": {Subject: "Hello {{user}}", Text: "Created {{user}}"}},
	}
}

func notificationErrorCode(err error) string {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return ""
}

func TestNotificationTemplateValidationErrors(t *testing.T) {
	type testCase struct {
		name, code string
		mutate     func(*notificationmodel.NotificationTemplate)
	}
	cases := []testCase{
		{"key", "backend.notification.template_key_invalid", func(v *notificationmodel.NotificationTemplate) { v.Key = "Bad" }},
		{"name", "backend.notification.template_name_required", func(v *notificationmodel.NotificationTemplate) { v.Name = " " }},
		{"channel", "backend.notification.template_provider_unsupported", func(v *notificationmodel.NotificationTemplate) { v.Channel = "sms" }},
		{"collaboration provider", "backend.notification.template_provider_unsupported", func(v *notificationmodel.NotificationTemplate) { v.Channel, v.Provider = "collaboration", "bad" }},
		{"whatsapp provider", "backend.notification.template_provider_unsupported", func(v *notificationmodel.NotificationTemplate) { v.Channel, v.Provider = "whatsapp", "bad" }},
		{"unknown email provider", "backend.notification.template_provider_unsupported", func(v *notificationmodel.NotificationTemplate) { v.Provider = "smtp" }},
		{"status", "backend.notification.template_status_invalid", func(v *notificationmodel.NotificationTemplate) { v.Status = "draft" }},
		{"version", "backend.notification.template_version_invalid", func(v *notificationmodel.NotificationTemplate) { v.Version = 0 }},
		{"default locale", "backend.notification.template_default_locale_required", func(v *notificationmodel.NotificationTemplate) { v.DefaultLocale = "" }},
		{"locales", "backend.notification.template_locales_required", func(v *notificationmodel.NotificationTemplate) { v.Locales = nil }},
		{"missing default", "backend.notification.template_default_locale_missing", func(v *notificationmodel.NotificationTemplate) { v.DefaultLocale = "fr" }},
		{"variable key", "backend.notification.template_variable_key_invalid", func(v *notificationmodel.NotificationTemplate) { v.Variables[0].Key = "Bad" }},
		{"variable duplicate", "backend.notification.template_variable_duplicate", func(v *notificationmodel.NotificationTemplate) { v.Variables = append(v.Variables, v.Variables[0]) }},
		{"variable type", "backend.notification.template_variable_type_unsupported", func(v *notificationmodel.NotificationTemplate) { v.Variables[0].Type = "object" }},
		{"fallback limit", "backend.notification.fallback_limit_exceeded", func(v *notificationmodel.NotificationTemplate) {
			v.Fallbacks = make([]notificationmodel.NotificationFallback, 6)
		}},
		{"fallback self", "backend.notification.fallback_invalid", func(v *notificationmodel.NotificationTemplate) {
			v.Fallbacks = []notificationmodel.NotificationFallback{{TemplateKey: v.Key, ConnectorKey: "c", ConnectionKey: "x", Operation: "send"}}
		}},
		{"fallback duplicate", "backend.notification.fallback_duplicate", func(v *notificationmodel.NotificationTemplate) {
			f := notificationmodel.NotificationFallback{TemplateKey: "backup", ConnectorKey: "connector", ConnectionKey: "x", Operation: "send"}
			v.Fallbacks = []notificationmodel.NotificationFallback{f, f}
		}},
		{"locale", "backend.notification.template_locale_invalid", func(v *notificationmodel.NotificationTemplate) {
			v.Locales[""] = notificationmodel.NotificationTemplateContent{Subject: "x", Text: "x"}
		}},
		{"subject", "backend.notification.template_subject_required", func(v *notificationmodel.NotificationTemplate) {
			c := v.Locales["en"]
			c.Subject = ""
			v.Locales["en"] = c
		}},
		{"subject newline", "backend.notification.template_subject_invalid", func(v *notificationmodel.NotificationTemplate) {
			c := v.Locales["en"]
			c.Subject = "x\n"
			v.Locales["en"] = c
		}},
		{"email body", "backend.notification.template_body_required", func(v *notificationmodel.NotificationTemplate) {
			c := v.Locales["en"]
			c.Text = ""
			v.Locales["en"] = c
		}},
		{"collab body", "backend.notification.template_body_required", func(v *notificationmodel.NotificationTemplate) {
			v.Channel, v.Provider = "collaboration", "slack"
			c := v.Locales["en"]
			c.Subject, c.Text = "", ""
			v.Locales["en"] = c
		}},
		{"facts limit", "backend.notification.template_components_limit", func(v *notificationmodel.NotificationTemplate) {
			c := v.Locales["en"]
			c.Facts = make([]notificationmodel.NotificationTemplateFact, 11)
			v.Locales["en"] = c
		}},
		{"fact", "backend.notification.template_fact_invalid", func(v *notificationmodel.NotificationTemplate) {
			c := v.Locales["en"]
			c.Facts = []notificationmodel.NotificationTemplateFact{{}}
			v.Locales["en"] = c
		}},
		{"action", "backend.notification.template_action_invalid", func(v *notificationmodel.NotificationTemplate) {
			c := v.Locales["en"]
			c.Actions = []notificationmodel.NotificationTemplateAction{{Label: "go", URL: "x", Style: "odd"}}
			v.Locales["en"] = c
		}},
		{"provider unsupported", "backend.notification.provider_template_unsupported", func(v *notificationmodel.NotificationTemplate) {
			c := v.Locales["en"]
			c.ProviderTemplate = &notificationmodel.NotificationProviderTemplate{Name: "x", Language: "en"}
			v.Locales["en"] = c
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := validEmailTemplate()
			tc.mutate(&v)
			if got := notificationErrorCode(NotificationValidateTemplate(v)); got != tc.code {
				t.Fatalf("want %s got %s", tc.code, got)
			}
		})
	}
}

func validWhatsAppTemplate() notificationmodel.NotificationTemplate {
	v := validEmailTemplate()
	v.Channel, v.Provider = "whatsapp", "meta_cloud_api"
	v.Locales["en"] = notificationmodel.NotificationTemplateContent{
		Text: "fallback",
		ProviderTemplate: &notificationmodel.NotificationProviderTemplate{
			Name: "approved_1", Language: "en_US",
			Components: []notificationmodel.NotificationProviderTemplateComponent{
				{Type: "header", Parameters: []string{"{{user}}"}},
				{Type: "button", SubType: "url", Index: "0", Parameters: []string{"https://x/{{user}}"}},
			},
		},
	}
	return v
}

func TestNotificationProviderTemplateValidation(t *testing.T) {
	if err := NotificationValidateTemplate(validWhatsAppTemplate()); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		code   string
		mutate func(*notificationmodel.NotificationProviderTemplate)
	}{
		{"backend.notification.provider_template_invalid", func(p *notificationmodel.NotificationProviderTemplate) { p.Name = "Bad-Name" }},
		{"backend.notification.provider_template_invalid", func(p *notificationmodel.NotificationProviderTemplate) { p.Language = "english" }},
		{"backend.notification.provider_template_invalid", func(p *notificationmodel.NotificationProviderTemplate) {
			p.Components = make([]notificationmodel.NotificationProviderTemplateComponent, 13)
		}},
		{"backend.notification.provider_template_invalid", func(p *notificationmodel.NotificationProviderTemplate) { p.Components[0].SubType = "url" }},
		{"backend.notification.provider_template_invalid", func(p *notificationmodel.NotificationProviderTemplate) { p.Components[1].SubType = "bad" }},
		{"backend.notification.provider_template_invalid", func(p *notificationmodel.NotificationProviderTemplate) { p.Components[0].Type = "footer" }},
		{"backend.notification.provider_template_invalid", func(p *notificationmodel.NotificationProviderTemplate) { p.Components[0].Parameters = nil }},
		{"backend.notification.provider_template_invalid", func(p *notificationmodel.NotificationProviderTemplate) { p.Components[0].Parameters = []string{""} }},
	}
	for _, tc := range cases {
		v := validWhatsAppTemplate()
		tc.mutate(v.Locales["en"].ProviderTemplate)
		if got := notificationErrorCode(NotificationValidateTemplate(v)); got != tc.code {
			t.Fatalf("want %s got %s", tc.code, got)
		}
	}
	v := validWhatsAppTemplate()
	c := v.Locales["en"]
	c.Facts = []notificationmodel.NotificationTemplateFact{{Key: "k", Value: "v"}}
	v.Locales["en"] = c
	if got := notificationErrorCode(NotificationValidateTemplate(v)); got != "backend.notification.provider_template_components_conflict" {
		t.Fatalf("got %s", got)
	}
}

func TestNotificationTemplateCollectionEditableTokensAndHashes(t *testing.T) {
	v := validEmailTemplate()
	backup := validEmailTemplate()
	backup.Key = "backup"
	v.Fallbacks = []notificationmodel.NotificationFallback{{TemplateKey: "backup", ConnectorKey: "connector", ConnectionKey: "conn", Operation: "send"}}
	if err := NotificationValidateTemplates([]notificationmodel.NotificationTemplate{v, backup}); err != nil {
		t.Fatal(err)
	}
	if got := notificationErrorCode(NotificationValidateTemplates([]notificationmodel.NotificationTemplate{v, v})); got != "backend.notification.template_key_duplicate" {
		t.Fatalf("duplicate=%s", got)
	}
	invalid := validEmailTemplate()
	invalid.Name = ""
	if err := NotificationValidateTemplates([]notificationmodel.NotificationTemplate{invalid}); notificationErrorCode(err) != "backend.notification.template_name_required" {
		t.Fatalf("indexed validation error=%v", err)
	}
	v.Fallbacks[0].TemplateKey = "missing"
	if got := notificationErrorCode(NotificationValidateTemplates([]notificationmodel.NotificationTemplate{v})); got != "backend.notification.fallback_template_not_found" {
		t.Fatalf("fallback=%s", got)
	}
	v = validEmailTemplate()
	v.Status = "draft"
	v.ContentHash = "old"
	if err := NotificationValidateEditableTemplate(v); err != nil {
		t.Fatal(err)
	}
	v.Status = "retired"
	if got := notificationErrorCode(NotificationValidateEditableTemplate(v)); got != "backend.notification.template_status_invalid" {
		t.Fatalf("editable=%s", got)
	}
	vars := map[string]notificationmodel.NotificationTemplateVariable{"user": {Key: "user"}}
	if err := validateTemplateTokens("t", "en", "{{user.name}}", vars); err != nil {
		t.Fatal(err)
	}
	actionTemplate := validEmailTemplate()
	content := actionTemplate.Locales["en"]
	content.Actions = []notificationmodel.NotificationTemplateAction{{Label: "Open {{user}}", URL: "https://example.com/{{user}}", Style: "primary"}}
	actionTemplate.Locales["en"] = content
	if err := NotificationValidateTemplate(actionTemplate); err != nil {
		t.Fatalf("valid action: %v", err)
	}
	tokenTemplate := validEmailTemplate()
	content = tokenTemplate.Locales["en"]
	content.Text = "{{unknown}}"
	tokenTemplate.Locales["en"] = content
	if got := notificationErrorCode(NotificationValidateTemplate(tokenTemplate)); got != "backend.notification.template_variable_unknown" {
		t.Fatalf("template token=%s", got)
	}
	for source, code := range map[string]string{"{{ broken": "backend.notification.template_syntax_invalid", "{{unknown}}": "backend.notification.template_variable_unknown"} {
		if got := notificationErrorCode(validateTemplateTokens("t", "en", source, vars)); got != code {
			t.Fatalf("%q=%s", source, got)
		}
	}
	if NotificationTemplateContentHash(v) == "" || NotificationTemplateContentHash(v) != NotificationTemplateContentHash(v) || NotificationValueHash(map[string]any{"x": 1}) == "" {
		t.Fatal("hash mismatch")
	}
	if err := notificationBadRequest("x", " ", "ignored"); notificationErrorCode(err) != "x" {
		t.Fatal(err)
	}
}

func TestNotificationRenderValidation(t *testing.T) {
	v := validEmailTemplate()
	v.Variables = []notificationmodel.NotificationTemplateVariable{{Key: "required", Type: "text", Required: true}, {Key: "bool", Type: "boolean"}, {Key: "number", Type: "number"}, {Key: "date", Type: "date"}, {Key: "datetime", Type: "datetime"}, {Key: "email", Type: "email"}, {Key: "url", Type: "url"}}
	values := map[string]any{"required": "x", "bool": true, "number": "1.5", "date": "2026-01-02", "datetime": "2026-01-02T03:04:05Z", "email": "a@example.com", "url": "https://example.com"}
	got, err := NotificationValidateRenderVariables(v, values)
	if err != nil || got["required"] != "x" {
		t.Fatalf("%#v %v", got, err)
	}
	delete(values, "required")
	if notificationErrorCode(func() error { _, err := NotificationValidateRenderVariables(v, values); return err }()) != "backend.notification.template_variable_required" {
		t.Fatal("required")
	}
	for key := range map[string]bool{"bool": true, "number": true, "date": true, "datetime": true, "email": true, "url": true} {
		values = map[string]any{"required": "x", key: "bad"}
		if notificationErrorCode(func() error { _, err := NotificationValidateRenderVariables(v, values); return err }()) != "backend.notification.template_variable_invalid" {
			t.Fatalf("invalid %s", key)
		}
	}
	values = map[string]any{"user": map[string]any{"name": "<Ada>"}}
	rendered, err := NotificationRenderRestricted("Hi {{user.name}}", values, true)
	if err != nil || rendered != "Hi &lt;Ada&gt;" {
		t.Fatalf("%q %v", rendered, err)
	}
	if rendered, err = NotificationRenderRestricted("", values, false); err != nil || rendered != "" {
		t.Fatal(err)
	}
	for _, source := range []string{"{{missing}}", "{{ broken"} {
		if _, err := NotificationRenderRestricted(source, values, false); err == nil {
			t.Fatalf("expected render error %q", source)
		}
	}
	p := &notificationmodel.NotificationProviderTemplate{Name: "n", Language: "en", Components: []notificationmodel.NotificationProviderTemplateComponent{{Type: "body", Parameters: []string{"{{user.name}}"}}}}
	if got, err := NotificationRenderProviderTemplate(p, values); err != nil || got.Components[0].Parameters[0] != "<Ada>" {
		t.Fatalf("%#v %v", got, err)
	}
	if got, err := NotificationRenderProviderTemplate(nil, values); err != nil || got != nil {
		t.Fatal(err)
	}
	if _, err := NotificationRenderProviderTemplate(p, map[string]any{}); err == nil {
		t.Fatal("provider render should fail")
	}
	if _, ok := notificationResolveVariablePath(map[string]any{"x": "scalar"}, "x.y"); ok {
		t.Fatal("scalar path")
	}
	if _, ok := notificationResolveVariablePath(map[string]any{}, "x"); ok {
		t.Fatal("missing path")
	}
	if err := notificationRenderBadRequest("x", " ", "ignored"); notificationErrorCode(err) != "x" {
		t.Fatal(err)
	}
	if !strings.Contains(NotificationValueHash("x"), "") {
		t.Fatal("unreachable")
	}
}

func TestNotificationConditionOutcomes(t *testing.T) {
	// Required-value short-circuit outcomes and optional nil handling.
	v := validEmailTemplate()
	v.Variables = []notificationmodel.NotificationTemplateVariable{{Key: "required", Type: "text", Required: true}, {Key: "optional", Type: "text"}}
	for _, value := range []any{nil, " "} {
		if _, err := NotificationValidateRenderVariables(v, map[string]any{"required": value}); err == nil {
			t.Fatalf("required value %#v", value)
		}
	}
	if _, err := NotificationValidateRenderVariables(v, map[string]any{"required": "ok", "optional": nil}); err != nil {
		t.Fatal(err)
	}
	if _, err := NotificationRenderRestricted("broken }}", nil, false); err == nil {
		t.Fatal("closing syntax")
	}
	for _, raw := range []string{"http://example.com", "ftp://example.com"} {
		variable := notificationmodel.NotificationTemplateVariable{Type: "url"}
		err := notificationValidateVariableValue(variable, raw)
		if strings.HasPrefix(raw, "http:") && err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(raw, "ftp:") && err == nil {
			t.Fatal("ftp accepted")
		}
	}

	assertTemplateCode := func(t *testing.T, template notificationmodel.NotificationTemplate, code string) {
		t.Helper()
		if got := notificationErrorCode(NotificationValidateTemplate(template)); got != code {
			t.Fatalf("want %s got %s", code, got)
		}
	}
	for _, fallback := range []notificationmodel.NotificationFallback{
		{TemplateKey: "Bad", ConnectorKey: "connector", ConnectionKey: "conn", Operation: "send"},
		{TemplateKey: "backup", ConnectorKey: "Bad", ConnectionKey: "conn", Operation: "send"},
		{TemplateKey: "backup", ConnectorKey: "connector", ConnectionKey: "", Operation: "send"},
		{TemplateKey: "backup", ConnectorKey: "connector", ConnectionKey: "conn", Operation: ""},
	} {
		template := validEmailTemplate()
		template.Fallbacks = []notificationmodel.NotificationFallback{fallback}
		assertTemplateCode(t, template, "backend.notification.fallback_invalid")
	}
	template := validEmailTemplate()
	content := template.Locales["en"]
	content.Text, content.HTML = "", "<p>x</p>"
	template.Locales["en"] = content
	if err := NotificationValidateTemplate(template); err != nil {
		t.Fatal(err)
	}
	template = validEmailTemplate()
	template.Channel, template.Provider = "collaboration", "slack"
	content = template.Locales["en"]
	content.Subject, content.Text, content.Markdown = "", "", "**x**"
	template.Locales["en"] = content
	if err := NotificationValidateTemplate(template); err != nil {
		t.Fatal(err)
	}
	template = validEmailTemplate()
	content = template.Locales["en"]
	content.Actions = make([]notificationmodel.NotificationTemplateAction, 6)
	template.Locales["en"] = content
	assertTemplateCode(t, template, "backend.notification.template_components_limit")
	template = validEmailTemplate()
	content = template.Locales["en"]
	content.Facts = []notificationmodel.NotificationTemplateFact{{Key: "k", Value: ""}}
	template.Locales["en"] = content
	assertTemplateCode(t, template, "backend.notification.template_fact_invalid")
	for _, action := range []notificationmodel.NotificationTemplateAction{
		{Label: "", URL: "x"}, {Label: "go", URL: ""}, {Label: "go", URL: "x", Style: ""},
		{Label: "go", URL: "x", Style: "secondary"}, {Label: "go", URL: "x", Style: "danger"},
	} {
		template = validEmailTemplate()
		content = template.Locales["en"]
		content.Actions = []notificationmodel.NotificationTemplateAction{action}
		template.Locales["en"] = content
		err := NotificationValidateTemplate(template)
		if action.Label != "" && action.URL != "" && (action.Style == "" || action.Style == "secondary" || action.Style == "danger") {
			if err != nil {
				t.Fatal(err)
			}
		} else if notificationErrorCode(err) != "backend.notification.template_action_invalid" {
			t.Fatalf("action=%#v err=%v", action, err)
		}
	}

	providerCases := []func(*notificationmodel.NotificationProviderTemplate){
		func(p *notificationmodel.NotificationProviderTemplate) { p.Components[0].Index = "1" },
		func(p *notificationmodel.NotificationProviderTemplate) { p.Components[1].Index = "bad" },
		func(p *notificationmodel.NotificationProviderTemplate) {
			p.Components[0].Parameters = make([]string, 11)
		},
	}
	for _, mutate := range providerCases {
		template = validWhatsAppTemplate()
		mutate(template.Locales["en"].ProviderTemplate)
		if err := NotificationValidateTemplate(template); err == nil {
			t.Fatal("invalid provider component accepted")
		}
	}
	template = validWhatsAppTemplate()
	template.Locales["en"].ProviderTemplate.Components[1].SubType = "quick_reply"
	if err := NotificationValidateTemplate(template); err != nil {
		t.Fatalf("quick reply: %v", err)
	}
	template = validWhatsAppTemplate()
	content = template.Locales["en"]
	content.Actions = []notificationmodel.NotificationTemplateAction{{Label: "go", URL: "x"}}
	template.Locales["en"] = content
	assertTemplateCode(t, template, "backend.notification.provider_template_components_conflict")
	template = validEmailTemplate()
	if err := NotificationValidateEditableTemplate(template); err != nil {
		t.Fatal(err)
	}
	if err := validateTemplateTokens("t", "en", "broken }}", map[string]notificationmodel.NotificationTemplateVariable{}); notificationErrorCode(err) != "backend.notification.template_syntax_invalid" {
		t.Fatal(err)
	}
}
