package record

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type internalMutationEdgeRepository struct {
	recordrepository.RecordRepository
	insertErr error
	updateErr error
	updated   recordmodel.Record
}

func (r *internalMutationEdgeRepository) InsertRecord(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record) error {
	return r.insertErr
}

func (r *internalMutationEdgeRepository) UpdateRecord(_ context.Context, _ string, _ definitionmodel.ObjectSchema, record recordmodel.Record) error {
	r.updated = record
	return r.updateErr
}

func TestInternalMutationUpdateAndRepositoryFailures(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "record_timer"}
	record := recordmodel.Record{ID: "timer-1"}
	repository := &internalMutationEdgeRepository{}
	service := NewRecordInternalMutationApplicationService(RecordInternalMutationDependencies{Repository: repository})
	if err := service.Update(t.Context(), "workspace-a", RecordInternalMutationSchedulerRuntime, object, record, "runtime progress"); err != nil || repository.updated.ID != record.ID {
		t.Fatalf("updated=%#v err=%v", repository.updated, err)
	}

	failure := errors.New("store unavailable")
	repository.insertErr = failure
	if err := service.Insert(t.Context(), "workspace-a", RecordInternalMutationSchedulerRuntime, object, record, "runtime progress"); apperror.CodeOf(err) != "backend.internal" || apperror.ParamsOf(err)["operation"] != "internal record insert" || !errors.Is(err, failure) {
		t.Fatalf("insert err=%#v", err)
	}
	repository.updateErr = failure
	if err := service.Update(t.Context(), "workspace-a", RecordInternalMutationSchedulerRuntime, object, record, "runtime progress"); apperror.CodeOf(err) != "backend.internal" || apperror.ParamsOf(err)["operation"] != "internal record update" || !errors.Is(err, failure) {
		t.Fatalf("update err=%#v", err)
	}
}

func TestInternalMutationOwnerPathPolicyRequiresBothFields(t *testing.T) {
	allowed := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{
		{Key: "owner_department_id", Type: "text", Config: map[string]any{"owner_department_id": true}},
		{Key: "owner_department_path", Type: "text", Config: map[string]any{"owner_department_path": true}},
	}}
	if err := RecordValidateInternalMutationPolicy(RecordInternalMutationOwnerPathRebuild, RecordInternalMutationUpdate, allowed); err != nil {
		t.Fatalf("owner path update rejected: %v", err)
	}
	if err := RecordValidateInternalMutationPolicy(RecordInternalMutationOwnerPathRebuild, RecordInternalMutationCreate, allowed); apperror.CodeOf(err) != "backend.record.internal_mutation_policy_denied" {
		t.Fatalf("owner path create err=%v", err)
	}
	missingPath := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{
		{Key: "owner_department_id", Type: "text", Config: map[string]any{"owner_department_id": true}},
	}}
	if err := RecordValidateInternalMutationPolicy(RecordInternalMutationOwnerPathRebuild, RecordInternalMutationUpdate, missingPath); apperror.CodeOf(err) != "backend.record.internal_mutation_policy_denied" {
		t.Fatalf("owner path missing path err=%v", err)
	}
	if params := apperror.ParamsOf(recordInternalMutationError(apperror.KindForbidden, "backend.record.internal_mutation_policy_denied", nil)); params != nil {
		t.Fatalf("empty params=%#v", params)
	}
	if params := apperror.ParamsOf(recordInternalMutationError(apperror.KindForbidden, "backend.record.internal_mutation_policy_denied", nil, " ", "ignored")); params != nil {
		t.Fatalf("blank key params=%#v", params)
	}
}

func TestInternalMutationServiceRejectsDisallowedPoliciesBeforeRepository(t *testing.T) {
	service := NewRecordInternalMutationApplicationService(RecordInternalMutationDependencies{Repository: &internalMutationEdgeRepository{}})
	object := definitionmodel.ObjectSchema{Key: "customer"}
	record := recordmodel.Record{ID: "customer-1"}
	if err := service.Insert(t.Context(), "workspace-a", RecordInternalMutationSchedulerRuntime, object, record, "invalid"); apperror.CodeOf(err) != "backend.record.internal_mutation_policy_denied" {
		t.Fatalf("insert policy err=%v", err)
	}
	if err := service.Update(t.Context(), "workspace-a", RecordInternalMutationSchedulerRuntime, object, record, "invalid"); apperror.CodeOf(err) != "backend.record.internal_mutation_policy_denied" {
		t.Fatalf("update policy err=%v", err)
	}
}
