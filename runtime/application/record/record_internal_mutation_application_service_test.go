// Internal-mutation domain service tests.
package record

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type internalMutationRepositoryProbe struct {
	recordrepository.RecordRepository
	inserted recordmodel.Record
	updated  recordmodel.Record
}

func (r *internalMutationRepositoryProbe) InsertRecord(_ context.Context, _ string, _ definitionmodel.ObjectSchema, record recordmodel.Record) error {
	r.inserted = record
	return nil
}

func (r *internalMutationRepositoryProbe) UpdateRecord(_ context.Context, _ string, _ definitionmodel.ObjectSchema, record recordmodel.Record) error {
	r.updated = record
	return nil
}

func TestInternalMutationPolicyRejectsUnapprovedBypass(t *testing.T) {
	customer := definitionmodel.ObjectSchema{Key: "customer"}
	err := RecordValidateInternalMutationPolicy(RecordInternalMutationSchedulerRuntime, RecordInternalMutationCreate, customer)
	assertRecordApplicationError(t, err, apperror.KindForbidden, "backend.record.internal_mutation_policy_denied", map[string]string{
		"policy": "scheduler_runtime", "operation": "create", "object": "customer",
	})
	err = RecordValidateInternalMutationPolicy(RecordInternalMutationOwnerPathRebuild, RecordInternalMutationUpdate, customer)
	assertRecordApplicationError(t, err, apperror.KindForbidden, "backend.record.internal_mutation_policy_denied", map[string]string{
		"policy": "owner_department_path_rebuild", "operation": "update", "object": "customer",
	})
	if err := RecordValidateInternalMutationPolicy(RecordInternalMutationSchedulerRuntime, RecordInternalMutationUpdate, definitionmodel.ObjectSchema{Key: "record_timer"}); err != nil {
		t.Fatalf("scheduler evidence update rejected: %v", err)
	}
}

func TestInternalMutationServiceOwnsWriteAndAudit(t *testing.T) {
	repository := &internalMutationRepositoryProbe{}
	var event, objectKey, recordID string
	var principal principalmodel.Principal
	var metadata map[string]any
	service := NewRecordInternalMutationApplicationService(RecordInternalMutationDependencies{
		Repository: repository,
		Audit: func(_ context.Context, gotEvent, gotObjectKey, gotRecordID string, gotPrincipal principalmodel.Principal, _ string, _, _ map[string]any, gotMetadata map[string]any) {
			event, objectKey, recordID, principal, metadata = gotEvent, gotObjectKey, gotRecordID, gotPrincipal, gotMetadata
		},
	})
	object := definitionmodel.ObjectSchema{Key: "record_timer_event"}
	record := recordmodel.Record{ID: "event-1"}
	if err := service.Insert(t.Context(), "workspace-primary", RecordInternalMutationSchedulerRuntime, object, record, " scheduler evidence "); err != nil {
		t.Fatal(err)
	}
	if repository.inserted.ID != "event-1" || event != "internal_record_mutation" || objectKey != "record_timer_event" || recordID != "event-1" || !principal.Known || principal.UserID != "system" {
		t.Fatalf("insert=%#v event=%q object=%q record=%q principal=%#v", repository.inserted, event, objectKey, recordID, principal)
	}
	if metadata["policy"] != "scheduler_runtime" || metadata["operation"] != "create" || metadata["reason"] != "scheduler evidence" {
		t.Fatalf("metadata = %#v", metadata)
	}
}
