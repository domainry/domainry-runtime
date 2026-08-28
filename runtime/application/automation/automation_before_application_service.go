package automation

import (
	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationpolicy "github.com/domainry/domainry-runtime/runtime/domain/automation/policy"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	automationruntime "github.com/domainry/domainry-runtime/runtime/domain/automation/runtime"
	automationdomain "github.com/domainry/domainry-runtime/runtime/domain/automation/service"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"

	"context"
	"reflect"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const (
	defaultBeforeRuleTimeout  = 10 * time.Second
	defaultBeforeTotalTimeout = 30 * time.Second
)

type BeforeRuleExecutor func(context.Context, automationmodel.AutomationRuleSchema, map[string]any, map[string]any, map[string]any, string, principalmodel.Principal) (automationprojection.AutomationRuleTrace, error)

type BeforeServiceDependencies struct {
	Rules       automationcontract.AutomationRuleRegistry
	ExecuteRule BeforeRuleExecutor
}

// AutomationBeforeApplicationService coordinates ordered before-rule execution.
type AutomationBeforeApplicationService struct {
	dependencies BeforeServiceDependencies
}

func NewAutomationBeforeApplicationService(dependencies BeforeServiceDependencies) *AutomationBeforeApplicationService {
	return &AutomationBeforeApplicationService{dependencies: dependencies}
}

func (s *AutomationBeforeApplicationService) Run(ctx context.Context, objectKey, operation, recordID string, input, before, candidate map[string]any, principal principalmodel.Principal) ([]automationprojection.AutomationRuleTrace, error) {
	if err := automationAuthorizeCommand(principal); err != nil {
		return nil, err
	}
	rules := automationdomain.MatchingRules(s.dependencies.Rules.List(), objectKey, "before", operation, before, candidate)
	traces := make([]automationprojection.AutomationRuleTrace, 0, len(rules))
	totalCtx, totalCancel := context.WithTimeout(ctx, defaultBeforeTotalTimeout)
	defer totalCancel()
	writes := map[string]fieldWrite{}
	for _, rule := range rules {
		if err := totalCtx.Err(); err != nil {
			return traces, automationError(apperror.KindBadRequest, "backend.automation.total_timeout", nil, "object", objectKey, "operation", operation)
		}
		if stringSliceContains(principal.VisitedRuleKeys, rule.Key) {
			return traces, automationError(apperror.KindBadRequest, "backend.automation.recursion_detected", nil, "rule", rule.Key)
		}
		maxDepth := rule.Execution.MaxDepth
		if maxDepth <= 0 {
			maxDepth = 8
		}
		if principal.AutomationDepth >= maxDepth {
			return traces, automationError(apperror.KindBadRequest, "backend.automation.max_depth_exceeded", nil, "rule", rule.Key)
		}
		rulePrincipal := principal
		rulePrincipal.AutomationDepth++
		rulePrincipal.VisitedRuleKeys = append(append([]string{}, principal.VisitedRuleKeys...), rule.Key)
		beforeRule := recordcontract.RecordCloneData(candidate)
		ruleTimeout := defaultBeforeRuleTimeout
		if rule.Execution.TimeoutSeconds > 0 {
			ruleTimeout = time.Duration(rule.Execution.TimeoutSeconds) * time.Second
		}
		execCtx, cancel := context.WithTimeout(totalCtx, ruleTimeout)
		trace, err := s.dependencies.ExecuteRule(execCtx, rule, input, before, candidate, recordID, rulePrincipal)
		if err == nil && execCtx.Err() != nil {
			err = automationError(apperror.KindBadRequest, "backend.automation.rule_timeout", nil, "rule", rule.Key)
			trace.Status, trace.ErrorCode = "blocked", errorCode(err)
		}
		cancel()
		traces = append(traces, trace)
		if err != nil {
			return traces, err
		}
		for key, value := range candidate {
			if reflect.DeepEqual(beforeRule[key], value) {
				continue
			}
			if prior, exists := writes[key]; exists {
				return traces, automationError(apperror.KindBadRequest, "backend.automation.write_conflict", nil,
					"field", key, "first_rule", prior.ruleKey, "second_rule", rule.Key)
			}
			writes[key] = fieldWrite{ruleKey: rule.Key, value: value}
		}
	}
	return traces, nil
}

type fieldWrite struct {
	ruleKey string
	value   any
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == strings.TrimSpace(target) {
			return true
		}
	}
	return false
}

// executeBeforeRule is deliberately self-contained: it only evaluates in-memory
// conditions and deterministic payload transformations. It must never acquire a
// repository, invoke another application service, or dispatch an external effect.
func executeBeforeRule(ctx context.Context, rule automationmodel.AutomationRuleSchema, input, before, candidate map[string]any, principal principalmodel.Principal) (automationprojection.AutomationRuleTrace, error) {
	trace := automationprojection.AutomationRuleTrace{RuleKey: rule.Key, Status: "succeeded", Matched: true}
	render := &automationmodel.AutomationRenderContext{
		Payload: candidate,
		Input:   recordcontract.RecordCloneData(input),
		Before:  recordcontract.RecordCloneData(before),
		Actor:   map[string]any{"user_id": principal.UserID, "role": principal.RoleKey},
		Event:   map[string]any{"phase": "before", "operation": rule.Trigger.Operation, "object_key": rule.ObjectKey},
		Results: map[string]automationmodel.AutomationInstructionResult{},
	}
	matched := automationpolicy.AutomationConditionGroupMatches(rule.Conditions, func(clause automationmodel.AutomationConditionClause) bool {
		return automationruntime.AutomationAssert(map[string]any{"source": clause.Reference, "operator": clause.Operator, "value": clause.Value}, render) == nil
	})
	if !matched {
		trace.Status, trace.Matched = "skipped", false
		return trace, nil
	}
	for _, instruction := range rule.Instructions {
		if err := ctx.Err(); err != nil {
			trace.Status, trace.ErrorCode = "blocked", "backend.automation.rule_timeout"
			return trace, automationError(apperror.KindBadRequest, trace.ErrorCode, nil, "rule", rule.Key, "instruction", instruction.Key)
		}
		result := automationmodel.AutomationInstructionResult{Key: instruction.Key, Type: instruction.Type, Status: "success"}
		switch strings.TrimSpace(instruction.Type) {
		case "derive_fields":
			fields, err := automationruntime.AutomationRenderOptionalData(instruction.Config["fields"], render)
			if err != nil {
				return blockedBeforeInstruction(trace, instruction, err)
			}
			for key, value := range fields {
				if strings.TrimSpace(key) != "" {
					candidate[key] = value
				}
			}
			result.Data = fields
		case "assert":
			if err := automationruntime.AutomationAssert(instruction.Config, render); err != nil {
				return blockedBeforeInstruction(trace, instruction, err)
			}
			result.Data = map[string]any{"matched": true}
		default:
			err := automationError(apperror.KindBadRequest, "backend.automation.before_instruction_unsupported", nil, "instruction", instruction.Key, "type", instruction.Type)
			return blockedBeforeInstruction(trace, instruction, err)
		}
		trace.InstructionTraces = append(trace.InstructionTraces, automationprojection.AutomationInstructionTrace{
			Key: instruction.Key, Type: instruction.Type, Status: result.Status, Data: result.Data,
		})
	}
	return trace, nil
}

func blockedBeforeInstruction(trace automationprojection.AutomationRuleTrace, instruction automationmodel.AutomationInstructionSchema, err error) (automationprojection.AutomationRuleTrace, error) {
	trace.Status, trace.ErrorCode = "blocked", errorCode(err)
	trace.InstructionTraces = append(trace.InstructionTraces, automationprojection.AutomationInstructionTrace{
		Key: instruction.Key, Type: instruction.Type, Status: "blocked", ErrorCode: trace.ErrorCode,
	})
	return trace, err
}
