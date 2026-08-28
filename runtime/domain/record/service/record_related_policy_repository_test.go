package service

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type relatedPolicyRepositoryProbe struct {
	recordrepository.RecordRepository
	pages   map[int]recordmodel.RecordPageResult
	record  recordmodel.Record
	found   bool
	err     error
	queries []recordmodel.RecordListQuery
}

func (r *relatedPolicyRepositoryProbe) ListRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	r.queries = append(r.queries, query)
	if r.err != nil {
		return recordmodel.RecordPageResult{}, r.err
	}
	return r.pages[query.Page], nil
}

func (r *relatedPolicyRepositoryProbe) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	return r.record, r.found, r.err
}

func TestRelatedPolicyValidatorBlocksMatchingRelatedRecords(t *testing.T) {
	repository := &relatedPolicyRepositoryProbe{pages: map[int]recordmodel.RecordPageResult{1: {
		Items: []recordmodel.Record{{ID: "task-1", Data: map[string]any{"order_id": "order-1", "status": "active"}}},
	}}}
	validator := NewRecordRelatedPolicyValidator(RecordRelatedPolicyDependencies{
		Repository: repository,
		Object: func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
			return definitionmodel.ObjectSchema{Key: key}, key == "task"
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
	object := definitionmodel.ObjectSchema{Key: "order", Validations: []definitionmodel.ValidationSchema{{
		Key: "no_active_tasks", Type: "blocking_related_records", Message: "backend.order.active_tasks",
		Config: map[string]any{
			"target_object": "task",
			"filters": []any{
				map[string]any{"field": "order_id", "source_field": "id"},
				map[string]any{"field": "status", "values": []any{"active"}},
			},
		},
	}}}

	err := validator.ValidateBlockingRecordPolicies(t.Context(), object, map[string]any{"id": "order-1"}, "delete", principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.order.active_tasks", map[string]string{"object": "task"})
	if len(repository.queries) != 1 || !reflect.DeepEqual(repository.queries[0].Filters, map[string]any{"order_id": "order-1"}) {
		t.Fatalf("blocking query = %#v", repository.queries)
	}
}

func TestRelatedPolicyValidatorValidatesDynamicReferences(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "activity", Validations: []definitionmodel.ValidationSchema{{
		Key: "target_exists", Type: "dynamic_reference_exists",
	}}}
	repository := &relatedPolicyRepositoryProbe{record: recordmodel.Record{ID: "customer-1"}, found: true}
	allow := true
	validator := NewRecordRelatedPolicyValidator(RecordRelatedPolicyDependencies{
		Repository: repository,
		Object: func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
			return definitionmodel.ObjectSchema{Key: key}, key == "customer"
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return allow },
	})
	data := map[string]any{"target_object": "customer", "target_record_id": "customer-1"}

	if err := validator.ValidateDynamicReferencePolicies(t.Context(), object, data, "create", principalmodel.Principal{}); err != nil {
		t.Fatalf("valid dynamic reference rejected: %v", err)
	}
	allow = false
	err := validator.ValidateDynamicReferencePolicies(t.Context(), object, data, "create", principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindForbidden, "backend.record.outside_scope", nil)

	allow = true
	repository.found = false
	err = validator.ValidateDynamicReferencePolicies(t.Context(), object, data, "create", principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.related_record_missing", map[string]string{"policy": "target_exists", "object": "customer"})

	repository.err = errors.New("store unavailable")
	err = validator.ValidateDynamicReferencePolicies(t.Context(), object, data, "create", principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "check dynamic reference"})
}

func TestRelatedPolicyValidatorFindsTimeOverlapAcrossPages(t *testing.T) {
	repository := &relatedPolicyRepositoryProbe{pages: map[int]recordmodel.RecordPageResult{
		1: {HasNext: true, Items: []recordmodel.Record{
			{ID: "booking-1", Data: map[string]any{"owner": "room-1", "starts_at": "2026-07-17T10:30:00Z", "ends_at": "2026-07-17T11:30:00Z", "visible": true}},
			{ID: "hidden", Data: map[string]any{"owner": "room-1", "starts_at": "2026-07-17T10:30:00Z", "ends_at": "2026-07-17T11:30:00Z", "visible": false}},
			{ID: "cancelled", Data: map[string]any{"owner": "room-1", "starts_at": "2026-07-17T10:30:00Z", "ends_at": "2026-07-17T11:30:00Z", "status": "cancelled", "visible": true}},
		}},
		2: {Items: []recordmodel.Record{{ID: "booking-2", Data: map[string]any{"owner": "room-1", "starts_at": "2026-07-17T11:00:00Z", "ends_at": "2026-07-17T13:00:00Z", "visible": true}}}},
	}}
	validator := NewRecordRelatedPolicyValidator(RecordRelatedPolicyDependencies{
		Repository: repository,
		CanAccess: func(_ principalmodel.Principal, _ definitionmodel.ObjectSchema, record recordmodel.Record) bool {
			visible, _ := record.Data["visible"].(bool)
			return visible
		},
	})
	object := definitionmodel.ObjectSchema{Key: "booking", Validations: []definitionmodel.ValidationSchema{{
		Key: "room_schedule", Type: "temporal_exclusion", Message: "backend.booking.room_busy", Config: map[string]any{"start_field": "starts_at", "end_field": "ends_at", "scope_fields": []any{"owner"}, "status_field": "status", "excluded_statuses": []any{"cancelled"}},
	}}}
	data := map[string]any{"owner": "room-1", "starts_at": "2026-07-17T10:00:00Z", "ends_at": "2026-07-17T12:00:00Z"}

	err := validator.ValidateTimeOverlapPolicies(t.Context(), object, data, "booking-1", "update", principalmodel.Principal{})
	assertRecordAppError(t, err, apperror.KindConflict, "backend.booking.room_busy", map[string]string{"policy": "room_schedule", "conflicting_record_id": "booking-2"})
	if len(repository.queries) != 2 || repository.queries[0].Page != 1 || repository.queries[1].Page != 2 {
		t.Fatalf("overlap queries = %#v", repository.queries)
	}

	repository.queries = nil
	data["status"] = "cancelled"
	if err := validator.ValidateTimeOverlapPolicies(t.Context(), object, data, "booking-1", "update", principalmodel.Principal{}); err != nil {
		t.Fatalf("cancelled booking rejected: %v", err)
	}
	if len(repository.queries) != 0 {
		t.Fatalf("cancelled booking queried repository: %#v", repository.queries)
	}
}
