package policy

import (
	"fmt"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func RecordApplyAutoCodeDefaults(object definitionmodel.ObjectSchema, data map[string]any, recordID string) {
	if data == nil {
		return
	}
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == "" || !recordAutoCodeValueEmpty(data[field.Key]) {
			continue
		}
		rule, ok := autoCodeRuleForField(field)
		if !ok {
			continue
		}
		data[field.Key] = generateAutoCodeValue(rule, recordID)
	}
}

type autoCodeRule struct {
	Prefix   string
	TimePart string
	Sequence string
}

func autoCodeRuleForField(field definitionmodel.FieldSchema) (autoCodeRule, bool) {
	config := field.Config
	if config == nil {
		return autoCodeRule{}, false
	}
	raw, ok := config["auto_code"]
	if !ok {
		raw = config["autoCode"]
	}
	rule := autoCodeRule{}
	switch typed := raw.(type) {
	case map[string]any:
		rule.Prefix = firstNonEmptyConfigString(typed, "prefix")
		rule.TimePart = firstNonEmptyConfigString(typed, "time", "time_format", "timeFormat")
		rule.Sequence = firstNonEmptyConfigString(typed, "sequence", "seq")
	case map[string]string:
		rule.Prefix = strings.TrimSpace(typed["prefix"])
		rule.TimePart = firstNonEmptyString(typed["time"], typed["time_format"], typed["timeFormat"])
		rule.Sequence = firstNonEmptyString(typed["sequence"], typed["seq"])
	case bool:
		if !typed {
			return autoCodeRule{}, false
		}
	case string:
		if strings.TrimSpace(typed) == "" || strings.TrimSpace(typed) == "false" {
			return autoCodeRule{}, false
		}
		rule.Prefix = strings.TrimSpace(typed)
	default:
		if raw == nil {
			return autoCodeRule{}, false
		}
	}
	if rule.Prefix == "" {
		rule.Prefix = strings.TrimSpace(fmt.Sprint(config["prefix"]))
	}
	if rule.TimePart == "" {
		rule.TimePart = "20060102"
	}
	return rule, true
}

func generateAutoCodeValue(rule autoCodeRule, recordID string) string {
	now := time.Now().UTC()
	timePart := now.Format(rule.TimePart)
	sequence := strings.TrimSpace(rule.Sequence)
	if sequence == "" {
		sequence = compactAutoCodeSequence(recordID)
	}
	parts := []string{}
	if strings.TrimSpace(rule.Prefix) != "" {
		parts = append(parts, strings.TrimSpace(rule.Prefix))
	}
	if strings.TrimSpace(timePart) != "" {
		parts = append(parts, strings.TrimSpace(timePart))
	}
	parts = append(parts, sequence)
	return strings.Join(parts, "-")
}

func compactAutoCodeSequence(recordID string) string {
	text := strings.TrimSpace(recordID)
	if text == "" {
		return fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	}
	if len(text) <= 8 {
		return text
	}
	return text[len(text)-8:]
}

func firstNonEmptyConfigString(config map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(fmt.Sprint(config[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}

func recordAutoCodeValueEmpty(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}
