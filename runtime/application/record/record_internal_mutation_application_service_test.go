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
	err := RecordValidateInternalMutationPolicy(RecordInternalMutationPolicy("unknown"), RecordInternalMutationCreate, customer)
	assertRecordApplicationError(t, err, apperror.KindForbidden, "backend.record.internal_mutation_policy_denied", map[string]string{
		"policy": "unknown", "operation": "create", "object": "customer",
	})
	err = RecordValidateInternalMutationPolicy(RecordInternalMutationOwnerPathRebuild, RecordInternalMutationUpdate, customer)
	assertRecordApplicationError(t, err, apperror.KindForbidden, "backend.record.internal_mutation_policy_denied", map[string]string{
		"policy": "owner_department_path_rebuild", "operation": "update", "object": "customer",
	})
}

func TestInternalMutationServiceOwnsOwnerPathWriteAndAudit(t *testing.T) {
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
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{
		{Key: "owner_department_id", Type: "text", Config: map[string]any{"owner_department_id": true}},
		{Key: "owner_department_path", Type: "text", Config: map[string]any{"owner_department_path": true}},
	}}
	record := recordmodel.Record{ID: "run-1"}
	if err := service.Update(t.Context(), "workspace-primary", RecordInternalMutationOwnerPathRebuild, object, record, " owner path rebuilt "); err != nil {
		t.Fatal(err)
	}
	if repository.updated.ID != "run-1" || event != "internal_record_mutation" || objectKey != "customer" || recordID != "run-1" || !principal.Known || principal.UserID != "system" {
		t.Fatalf("update=%#v event=%q object=%q record=%q principal=%#v", repository.updated, event, objectKey, recordID, principal)
	}
	if metadata["policy"] != "owner_department_path_rebuild" || metadata["operation"] != "update" || metadata["reason"] != "owner path rebuilt" {
		t.Fatalf("metadata = %#v", metadata)
	}
}
