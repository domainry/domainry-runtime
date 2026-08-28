package rulesetmodel

import (
	"strings"
	"time"

	expressionmodel "github.com/domainry/domainry-foundation/expression/model"
)

const RuleSetMatchFirst = "first_match"

type RuleSetDefinition struct {
	Key            string                                        `json:"key"`
	Name           string                                        `json:"name"`
	MatchPolicy    string                                        `json:"match_policy"`
	InputTypes     map[string]string                             `json:"input_types"`
	OutputTypes    map[string]string                             `json:"output_types"`
	EffectiveFrom  string                                        `json:"effective_from"`
	EffectiveTo    string                                        `json:"effective_to,omitempty"`
	Rules          []RuleSetRule                                 `json:"rules"`
	DefaultOutputs map[string]expressionmodel.BusinessExpression `json:"default_outputs"`
}

type RuleSetRule struct {
	Key      string                                        `json:"key"`
	Priority int                                           `json:"priority"`
	When     expressionmodel.BusinessExpression            `json:"when"`
	Outputs  map[string]expressionmodel.BusinessExpression `json:"outputs"`
}

type RuleSetVersion struct {
	WorkspaceID  string
	Definition   RuleSetDefinition
	Version      string
	ResourceHash string
}

type RuleSetResolution struct {
	WorkspaceID    string         `json:"workspace_id"`
	RuleSetKey     string         `json:"rule_set_key"`
	Version        string         `json:"version"`
	ResourceHash   string         `json:"resource_hash"`
	EffectiveAt    string         `json:"effective_at"`
	EffectiveFrom  string         `json:"effective_from"`
	EffectiveTo    string         `json:"effective_to,omitempty"`
	MatchedRuleKey string         `json:"matched_rule_key,omitempty"`
	Outputs        map[string]any `json:"outputs"`
}

type RuleSetError struct {
	Code       string
	RuleSetKey string
	Cause      error
}

func (e *RuleSetError) Error() string     { return e.Code }
func (e *RuleSetError) Unwrap() error     { return e.Cause }
func (e *RuleSetError) ErrorCode() string { return strings.TrimSpace(e.Code) }
func (e *RuleSetError) ErrorParams() map[string]string {
	return map[string]string{"rule_set_key": strings.TrimSpace(e.RuleSetKey)}
}

func ParseEffectiveTime(value string) (time.Time, error) {
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
