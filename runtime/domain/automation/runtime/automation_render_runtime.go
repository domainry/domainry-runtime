package runtime

import (
	"errors"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func AutomationRenderStepData(step map[string]any, ctx *automationmodel.AutomationRenderContext) (map[string]any, error) {
	if _, ok := step["data"]; ok {
		return AutomationRenderData(step["data"], ctx)
	}
	if _, ok := step["value"]; ok {
		return map[string]any{"value": AutomationRenderObjectValue(step["value"], ctx)}, nil
	}
	return AutomationRenderData(step, ctx)
}

func AutomationMergeTargetPayload(step, data map[string]any, ctx *automationmodel.AutomationRenderContext) error {
	targetPayloadValue, hasTargetPayload := AutomationFirstPresent(step, "target_payload", "payload_patch", "merge_payload")
	targetField := AutomationRenderString(AutomationFirstNonNil(step["target_field"], step["field_key"], step["payload_field"]), ctx)
	if !hasTargetPayload && targetField == "" && !AutomationRuntimeBool(step["writes_target_payload"]) {
		return nil
	}
	if ctx.Payload == nil {
		ctx.Payload = map[string]any{}
	}
	if hasTargetPayload {
		patch, err := AutomationRenderData(targetPayloadValue, ctx)
		if err != nil {
			return err
		}
		for key, value := range patch {
			if strings.TrimSpace(key) != "" {
				ctx.Payload[key] = value
			}
		}
		return nil
	}
	if targetField != "" {
		value := any(data)
		if raw, ok := data["value"]; ok && len(data) == 1 {
			value = raw
		}
		ctx.Payload[targetField] = value
		return nil
	}
	for key, value := range data {
		if strings.TrimSpace(key) != "" {
			ctx.Payload[key] = value
		}
	}
	return nil
}

func AutomationRenderData(value any, ctx *automationmodel.AutomationRenderContext) (map[string]any, error) {
	rendered := AutomationRenderObjectValue(value, ctx)
	if typed, ok := rendered.(map[string]any); ok {
		return typed, nil
	}
	return nil, AutomationRuntimeError("backend.action.data_object_required")
}

func AutomationRenderOptionalData(value any, ctx *automationmodel.AutomationRenderContext) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	return AutomationRenderData(value, ctx)
}

func AutomationRenderObjectValue(value any, ctx *automationmodel.AutomationRenderContext) any {
	switch typed := value.(type) {
	case string:
		return renderStringOrValue(typed, ctx)
	case map[string]any:
		out := map[string]any{}
		for key, item := range typed {
			out[key] = AutomationRenderObjectValue(item, ctx)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, AutomationRenderObjectValue(item, ctx))
		}
		return out
	default:
		return value
	}
}

func AutomationRenderString(value any, ctx *automationmodel.AutomationRenderContext) string {
	rendered := AutomationRenderObjectValue(value, ctx)
	if rendered == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(rendered))
}

func renderStringOrValue(value string, ctx *automationmodel.AutomationRenderContext) any {
	value = strings.TrimSpace(value)
	if value == "" || ctx == nil {
		return value
	}
	for prefix, source := range map[string]map[string]any{
		"$payload.": ctx.Payload, "$candidate.": ctx.Payload, "$input.": ctx.Input,
		"$before.": ctx.Before, "$actor.": ctx.Actor, "$event.": ctx.Event, "$record.": ctx.Record,
	} {
		if strings.HasPrefix(value, prefix) {
			return nestedValue(source, strings.TrimPrefix(value, prefix))
		}
	}
	for _, prefix := range []string{"$steps.", "$actions."} {
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(value, prefix), ".", 2)
		step, ok := ctx.Results[parts[0]]
		if !ok {
			return ""
		}
		if len(parts) == 1 {
			return step.Data
		}
		if prefix == "$steps." {
			switch parts[1] {
			case "record_id":
				return step.RecordID
			case "object_key":
				return step.ObjectKey
			}
		}
		return nestedValue(step.Data, parts[1])
	}
	return value
}

func AutomationCalculateNumber(step map[string]any, ctx *automationmodel.AutomationRenderContext) (map[string]any, error) {
	left, err := renderedNumber(AutomationFirstNonNil(step["left"], step["source"]), ctx)
	if err != nil {
		return nil, err
	}
	right, err := renderedNumber(AutomationFirstNonNil(step["right"], step["value"]), ctx)
	if err != nil {
		return nil, err
	}
	operator := strings.TrimSpace(fmt.Sprint(step["operator"]))
	value := left
	switch operator {
	case "add", "+":
		value = left + right
	case "subtract", "-":
		value = left - right
	case "multiply", "*":
		value = left * right
	case "divide", "/":
		if right == 0 {
			return nil, AutomationRuntimeError("backend.action.calculate_divide_by_zero")
		}
		value = left / right
	case "ceil_to_multiple":
		if right <= 0 {
			return nil, AutomationRuntimeError("backend.action.calculate_multiple_invalid")
		}
		value = math.Ceil(left/right) * right
	default:
		return nil, AutomationRuntimeError("backend.action.calculate_operator_invalid", "operator", operator)
	}
	return map[string]any{"value": value}, nil
}

func AutomationCalculateDecimal(step map[string]any, ctx *automationmodel.AutomationRenderContext) (map[string]any, error) {
	config, err := recordmodel.RecordNormalizeDecimalConfig(map[string]any{
		"precision":     AutomationFirstNonNil(step["precision"], recordmodel.RecordDecimalDefaultPrecision),
		"scale":         AutomationFirstNonNil(step["scale"], recordmodel.RecordDecimalDefaultScale),
		"rounding_mode": AutomationFirstNonNil(step["rounding_mode"], recordmodel.RecordDecimalDefaultRoundingMode),
		"currency_code": AutomationFirstNonNil(step["currency_code"], recordmodel.RecordDecimalDefaultCurrencyCode),
	})
	if err != nil {
		return nil, actionDecimalRuntimeError(err)
	}
	left := fmt.Sprint(AutomationRenderObjectValue(AutomationFirstNonNil(step["left"], step["source"]), ctx))
	right := fmt.Sprint(AutomationRenderObjectValue(AutomationFirstNonNil(step["right"], step["value"]), ctx))
	operator := strings.TrimSpace(fmt.Sprint(step["operator"]))
	var result string
	switch operator {
	case "add", "+":
		result, err = recordmodel.RecordAddDecimals(left, right, config)
	case "subtract", "-":
		result, err = recordmodel.RecordSubtractDecimals(left, right, config)
	case "multiply", "*":
		result, err = recordmodel.RecordMultiplyDecimals(left, right, config)
	case "divide", "/":
		result, err = recordmodel.RecordDivideDecimals(left, right, config)
	case "percentage", "percent":
		result, err = recordmodel.RecordPercentageDecimal(left, right, config)
	default:
		return nil, AutomationRuntimeError("backend.decimal.operator_invalid", "operator", operator)
	}
	if err != nil {
		return nil, actionDecimalRuntimeError(err)
	}
	return map[string]any{"value": result, "currency_code": config.CurrencyCode, "scale": config.Scale}, nil
}

func actionDecimalRuntimeError(err error) error {
	if decimalError, ok := err.(*recordmodel.RecordDecimalError); ok {
		return AutomationRuntimeError(decimalError.Code)
	}
	return AutomationRuntimeError("backend.decimal.value_invalid")
}

func AutomationCompareNumbers(actual, expected any, operator string) (bool, error) {
	left, err := parseNumber(actual)
	if err != nil {
		return false, err
	}
	right, err := parseNumber(expected)
	if err != nil {
		return false, err
	}
	switch operator {
	case "gt":
		return left > right, nil
	case "gte":
		return left >= right, nil
	case "lt":
		return left < right, nil
	case "lte":
		return left <= right, nil
	default:
		return false, AutomationRuntimeError("backend.action.assert_operator_unsupported", "operator", operator)
	}
}

func AutomationAssert(step map[string]any, ctx *automationmodel.AutomationRenderContext) error {
	actual := AutomationRenderObjectValue(step["source"], ctx)
	expected := AutomationRenderObjectValue(step["value"], ctx)
	operator := AutomationCleanRuntimeValue(step["operator"])
	if operator == "" {
		operator = "eq"
	}
	matched := false
	switch operator {
	case "eq":
		matched = fmt.Sprint(actual) == fmt.Sprint(expected)
	case "ne":
		matched = fmt.Sprint(actual) != fmt.Sprint(expected)
	case "in":
		for _, candidate := range AutomationRuntimeStringList(expected) {
			if fmt.Sprint(actual) == candidate {
				matched = true
				break
			}
		}
	case "future_date":
		value, err := time.Parse("2006-01-02", strings.TrimSpace(fmt.Sprint(actual)))
		if err != nil {
			return AutomationRuntimeError("backend.action.assert_date_invalid")
		}
		matched = value.After(time.Now().UTC().Truncate(24 * time.Hour))
	case "not_empty":
		matched = !actionRuntimeIsEmptyValue(actual)
	case "empty":
		matched = actionRuntimeIsEmptyValue(actual)
	case "gt", "gte", "lt", "lte":
		var err error
		matched, err = AutomationCompareNumbers(actual, expected, operator)
		if err != nil {
			return err
		}
	default:
		return AutomationRuntimeError("backend.action.assert_operator_unsupported", "operator", operator)
	}
	if matched {
		return nil
	}
	code := AutomationCleanRuntimeValue(step["error_code"])
	if code == "" {
		code = "backend.action.assert_failed"
	}
	return AutomationRuntimeError(code)
}

func AutomationFirstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func AutomationFirstPresent(values map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		value, ok := values[key]
		if ok {
			return value, true
		}
	}
	return nil, false
}

func renderedNumber(raw any, ctx *automationmodel.AutomationRenderContext) (float64, error) {
	return parseNumber(AutomationRenderObjectValue(raw, ctx))
}

func parseNumber(value any) (float64, error) {
	number, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(value)), 64)
	if err != nil {
		return 0, AutomationRuntimeError("backend.action.calculate_number_invalid", "value", fmt.Sprint(value))
	}
	return number, nil
}

func nestedValue(data map[string]any, path string) any {
	if data == nil || strings.TrimSpace(path) == "" {
		return ""
	}
	var current any = data
	for _, part := range strings.Split(path, ".") {
		mapped, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current, ok = mapped[part]
		if !ok {
			return ""
		}
	}
	return current
}

func AutomationCleanRuntimeValue(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func AutomationRuntimeBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, _ := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed
	default:
		return false
	}
}

func actionRuntimeIsEmptyValue(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}

func AutomationRuntimeStringList(value any) []string {
	result := []string{}
	switch typed := value.(type) {
	case []string:
		result = append(result, typed...)
	case []any:
		for _, item := range typed {
			result = append(result, fmt.Sprint(item))
		}
	case string:
		result = strings.Split(typed, ",")
	}
	out, seen := []string{}, map[string]bool{}
	for _, item := range result {
		item = strings.TrimSpace(item)
		if item != "" && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

func AutomationRuntimeError(code string, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		values[params[index]] = params[index+1]
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: code, Params: values}
}

func AutomationRuntimeErrorCode(err error) string {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) && strings.TrimSpace(appErr.ErrorCode()) != "" {
		return appErr.ErrorCode()
	}
	return "backend.internal"
}
