package policy

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"fmt"
	"strconv"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func ActionCheckPreconditions(action definitionmodel.ActionSchema, data map[string]any) error {
	for _, precondition := range action.Preconditions {
		text := strings.TrimSpace(strings.ToLower(precondition))
		switch {
		case strings.Contains(text, " not in "):
			parts := strings.SplitN(text, " not in ", 2)
			value := strings.ToLower(strings.TrimSpace(fmt.Sprint(data[strings.TrimSpace(parts[0])])))
			for _, candidate := range strings.Split(parts[1], ",") {
				if value == strings.TrimSpace(candidate) {
					return ActionPreconditionFailedError()
				}
			}
		case strings.Contains(text, " in "):
			parts := strings.SplitN(text, " in ", 2)
			value := strings.ToLower(strings.TrimSpace(fmt.Sprint(data[strings.TrimSpace(parts[0])])))
			matched := false
			for _, candidate := range strings.Split(parts[1], ",") {
				matched = matched || value == strings.TrimSpace(candidate)
			}
			if !matched {
				return ActionPreconditionFailedError()
			}
		case strings.HasSuffix(text, " is present"):
			if field := strings.TrimSpace(strings.TrimSuffix(text, " is present")); actionIsEmptyValue(data[field]) {
				return ActionPreconditionFailedError()
			}
		case strings.Contains(text, " > 0"):
			field := strings.TrimSpace(strings.TrimSuffix(text, " > 0"))
			if number, ok := ActionNumericValue(data[field]); !ok || number <= 0 {
				return ActionPreconditionFailedError()
			}
		case strings.HasSuffix(text, " <= today"):
			field := strings.TrimSpace(strings.TrimSuffix(text, " <= today"))
			value, ok := actionDateOnly(data[field])
			if !ok || value.After(time.Now().UTC().Truncate(24*time.Hour)) {
				return ActionPreconditionFailedError()
			}
		case strings.HasSuffix(text, " < today"):
			field := strings.TrimSpace(strings.TrimSuffix(text, " < today"))
			value, ok := actionDateOnly(data[field])
			if !ok || !value.Before(time.Now().UTC().Truncate(24*time.Hour)) {
				return ActionPreconditionFailedError()
			}
		case strings.Contains(text, " >= "), strings.Contains(text, " <= "), strings.Contains(text, " > "), strings.Contains(text, " < "):
			operator := ""
			for _, candidate := range []string{" >= ", " <= ", " > ", " < "} {
				if strings.Contains(text, candidate) {
					operator = candidate
					break
				}
			}
			parts := strings.SplitN(text, operator, 2)
			if !comparePrecondition(data, strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(operator)) {
				return ActionPreconditionFailedError()
			}
		case strings.Contains(text, " == "):
			parts := strings.SplitN(text, " == ", 2)
			left := strings.TrimSpace(fmt.Sprint(data[fieldKeyFromLabel(parts[0])]))
			if !strings.EqualFold(left, strings.TrimSpace(parts[1])) {
				return ActionPreconditionFailedError()
			}
		case strings.Contains(text, " != "):
			parts := strings.SplitN(text, " != ", 2)
			left := strings.TrimSpace(fmt.Sprint(data[fieldKeyFromLabel(parts[0])]))
			right := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
			if left != "" && right != "" && left == right {
				return ActionPreconditionFailedError()
			}
		}
	}
	return nil
}

func comparePrecondition(data map[string]any, leftField, rightOperand, operator string) bool {
	if rightOperand == "today" {
		left, ok := actionDateOnly(data[leftField])
		if !ok {
			return false
		}
		return compareNumbers(float64(left.Unix()), float64(time.Now().UTC().Truncate(24*time.Hour).Unix()), operator)
	}
	left, ok := ActionNumericValue(data[leftField])
	if !ok {
		return false
	}
	right, rightOK := ActionNumericValue(data[rightOperand])
	if !rightOK {
		var err error
		right, err = strconv.ParseFloat(rightOperand, 64)
		if err != nil {
			return false
		}
	}
	return compareNumbers(left, right, operator)
}

func compareNumbers(left, right float64, operator string) bool {
	switch operator {
	case ">=":
		return left >= right
	case "<=":
		return left <= right
	case ">":
		return left > right
	case "<":
		return left < right
	default:
		return false
	}
}

func ActionNumericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		var out float64
		_, err := fmt.Sscan(typed, &out)
		return out, err == nil
	default:
		return 0, false
	}
}

func fieldKeyFromLabel(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), " ", "_")
}

func ActionNextSelectOption(object definitionmodel.ObjectSchema, fieldKey, current string) (string, bool) {
	for _, field := range object.Fields {
		if field.Key != fieldKey {
			continue
		}
		options := ActionStringList(field.Config["options"])
		for index, option := range options {
			if option != current || index+1 >= len(options) {
				continue
			}
			if next := options[index+1]; next != "lost" {
				return next, true
			}
			return "", false
		}
		if len(options) > 0 && strings.TrimSpace(current) == "" {
			return options[0], true
		}
	}
	return "", false
}

func ActionValidateMakerChecker(action definitionmodel.ActionSchema, object definitionmodel.ObjectSchema, record recordmodel.Record, principal principalmodel.Principal) error {
	if !makerCheckerEnabled(action) {
		return nil
	}
	actor := strings.TrimSpace(principal.UserID)
	if actor == "" {
		return nil
	}
	for _, field := range makerFieldCandidates(action, object) {
		maker := ActionNormalizedValue(record.Data[field])
		if maker == "" {
			continue
		}
		if maker == actor {
			return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.action.maker_checker_denied"}
		}
		return nil
	}
	return nil
}

func ActionUniqueNonEmptyStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func ActionCloneData(data map[string]any) map[string]any {
	out := make(map[string]any, len(data))
	for key, value := range data {
		out[key] = value
	}
	return out
}

func makerCheckerEnabled(action definitionmodel.ActionSchema) bool {
	if action.AssurancePolicy == nil {
		return false
	}
	for _, method := range action.AssurancePolicy.RequiredMethods {
		if strings.TrimSpace(method) == definitionmodel.ActionAssuranceMakerChecker {
			return true
		}
	}
	return false
}

func makerFieldCandidates(action definitionmodel.ActionSchema, object definitionmodel.ObjectSchema) []string {
	candidates := []string{}
	if action.AssurancePolicy != nil {
		if field := strings.TrimSpace(action.AssurancePolicy.MakerField); field != "" {
			candidates = append(candidates, field)
		}
	}
	candidates = append(candidates, "submitted_by", "created_by")
	if ownerField := actionOwnerFieldKey(object); ownerField != "" {
		candidates = append(candidates, ownerField)
	}
	return candidates
}

func ActionBoolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		text := strings.ToLower(strings.TrimSpace(typed))
		if text == "true" || text == "1" || text == "yes" {
			return true, true
		}
		if text == "false" || text == "0" || text == "no" {
			return false, true
		}
	}
	return false, false
}

func ActionPreconditionFailedError() error {
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.action.precondition_failed"}
}

func actionIsEmptyValue(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}

func actionDateOnly(value any) (time.Time, bool) {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return time.Time{}, false
	}
	if len(text) >= 10 {
		text = text[:10]
	}
	parsed, err := time.Parse("2006-01-02", text)
	return parsed, err == nil
}

func actionOwnerFieldKey(object definitionmodel.ObjectSchema) string {
	if strings.TrimSpace(fmt.Sprint(object.UX["kind"])) == "identity_profile_extension" {
		if config, ok := object.UX["config"].(map[string]any); ok {
			key := strings.TrimSpace(fmt.Sprint(config["identity_relation_field"]))
			for _, field := range object.Fields {
				if field.Key == key && field.Type == "relation" {
					return key
				}
			}
		}
	}
	for _, preferred := range []string{"owner", "assignee", "requester", "created_by", "createdBy"} {
		for _, field := range object.Fields {
			if field.Key == preferred && field.Type == "user" {
				return field.Key
			}
		}
	}
	for _, field := range object.Fields {
		if field.Type == "user" {
			return field.Key
		}
	}
	return ""
}
