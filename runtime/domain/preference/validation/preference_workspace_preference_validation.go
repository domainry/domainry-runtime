package validation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	preferencemodel "github.com/domainry/domainry-runtime/runtime/domain/preference/model"
)

var workspacePreferenceDecimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

func DecodeWorkspacePreferenceDefinition(resourceKey string, payload json.RawMessage) (preferencemodel.WorkspacePreferenceDefinition, error) {
	var definition preferencemodel.WorkspacePreferenceDefinition
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&definition); err != nil {
		return definition, workspacePreferenceValidationError("backend.preference.definition_invalid", resourceKey, err)
	}
	if err := requirePreferenceJSONEOF(decoder); err != nil {
		return definition, workspacePreferenceValidationError("backend.preference.definition_invalid", resourceKey, err)
	}
	definition.Key = strings.TrimSpace(definition.Key)
	definition.Name = strings.TrimSpace(definition.Name)
	definition.ValueType = strings.TrimSpace(definition.ValueType)
	definition.EffectiveFrom = strings.TrimSpace(definition.EffectiveFrom)
	definition.EffectiveTo = strings.TrimSpace(definition.EffectiveTo)
	if definition.Key == "" || definition.Name == "" || definition.ValueType == "" || len(definition.Value) == 0 || definition.EffectiveFrom == "" {
		return definition, workspacePreferenceValidationError("backend.preference.definition_invalid", resourceKey, nil)
	}
	if resourceKey = strings.TrimSpace(resourceKey); resourceKey != "" && definition.Key != resourceKey {
		return definition, workspacePreferenceValidationError("backend.preference.definition_invalid", resourceKey, nil)
	}
	from, err := preferencemodel.ParsePreferenceEffectiveTime(definition.EffectiveFrom)
	if err != nil {
		return definition, workspacePreferenceValidationError("backend.preference.effective_from_invalid", definition.Key, err)
	}
	if definition.EffectiveTo != "" {
		to, parseErr := preferencemodel.ParsePreferenceEffectiveTime(definition.EffectiveTo)
		if parseErr != nil || !to.After(from) {
			return definition, workspacePreferenceValidationError("backend.preference.effective_to_invalid", definition.Key, parseErr)
		}
	}
	if err := validateWorkspacePreferenceValue(definition.Key, definition.ValueType, definition.Value); err != nil {
		return definition, err
	}
	return definition, nil
}

func validateWorkspacePreferenceValue(preferenceKey, valueType string, raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return workspacePreferenceValidationError("backend.preference.value_invalid", preferenceKey, nil)
	}
	if err := requirePreferenceJSONEOF(decoder); err != nil {
		return workspacePreferenceValidationError("backend.preference.value_invalid", preferenceKey, err)
	}
	switch valueType {
	case preferencemodel.PreferenceValueBoolean:
		if _, ok := value.(bool); !ok {
			return workspacePreferenceValidationError("backend.preference.value_type_mismatch", preferenceKey, nil)
		}
	case preferencemodel.PreferenceValueInteger:
		number, ok := value.(json.Number)
		if !ok || strings.ContainsAny(number.String(), ".eE") {
			return workspacePreferenceValidationError("backend.preference.value_type_mismatch", preferenceKey, nil)
		}
		if _, err := number.Int64(); err != nil {
			return workspacePreferenceValidationError("backend.preference.value_type_mismatch", preferenceKey, err)
		}
	case preferencemodel.PreferenceValueDecimal:
		text, ok := value.(string)
		if !ok || !workspacePreferenceDecimalPattern.MatchString(strings.TrimSpace(text)) {
			return workspacePreferenceValidationError("backend.preference.value_type_mismatch", preferenceKey, nil)
		}
	case preferencemodel.PreferenceValueNumber:
		number, ok := value.(json.Number)
		if !ok {
			return workspacePreferenceValidationError("backend.preference.value_type_mismatch", preferenceKey, nil)
		}
		if _, err := strconv.ParseFloat(number.String(), 64); err != nil {
			return workspacePreferenceValidationError("backend.preference.value_type_mismatch", preferenceKey, err)
		}
	case preferencemodel.PreferenceValueText:
		if _, ok := value.(string); !ok {
			return workspacePreferenceValidationError("backend.preference.value_type_mismatch", preferenceKey, nil)
		}
	case preferencemodel.PreferenceValueDate:
		text, ok := value.(string)
		if !ok || len(text) != len("2006-01-02") {
			return workspacePreferenceValidationError("backend.preference.value_type_mismatch", preferenceKey, nil)
		}
		if _, err := preferencemodel.ParsePreferenceEffectiveTime(text); err != nil {
			return workspacePreferenceValidationError("backend.preference.value_type_mismatch", preferenceKey, err)
		}
	case preferencemodel.PreferenceValueDateTime:
		text, ok := value.(string)
		if !ok || len(text) == len("2006-01-02") {
			return workspacePreferenceValidationError("backend.preference.value_type_mismatch", preferenceKey, nil)
		}
		if _, err := preferencemodel.ParsePreferenceEffectiveTime(text); err != nil {
			return workspacePreferenceValidationError("backend.preference.value_type_mismatch", preferenceKey, err)
		}
	case preferencemodel.PreferenceValueJSON:
		return nil
	default:
		return workspacePreferenceValidationError("backend.preference.value_type_invalid", preferenceKey, nil)
	}
	return nil
}

func workspacePreferenceValidationError(code, preferenceKey string, cause error) error {
	return &preferencemodel.WorkspacePreferenceError{Code: code, PreferenceKey: strings.TrimSpace(preferenceKey), Cause: cause}
}

func requirePreferenceJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}
