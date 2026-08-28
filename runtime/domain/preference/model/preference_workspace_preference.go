package preferencemodel

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	PreferenceValueBoolean  = "boolean"
	PreferenceValueDate     = "date"
	PreferenceValueDateTime = "datetime"
	PreferenceValueDecimal  = "decimal"
	PreferenceValueInteger  = "integer"
	PreferenceValueJSON     = "json"
	PreferenceValueNumber   = "number"
	PreferenceValueText     = "text"
)

type WorkspacePreferenceDefinition struct {
	Key           string          `json:"key"`
	Name          string          `json:"name"`
	ValueType     string          `json:"value_type"`
	Value         json.RawMessage `json:"value"`
	EffectiveFrom string          `json:"effective_from"`
	EffectiveTo   string          `json:"effective_to,omitempty"`
}

type WorkspacePreferenceVersion struct {
	WorkspaceID  string
	Definition   WorkspacePreferenceDefinition
	Version      string
	ResourceHash string
}

type WorkspacePreferenceResolution struct {
	WorkspaceID   string `json:"workspace_id"`
	PreferenceKey string `json:"preference_key"`
	ValueType     string `json:"value_type"`
	Value         any    `json:"value"`
	Version       string `json:"version"`
	ResourceHash  string `json:"resource_hash"`
	EffectiveAt   string `json:"effective_at"`
	EffectiveFrom string `json:"effective_from"`
	EffectiveTo   string `json:"effective_to,omitempty"`
}

type WorkspacePreferenceError struct {
	Code          string
	PreferenceKey string
	Cause         error
}

func (e *WorkspacePreferenceError) Error() string { return e.Code }
func (e *WorkspacePreferenceError) Unwrap() error { return e.Cause }
func (e *WorkspacePreferenceError) ErrorCode() string {
	return e.Code
}
func (e *WorkspacePreferenceError) ErrorParams() map[string]string {
	return map[string]string{"preference_key": e.PreferenceKey}
}

func ParsePreferenceEffectiveTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if len(value) == len("2006-01-02") {
		if parsed, err := time.Parse("2006-01-02", value); err == nil {
			return parsed.UTC(), nil
		}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}
