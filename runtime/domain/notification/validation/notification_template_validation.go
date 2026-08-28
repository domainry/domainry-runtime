package validation

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	sourcetemplate "github.com/domainry/domainry-notification-sdk/contract"
	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

var stableTemplateKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)
var templateTokenPattern = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_.-]*)\s*\}\}`)

func notificationTemplateValidator() (*sourcetemplate.NotificationTemplateValidator, error) {
	capabilities, err := notificationcontract.NotificationTemplateCapabilityCatalog()
	if err != nil {
		return nil, err
	}
	return sourcetemplate.NewNotificationTemplateValidator(capabilities)
}

func NotificationValidateTemplates(values []notificationmodel.NotificationTemplate) error {
	validator, err := notificationTemplateValidator()
	if err != nil {
		return err
	}
	templates := make([]sourcetemplate.NotificationTemplate, len(values))
	for i, value := range values {
		templates[i] = notificationcontract.ModuleTemplate(value)
	}
	return mapNotificationTemplateValidationError(validator.ValidateAll(templates))
}

func NotificationValidateTemplate(value notificationmodel.NotificationTemplate) error {
	validator, err := notificationTemplateValidator()
	if err != nil {
		return err
	}
	return mapNotificationTemplateValidationError(validator.Validate(notificationcontract.ModuleTemplate(value)))
}

func NotificationValidateEditableTemplate(value notificationmodel.NotificationTemplate) error {
	validator, err := notificationTemplateValidator()
	if err != nil {
		return err
	}
	return mapNotificationTemplateValidationError(validator.ValidateEditable(notificationcontract.ModuleTemplate(value)))
}

func NotificationTemplateContentHash(value notificationmodel.NotificationTemplate) string {
	return sourcetemplate.NotificationTemplateContentHash(notificationcontract.ModuleTemplate(value))
}
func NotificationValueHash(value any) string { return sourcetemplate.NotificationValueHash(value) }

func validateTemplateTokens(templateKey, locale, source string, variables map[string]notificationmodel.NotificationTemplateVariable) error {
	matches := templateTokenPattern.FindAllStringSubmatch(source, -1)
	consumed := templateTokenPattern.ReplaceAllString(source, "")
	if strings.Contains(consumed, "{{") || strings.Contains(consumed, "}}") {
		return notificationBadRequest("backend.notification.template_syntax_invalid", "template_key", templateKey, "locale", locale)
	}
	for _, match := range matches {
		root := strings.Split(match[1], ".")[0]
		if variables[root].Key == "" {
			return notificationBadRequest("backend.notification.template_variable_unknown", "template_key", templateKey, "locale", locale, "variable", root)
		}
	}
	return nil
}

func mapNotificationTemplateValidationError(err error) error {
	if err == nil {
		return nil
	}
	var source *sourcetemplate.TemplateValidationError
	if !errors.As(err, &source) {
		return err
	}
	params := map[string]string{}
	for key, value := range source.Params {
		params[key] = fmt.Sprint(value)
	}
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: source.Code, Params: params, Err: err}
}

func notificationBadRequest(code string, params ...string) error {
	values := map[string]string{}
	for i := 0; i+1 < len(params); i += 2 {
		if key := strings.TrimSpace(params[i]); key != "" {
			values[key] = params[i+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: code, Params: values}
}
