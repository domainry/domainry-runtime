package notificationmodel

// NotificationTemplate is the published, immutable message contract installed
// with a Runtime manifest. Version and ContentHash pin the exact content used
// to produce retry-stable outbox snapshots.
type NotificationTemplate struct {
	Key           string                                 `json:"key"`
	Name          string                                 `json:"name"`
	Channel       string                                 `json:"channel"`
	Provider      string                                 `json:"provider,omitempty"`
	Status        string                                 `json:"status"`
	Version       int                                    `json:"version"`
	DefaultLocale string                                 `json:"default_locale"`
	Variables     []NotificationTemplateVariable         `json:"variables,omitempty"`
	Fallbacks     []NotificationFallback                 `json:"fallbacks,omitempty"`
	Locales       map[string]NotificationTemplateContent `json:"locales"`
	ContentHash   string                                 `json:"content_hash,omitempty"`
}

type NotificationFallback struct {
	TemplateKey   string `json:"template_key"`
	ConnectorKey  string `json:"connector_key"`
	ConnectionKey string `json:"connection_key"`
	Operation     string `json:"operation"`
}

type NotificationTemplateVariable struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
}

type NotificationTemplateContent struct {
	Subject          string                        `json:"subject"`
	Title            string                        `json:"title,omitempty"`
	Text             string                        `json:"text,omitempty"`
	HTML             string                        `json:"html,omitempty"`
	Markdown         string                        `json:"markdown,omitempty"`
	Facts            []NotificationTemplateFact    `json:"facts,omitempty"`
	Actions          []NotificationTemplateAction  `json:"actions,omitempty"`
	ProviderTemplate *NotificationProviderTemplate `json:"provider_template,omitempty"`
}

// NotificationProviderTemplate binds a Runtime notification to a
// provider-approved template. Components keep parameter order explicit so the
// immutable outbox payload matches the approved provider contract.
type NotificationProviderTemplate struct {
	Name       string                                  `json:"name"`
	Language   string                                  `json:"language"`
	Components []NotificationProviderTemplateComponent `json:"components,omitempty"`
}

type NotificationProviderTemplateComponent struct {
	Type       string   `json:"type"`
	SubType    string   `json:"sub_type,omitempty"`
	Index      string   `json:"index,omitempty"`
	Parameters []string `json:"parameters,omitempty"`
}

// NotificationTemplateFact is a rendered key/value row supported by native
// cards and rich-message attachments.
type NotificationTemplateFact struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// NotificationTemplateAction is a portable URL action. Provider compilers
// map its semantic style into the closest native button representation.
type NotificationTemplateAction struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Style string `json:"style,omitempty"`
}

type RenderedNotification struct {
	Channel             string                        `json:"channel"`
	Provider            string                        `json:"provider,omitempty"`
	Recipients          []string                      `json:"recipients"`
	Subject             string                        `json:"subject"`
	Title               string                        `json:"title,omitempty"`
	Text                string                        `json:"text,omitempty"`
	HTML                string                        `json:"html,omitempty"`
	Markdown            string                        `json:"markdown,omitempty"`
	Message             string                        `json:"message,omitempty"`
	Facts               []NotificationTemplateFact    `json:"facts,omitempty"`
	Actions             []NotificationTemplateAction  `json:"actions,omitempty"`
	ProviderTemplate    *NotificationProviderTemplate `json:"provider_template,omitempty"`
	ProviderPayload     map[string]any                `json:"provider_payload,omitempty"`
	TemplateKey         string                        `json:"template_key"`
	TemplateVersion     int                           `json:"template_version"`
	TemplateLocale      string                        `json:"template_locale"`
	TemplateContentHash string                        `json:"template_content_hash"`
	VariablesHash       string                        `json:"variables_hash"`
	Metadata            map[string]any                `json:"metadata,omitempty"`
	Fallbacks           []NotificationFallback        `json:"fallbacks,omitempty"`
}
