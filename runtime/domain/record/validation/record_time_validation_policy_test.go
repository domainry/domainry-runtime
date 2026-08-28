package validation

import (
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestValidateRetentionPolicies(t *testing.T) {
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	object := definitionmodel.ObjectSchema{Key: "document", Validations: []definitionmodel.ValidationSchema{{
		Key: "document_retention", Type: "retention_guard", Message: "backend.document.retention_denied",
	}}}

	err := validateRetentionPoliciesAt(object, nil, map[string]any{"legal_hold": true}, "delete", now)
	assertRecordAppError(t, err, apperror.KindForbidden, "backend.document.retention_denied", map[string]string{
		"policy": "document_retention", "field": "legal_hold", "reason": "legal_hold",
	})

	err = validateRetentionPoliciesAt(object, nil, map[string]any{"retain_until": "2026-07-17"}, "delete", now)
	assertRecordAppError(t, err, apperror.KindForbidden, "backend.document.retention_denied", map[string]string{
		"policy": "document_retention", "field": "retain_until", "retain_until": "2026-07-17",
	})

	if err := validateRetentionPoliciesAt(object, nil, map[string]any{"retain_until": "2026-07-16"}, "delete", now); err != nil {
		t.Fatalf("expired retention rejected: %v", err)
	}
	err = validateRetentionPoliciesAt(object, nil, map[string]any{"retain_until": "not-a-date"}, "delete", now)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.validation.date_format", map[string]string{"field": "retain_until"})
}

func TestValidateRetentionPoliciesOnlyBlocksDeleteIntent(t *testing.T) {
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	object := definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Key: "retention", Type: "retention_policy"}}}
	before := map[string]any{"status": "active"}
	if err := validateRetentionPoliciesAt(object, before, map[string]any{"status": "active", "legal_hold": true}, "update", now); err != nil {
		t.Fatalf("non-delete update rejected: %v", err)
	}
	err := validateRetentionPoliciesAt(object, before, map[string]any{"status": "deleted", "legal_hold": true}, "update", now)
	assertRecordAppError(t, err, apperror.KindForbidden, "backend.policy.retention_guard", map[string]string{
		"policy": "retention", "field": "legal_hold", "reason": "legal_hold",
	})
}

func TestValidateStaleRecordPolicies(t *testing.T) {
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	object := definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{
		Key: "stale_customer", Type: "stale_record_guard", Config: map[string]any{"max_age_days": 30},
	}}}

	err := validateStaleRecordPoliciesAt(object, map[string]any{"last_activity_at": "2026-06-01"}, "update", now)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.stale_record_required_field", map[string]string{
		"policy": "stale_customer", "field": "stalled_reason", "date_field": "last_activity_at",
	})
	if err := validateStaleRecordPoliciesAt(object, map[string]any{"last_activity_at": "2026-06-01", "stalled_reason": "Customer paused"}, "update", now); err != nil {
		t.Fatalf("stale record with reason rejected: %v", err)
	}
	if err := validateStaleRecordPoliciesAt(object, map[string]any{"last_activity_at": "2026-07-01"}, "update", now); err != nil {
		t.Fatalf("recent record rejected: %v", err)
	}
	err = validateStaleRecordPoliciesAt(object, map[string]any{"last_activity_at": "invalid"}, "update", now)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.validation.date_format", map[string]string{"field": "last_activity_at"})
}
