package service

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type RecordPolicyDeniedObserver func(error, string)

func (v *RecordRelatedPolicyValidator) ValidateDomainPolicies(ctx context.Context, object definitionmodel.ObjectSchema, before, next map[string]any, recordID, operation string, principal principalmodel.Principal, denied RecordPolicyDeniedObserver) error {
	checks := []struct {
		policyType string
		validate   func() error
	}{
		{"state_machine", func() error {
			return recordvalidation.RecordValidateStateMachinePolicies(object, before, next, principal)
		}},
		{"threshold_permission", func() error {
			return recordvalidation.RecordValidateThresholdPermissionPolicies(object, before, next, operation, principal)
		}},
		{"immutable_after_status", func() error {
			return recordvalidation.RecordValidateImmutableAfterStatusPolicies(object, before, next, operation, principal)
		}},
		{"related_record_status", func() error { return v.ValidateRecordPolicies(ctx, object, next, operation, principal) }},
		{"related_numeric_limit", func() error { return v.ValidateNumericLimitPolicies(ctx, object, next, operation, principal) }},
		{"blocking_related_records", func() error { return v.ValidateBlockingRecordPolicies(ctx, object, next, operation, principal) }},
		{"dynamic_reference", func() error { return v.ValidateDynamicReferencePolicies(ctx, object, next, operation, principal) }},
		{"temporal_exclusion", func() error { return v.ValidateTimeOverlapPolicies(ctx, object, next, recordID, operation, principal) }},
		{"retention_guard", func() error { return recordvalidation.RecordValidateRetentionPolicies(object, before, next, operation) }},
		{"stale_record_guard", func() error { return recordvalidation.RecordValidateStaleRecordPolicies(object, next, operation) }},
		{"relation_context", func() error { return recordvalidation.RecordValidateRelationContextPolicies(object, next, operation) }},
	}
	for _, check := range checks {
		if err := check.validate(); err != nil {
			if denied != nil {
				denied(err, check.policyType)
			}
			return err
		}
	}
	return nil
}
