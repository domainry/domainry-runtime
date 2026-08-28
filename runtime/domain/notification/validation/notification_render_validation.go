package validation

import (
	"fmt"
	"html"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

var notificationRenderTokenPattern = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_.-]*)\s*\}\}`)

func NotificationValidateRenderVariables(template notificationmodel.NotificationTemplate, values map[string]any) (map[string]any, error) {
	result := make(map[string]any, len(template.Variables))
	allowed := make(map[string]bool, len(template.Variables))
	for _, variable := range template.Variables {
		allowed[strings.TrimSpace(variable.Key)] = true
	}
	for key := range values {
		if !allowed[strings.TrimSpace(key)] {
			return nil, notificationRenderBadRequest("backend.notification.template_variable_unknown", "template_key", template.Key, "variable", key)
		}
	}
	for _, variable := range template.Variables {
		value, exists := values[variable.Key]
		if variable.Required && (!exists || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "") {
			return nil, notificationRenderBadRequest("backend.notification.template_variable_required", "template_key", template.Key, "variable", variable.Key)
		}
		if !exists || value == nil {
			continue
		}
		if err := notificationValidateVariableValue(variable, value); err != nil {
			return nil, notificationRenderBadRequest("backend.notification.template_variable_invalid", "template_key", template.Key, "variable", variable.Key, "type", variable.Type)
		}
		result[variable.Key] = value
	}
	return result, nil
}

func NotificationRenderRestricted(source string, values map[string]any, escapeHTML bool) (string, error) {
	if source == "" {
		return "", nil
	}
	var renderErr error
	rendered := notificationRenderTokenPattern.ReplaceAllStringFunc(source, func(token string) string {
		match := notificationRenderTokenPattern.FindStringSubmatch(token)
		value, ok := notificationResolveVariablePath(values, match[1])
		if !ok {
			renderErr = fmt.Errorf("missing variable %s", match[1])
			return ""
		}
		text := fmt.Sprint(value)
		if escapeHTML {
			return html.EscapeString(text)
		}
		return text
	})
	if renderErr != nil || strings.Contains(rendered, "{{") || strings.Contains(rendered, "}}") {
		return "", fmt.Errorf("template render failed")
	}
	return rendered, nil
}

func notificationRenderBadRequest(code string, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: code, Params: values}
}

func NotificationRenderProviderTemplate(template *notificationmodel.NotificationProviderTemplate, variables map[string]any) (*notificationmodel.NotificationProviderTemplate, error) {
	if template == nil {
		return nil, nil
	}
	rendered := &notificationmodel.NotificationProviderTemplate{Name: template.Name, Language: template.Language, Components: make([]notificationmodel.NotificationProviderTemplateComponent, 0, len(template.Components))}
	for _, component := range template.Components {
		next := notificationmodel.NotificationProviderTemplateComponent{Type: component.Type, SubType: component.SubType, Index: component.Index, Parameters: make([]string, 0, len(component.Parameters))}
		for _, parameter := range component.Parameters {
			value, err := NotificationRenderRestricted(parameter, variables, false)
			if err != nil {
				return nil, err
			}
			next.Parameters = append(next.Parameters, value)
		}
		rendered.Components = append(rendered.Components, next)
	}
	return rendered, nil
}

func notificationValidateVariableValue(variable notificationmodel.NotificationTemplateVariable, value any) error {
	text := strings.TrimSpace(fmt.Sprint(value))
	switch variable.Type {
	case "boolean":
		_, err := strconv.ParseBool(text)
		return err
	case "number":
		_, err := strconv.ParseFloat(text, 64)
		return err
	case "date":
		_, err := time.Parse("2006-01-02", text)
		return err
	case "datetime":
		_, err := time.Parse(time.RFC3339, text)
		return err
	case "email":
		_, err := mail.ParseAddress(text)
		return err
	case "url":
		parsed, err := url.ParseRequestURI(text)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("invalid url")
		}
	}
	return nil
}

func notificationResolveVariablePath(values map[string]any, path string) (any, bool) {
	var current any = values
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}
