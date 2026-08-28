package businessseed

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	businessseedcontract "github.com/domainry/domainry-runtime/runtime/domain/businessseed/contract"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type businessSeedAuthoringRecordStore struct {
	recordrepository.RecordRepository
	values     map[string]recordmodel.Record
	getErr     error
	insertErr  error
	deleteErr  error
	deleteCall int
}

func (s *businessSeedAuthoringRecordStore) GetRecord(_ context.Context, _ string, _ definitionmodel.ObjectSchema, recordID string) (recordmodel.Record, bool, error) {
	if s.getErr != nil {
		return recordmodel.Record{}, false, s.getErr
	}
	value, found := s.values[recordID]
	return value, found, nil
}
func (s *businessSeedAuthoringRecordStore) InsertRecord(_ context.Context, _ string, _ definitionmodel.ObjectSchema, record recordmodel.Record) error {
	if s.insertErr != nil {
		return s.insertErr
	}
	s.values[record.ID] = record
	return nil
}
func (s *businessSeedAuthoringRecordStore) DeleteRecord(_ context.Context, _ string, _ definitionmodel.ObjectSchema, recordID string) error {
	s.deleteCall++
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.values, recordID)
	return nil
}

type businessSeedAuthoringProvenanceStore struct {
	values    map[string]businessseedmodel.BusinessSeedProvenance
	getErr    error
	upsertErr error
}

func (s *businessSeedAuthoringProvenanceStore) GetSeedProvenance(_ context.Context, seedKey string) (businessseedmodel.BusinessSeedProvenance, bool, error) {
	if s.getErr != nil {
		return businessseedmodel.BusinessSeedProvenance{}, false, s.getErr
	}
	value, found := s.values[seedKey]
	return value, found, nil
}
func (s *businessSeedAuthoringProvenanceStore) UpsertSeedProvenance(_ context.Context, value businessseedmodel.BusinessSeedProvenance) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.values[value.SeedKey] = value
	return nil
}

func TestBusinessSeedAuthoringValidatesAppliesReplaysAndResolvesReferences(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}, {Key: "parent_id", Type: "relation"}}}}
	records := &businessSeedAuthoringRecordStore{values: map[string]recordmodel.Record{}}
	provenance := &businessSeedAuthoringProvenanceStore{values: map[string]businessseedmodel.BusinessSeedProvenance{"customer.parent": {SeedKey: "customer.parent", RecordID: "customer_parent"}}}
	audits := 0
	service := NewBusinessSeedAuthoringApplicationService(BusinessSeedAuthoringDependencies{Records: records, Provenance: provenance, Objects: func() []definitionmodel.ObjectSchema { return objects }, Now: func() time.Time { return time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC) }, Audit: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
		audits++
	}})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: principalmodel.InstallationWorkspaceID}, RequestID: "builder-task"}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	request := businessseedmodel.SeedRecordAuthoringRequest{SeedKey: "customer.acme", ObjectKey: "customer", Data: map[string]any{"name": "Acme", "parent_id": "$record:customer.parent"}}
	validated, err := service.Validate(t.Context(), "customer.acme", request, principal)
	if err != nil || validated.RecordID != "customer_customer_acme" || validated.Data["parent_id"] != "customer_parent" || validated.ContentHash == "" {
		t.Fatalf("validated=%#v err=%v", validated, err)
	}
	created, err := service.Apply(t.Context(), "customer.acme", request, principal)
	if err != nil || created.Replayed || audits != 1 || records.values[created.RecordID].Data["name"] != "Acme" {
		t.Fatalf("created=%#v audits=%d err=%v", created, audits, err)
	}
	replayed, err := service.Apply(t.Context(), "customer.acme", request, principal)
	if err != nil || !replayed.Replayed || audits != 1 {
		t.Fatalf("replayed=%#v audits=%d err=%v", replayed, audits, err)
	}
	loaded, err := service.Get(t.Context(), "customer.acme", principal)
	if err != nil || loaded.RecordID != created.RecordID || loaded.ContentHash != created.ContentHash || !loaded.Replayed {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	versions, err := service.Versions(t.Context(), "customer.acme", principal)
	if err != nil || len(versions) != 1 || versions[0].ContentHash != created.ContentHash {
		t.Fatalf("versions=%#v err=%v", versions, err)
	}
	request.Data["name"] = "Changed"
	if _, err := service.Apply(t.Context(), "customer.acme", request, principal); apperror.CodeOf(err) != "backend.business_seed.content_conflict" {
		t.Fatalf("content conflict=%v", err)
	}
}

func TestBusinessSeedAuthoringRejectsInvalidInputsAndCompensatesProvenanceFailure(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}}}
	records := &businessSeedAuthoringRecordStore{values: map[string]recordmodel.Record{}}
	provenance := &businessSeedAuthoringProvenanceStore{values: map[string]businessseedmodel.BusinessSeedProvenance{}}
	service := NewBusinessSeedAuthoringApplicationService(BusinessSeedAuthoringDependencies{Records: records, Provenance: provenance, Objects: func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{object} }})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: principalmodel.InstallationWorkspaceID}, RequestID: "builder-task"}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	tests := []struct {
		name, seedKey, code string
		request             businessseedmodel.SeedRecordAuthoringRequest
		principal           principalmodel.Principal
	}{
		{name: "workspace", seedKey: "customer.a", request: businessseedmodel.SeedRecordAuthoringRequest{ObjectKey: "customer", Data: map[string]any{"name": "A"}}, principal: principalmodel.Principal{}, code: "backend.workspace_scope_required"},
		{name: "permission", seedKey: "customer.a", request: businessseedmodel.SeedRecordAuthoringRequest{ObjectKey: "customer", Data: map[string]any{"name": "A"}}, principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: principalmodel.InstallationWorkspaceID}}, code: "auth.permission_denied"},
		{name: "key mismatch", seedKey: "customer.a", request: businessseedmodel.SeedRecordAuthoringRequest{SeedKey: "customer.b", ObjectKey: "customer", Data: map[string]any{"name": "A"}}, principal: principal, code: "backend.business_seed.key_mismatch"},
		{name: "identity", seedKey: "customer.a", request: businessseedmodel.SeedRecordAuthoringRequest{ObjectKey: "identity_user", Data: map[string]any{}}, principal: principal, code: "backend.business_seed.identity_object_forbidden"},
		{name: "object", seedKey: "customer.a", request: businessseedmodel.SeedRecordAuthoringRequest{ObjectKey: "missing", Data: map[string]any{}}, principal: principal, code: "backend.business_seed.object_not_found"},
		{name: "required", seedKey: "customer.a", request: businessseedmodel.SeedRecordAuthoringRequest{ObjectKey: "customer", Data: map[string]any{}}, principal: principal, code: "backend.validation.required"},
		{name: "unknown", seedKey: "customer.a", request: businessseedmodel.SeedRecordAuthoringRequest{ObjectKey: "customer", Data: map[string]any{"name": "A", "unknown": true}}, principal: principal, code: "backend.validation.unknown_field"},
		{name: "reference", seedKey: "customer.a", request: businessseedmodel.SeedRecordAuthoringRequest{ObjectKey: "customer", Data: map[string]any{"name": "$record:missing"}}, principal: principal, code: "backend.business_seed.reference_not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Validate(t.Context(), test.seedKey, test.request, test.principal); apperror.CodeOf(err) != test.code {
				t.Fatalf("code=%q want=%q err=%v", apperror.CodeOf(err), test.code, err)
			}
		})
	}
	if _, err := service.Get(t.Context(), "customer.missing", principal); apperror.CodeOf(err) != "backend.business_seed.not_found" {
		t.Fatalf("get missing code=%q err=%v", apperror.CodeOf(err), err)
	}
	provenance.upsertErr = errors.New("provenance unavailable")
	request := businessseedmodel.SeedRecordAuthoringRequest{ObjectKey: "customer", Data: map[string]any{"name": "A"}}
	if _, err := service.Apply(t.Context(), "customer.a", request, principal); !errors.Is(err, provenance.upsertErr) || records.deleteCall != 1 || len(records.values) != 0 {
		t.Fatalf("compensation calls=%d records=%v err=%v", records.deleteCall, records.values, err)
	}
}

func TestSeedRecordAuthoringExamplesExecuteThroughRealValidator(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}, {Key: "status", Type: "text", Default: "active", Validation: definitionmodel.FieldValidation{Options: []string{"active", "inactive"}}}}}
	service := NewBusinessSeedAuthoringApplicationService(BusinessSeedAuthoringDependencies{
		Records:    &businessSeedAuthoringRecordStore{values: map[string]recordmodel.Record{}},
		Provenance: &businessSeedAuthoringProvenanceStore{values: map[string]businessseedmodel.BusinessSeedProvenance{}},
		Objects:    func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{object} },
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: principalmodel.InstallationWorkspaceID}, RequestID: "builder-task"}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	capability := businessseedcontract.SpecializeSeedRecordAuthoringCapability(object)
	for _, example := range capability.Examples {
		value := example.Value
		request := businessseedmodel.SeedRecordAuthoringRequest{
			SeedKey: value["seed_key"].(string), ObjectKey: value["object_key"].(string), Data: value["data"].(map[string]any),
		}
		if sourceKind, ok := value["source_kind"].(string); ok {
			request.SourceKind = sourceKind
		}
		if sourceID, ok := value["source_id"].(string); ok {
			request.SourceID = sourceID
		}
		_, err := service.Validate(t.Context(), request.SeedKey, request, principal)
		if len(example.ExpectedErrorCodes) == 0 {
			if err != nil {
				t.Fatalf("example %s failed real validator: %v", example.Name, err)
			}
			continue
		}
		if apperror.CodeOf(err) != example.ExpectedErrorCodes[0] {
			t.Fatalf("example %s code=%q want=%q err=%v", example.Name, apperror.CodeOf(err), example.ExpectedErrorCodes[0], err)
		}
	}
}
