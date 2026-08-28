package validation

import (
	"fmt"
	"sort"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func (state *validationState) validateObjectExportAssurancePolicy(path string, object definitionmodel.ObjectSchema) {
	policy := object.ExportAssurancePolicy
	if policy == nil {
		return
	}
	seen := map[string]bool{}
	for index, rawMethod := range policy.RequiredMethods {
		method := strings.TrimSpace(rawMethod)
		valid := method == definitionmodel.ActionAssuranceNormalLogin || method == definitionmodel.ActionAssuranceRecentReauth || method == definitionmodel.ActionAssuranceOTP || method == definitionmodel.ActionAssuranceMakerChecker || method == definitionmodel.ActionAssuranceWorkflowApproval
		if !valid {
			state.add(path+".export_assurance_policy.required_methods", "unknown method at index %d: %q", index, method)
		}
		if seen[method] {
			state.add(path+".export_assurance_policy.required_methods", "duplicate method %q", method)
		}
		seen[method] = true
	}
	if len(policy.RequiredMethods) == 0 {
		state.add(path+".export_assurance_policy.required_methods", "must contain at least one method")
	}
	if seen[definitionmodel.ActionAssuranceRecentReauth] {
		if policy.RecentReauthMaxAgeSeconds < 1 || policy.RecentReauthMaxAgeSeconds > 86400 {
			state.add(path+".export_assurance_policy.recent_reauth_max_age_seconds", "must be between 1 and 86400")
		}
	} else if policy.RecentReauthMaxAgeSeconds != 0 {
		state.add(path+".export_assurance_policy.recent_reauth_max_age_seconds", "requires recent_reauth method")
	}
	if strings.TrimSpace(policy.ApprovalVersionField) != "" || strings.TrimSpace(policy.ApprovalHashField) != "" || strings.TrimSpace(policy.MakerField) != "" {
		state.add(path+".export_assurance_policy", "record-bound action selector fields are not valid for object exports")
	}
}

func (state *validationState) validateObjectLedgerPolicy(path string, object definitionmodel.ObjectSchema) {
	policy := object.LedgerPolicy
	if policy == nil {
		return
	}
	if object.LifecyclePolicy == nil || strings.TrimSpace(object.LifecyclePolicy.Mode) != definitionmodel.ObjectLifecycleAppendOnly {
		state.add(path+".ledger_policy", "requires lifecycle_policy.mode append_only")
	}
	if strings.TrimSpace(policy.Integrity) != definitionmodel.ObjectLedgerIntegritySHA256Chain {
		state.add(path+".ledger_policy.integrity", "must be sha256_chain")
	}
	signature := strings.TrimSpace(policy.Signature)
	if signature != "" && signature != definitionmodel.ObjectLedgerSignatureNone && signature != definitionmodel.ObjectLedgerSignatureHMACSHA256 {
		state.add(path+".ledger_policy.signature", "must be none or hmac_sha256")
	}
	required := map[string][]string{
		"account_id": {"text", "relation"}, "business_key": {"text"}, "entry_kind": {"text"}, "direction": {"text"}, "balance_bucket": {"text"},
		"amount": {"currency"}, "currency": {"text"}, "source_reference": {"text"}, "actor_id": {"text", "relation"}, "rule_version": {"text"},
		"occurred_at": {"datetime"}, "sequence": {"number"}, "reversal_of": {"text", "relation"}, "previous_hash": {"text"}, "entry_hash": {"text"},
	}
	if signature == definitionmodel.ObjectLedgerSignatureHMACSHA256 {
		required["signature"] = []string{"text"}
	}
	keys := make([]string, 0, len(required))
	for key := range required {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		field := state.fields[object.Key][key]
		if field.Key == "" {
			state.add(path+".ledger_policy", "requires canonical field %q", key)
			continue
		}
		allowed := false
		for _, fieldType := range required[key] {
			allowed = allowed || strings.TrimSpace(field.Type) == fieldType
		}
		if !allowed {
			state.add(path+".ledger_policy", "field %q must have type %s", key, strings.Join(required[key], " or "))
		}
	}
}

func (state *validationState) validateObjectLifecyclePolicy(path string, object definitionmodel.ObjectSchema) {
	policy := object.LifecyclePolicy
	if policy == nil {
		return
	}
	mode := strings.TrimSpace(policy.Mode)
	switch mode {
	case definitionmodel.ObjectLifecycleMutable, definitionmodel.ObjectLifecycleAppendOnly:
		if strings.TrimSpace(policy.StateField) != "" || len(policy.ImmutableStates) > 0 {
			state.add(path+".lifecycle_policy", "state_field and immutable_states are only valid for immutable_after_state")
		}
	case definitionmodel.ObjectLifecycleSoftDeleteOnly:
		for _, fieldKey := range []string{"status", "deleted_at", "deleted_by"} {
			if state.fields[object.Key][fieldKey].Key == "" {
				state.add(path+".lifecycle_policy", "soft_delete_only requires field %q", fieldKey)
			}
		}
		if strings.TrimSpace(policy.StateField) != "" || len(policy.ImmutableStates) > 0 {
			state.add(path+".lifecycle_policy", "state_field and immutable_states are only valid for immutable_after_state")
		}
	case definitionmodel.ObjectLifecycleImmutableAfterState:
		fieldKey := strings.TrimSpace(policy.StateField)
		if fieldKey == "" {
			state.add(path+".lifecycle_policy.state_field", "is required")
		} else if state.fields[object.Key][fieldKey].Key == "" {
			state.add(path+".lifecycle_policy.state_field", "unknown field %q", fieldKey)
		}
		seen := map[string]bool{}
		if len(policy.ImmutableStates) == 0 {
			state.add(path+".lifecycle_policy.immutable_states", "must contain at least one state")
		}
		for index, value := range policy.ImmutableStates {
			value = strings.TrimSpace(value)
			if value == "" {
				state.add(fmt.Sprintf("%s.lifecycle_policy.immutable_states[%d]", path, index), "must not be empty")
			} else if seen[value] {
				state.add(fmt.Sprintf("%s.lifecycle_policy.immutable_states[%d]", path, index), "duplicate state %q", value)
			}
			seen[value] = true
		}
	default:
		state.add(path+".lifecycle_policy.mode", "must be mutable, soft_delete_only, append_only, or immutable_after_state")
	}
}
