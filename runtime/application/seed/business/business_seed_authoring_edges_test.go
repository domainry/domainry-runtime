package businessseed

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func businessSeedEdgePrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: principalmodel.InstallationWorkspaceID}, RequestID: "request"}, accessfixture.Bundle{Permissions: []string{"other", "workspace.admin"}})
}

func businessSeedEdgeService(records *businessSeedAuthoringRecordStore, provenance *businessSeedAuthoringProvenanceStore, objects func() []definitionmodel.ObjectSchema) *BusinessSeedAuthoringApplicationService {
	return NewBusinessSeedAuthoringApplicationService(BusinessSeedAuthoringDependencies{
		Records: records, Provenance: provenance, Objects: objects,
		Now: func() time.Time { return time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC) },
	})
}

func businessSeedEdgeObject() definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}, {Key: "refs", Type: "json"}}}
}

func TestBusinessSeedAuthoringAvailabilityContextAndReadAuthorizationEdges(t *testing.T) {
	principal := businessSeedEdgePrincipal()
	request := businessseedmodel.SeedRecordAuthoringRequest{ObjectKey: "customer", Data: map[string]any{"name": "A"}}
	var nilService *BusinessSeedAuthoringApplicationService
	if _, err := nilService.Get(t.Context(), "customer.a", principal); err == nil {
		t.Fatal("nil service Get accepted")
	}
	if _, err := nilService.Validate(t.Context(), "customer.a", request, principal); err == nil {
		t.Fatal("nil service Validate accepted")
	}
	for _, dependencies := range []BusinessSeedAuthoringDependencies{
		{},
		{Records: &businessSeedAuthoringRecordStore{}},
		{Records: &businessSeedAuthoringRecordStore{}, Provenance: &businessSeedAuthoringProvenanceStore{}},
	} {
		service := NewBusinessSeedAuthoringApplicationService(dependencies)
		if _, err := service.Get(t.Context(), "customer.a", principal); err == nil {
			t.Fatalf("incomplete Get accepted: %#v", dependencies)
		}
		if _, err := service.Validate(t.Context(), "customer.a", request, principal); err == nil {
			t.Fatalf("incomplete Validate accepted: %#v", dependencies)
		}
	}
	records := &businessSeedAuthoringRecordStore{values: map[string]recordmodel.Record{}}
	provenance := &businessSeedAuthoringProvenanceStore{values: map[string]businessseedmodel.BusinessSeedProvenance{}}
	service := businessSeedEdgeService(records, provenance, func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{businessSeedEdgeObject()} })
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Get(cancelled, "customer.a", principal); err == nil {
		t.Fatal("cancelled Get accepted")
	}
	if _, err := service.Validate(cancelled, "customer.a", request, principal); err == nil {
		t.Fatal("cancelled Validate accepted")
	}
	for _, invalid := range []principalmodel.Principal{principalmodel.Principal{}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: ""}}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "tenant"}}, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: principalmodel.InstallationWorkspaceID}}, accessfixture.Bundle{Permissions: []string{"other"}})} {
		if _, err := service.Get(t.Context(), "customer.a", invalid); err == nil {
			t.Fatalf("unauthorized Get accepted: %#v", invalid)
		}
	}
	if _, err := service.Get(t.Context(), "bad key", principal); err == nil {
		t.Fatal("invalid seed key accepted")
	}
	if _, err := service.Versions(t.Context(), "customer.missing", principal); err == nil {
		t.Fatal("Versions missing seed accepted")
	}
	if err := service.AuthorizeSeedRecordUpsert(principal); err != nil {
		t.Fatal(err)
	}
}

func TestBusinessSeedAuthoringGetAndHashFailureEdges(t *testing.T) {
	principal := businessSeedEdgePrincipal()
	records := &businessSeedAuthoringRecordStore{values: map[string]recordmodel.Record{}}
	provenance := &businessSeedAuthoringProvenanceStore{values: map[string]businessseedmodel.BusinessSeedProvenance{}}
	objects := func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{businessSeedEdgeObject()} }
	service := businessSeedEdgeService(records, provenance, objects)
	provenance.getErr = errors.New("provenance get")
	if _, err := service.Get(t.Context(), "customer.a", principal); !errors.Is(err, provenance.getErr) {
		t.Fatalf("provenance error=%v", err)
	}
	provenance.getErr = nil
	provenance.values["customer.a"] = businessseedmodel.BusinessSeedProvenance{SeedKey: "customer.a", ObjectKey: "missing", RecordID: "record"}
	if _, err := service.Get(t.Context(), "customer.a", principal); err == nil {
		t.Fatal("missing provenance object accepted")
	}
	provenance.values["customer.a"] = businessseedmodel.BusinessSeedProvenance{SeedKey: "customer.a", ObjectKey: "customer", RecordID: "record"}
	records.getErr = errors.New("record get")
	if _, err := service.Get(t.Context(), "customer.a", principal); !errors.Is(err, records.getErr) {
		t.Fatalf("record error=%v", err)
	}
	records.getErr = nil
	if _, err := service.Get(t.Context(), "customer.a", principal); err == nil {
		t.Fatal("orphaned provenance accepted")
	}
	delete(provenance.values, "customer.a")
	if hash, found, err := service.SeedRecordAuthoringHash(t.Context(), "customer.a", principal); err != nil || found || hash != "" {
		t.Fatalf("missing hash=%q found=%v err=%v", hash, found, err)
	}
	provenance.getErr = errors.New("hash get")
	if _, _, err := service.SeedRecordAuthoringHash(t.Context(), "customer.a", principal); !errors.Is(err, provenance.getErr) {
		t.Fatalf("hash error=%v", err)
	}
	provenance.getErr = nil
	provenance.values["customer.a"] = businessseedmodel.BusinessSeedProvenance{SeedKey: "customer.a", ObjectKey: "customer", RecordID: "record", ContentHash: "content"}
	records.values["record"] = recordmodel.Record{ID: "record", Data: map[string]any{"name": "A"}}
	if hash, found, err := service.SeedRecordAuthoringHash(t.Context(), "customer.a", principal); err != nil || !found || hash == "" {
		t.Fatalf("hash=%q found=%v err=%v", hash, found, err)
	}
}

func TestBusinessSeedAuthoringApplyPersistenceFailureEdges(t *testing.T) {
	principal := businessSeedEdgePrincipal()
	request := businessseedmodel.SeedRecordAuthoringRequest{ObjectKey: "customer", Data: map[string]any{"name": "A"}}
	records := &businessSeedAuthoringRecordStore{values: map[string]recordmodel.Record{}}
	provenance := &businessSeedAuthoringProvenanceStore{values: map[string]businessseedmodel.BusinessSeedProvenance{}}
	service := businessSeedEdgeService(records, provenance, func() []definitionmodel.ObjectSchema { return []definitionmodel.ObjectSchema{businessSeedEdgeObject()} })
	invalidRequest := request
	invalidRequest.ObjectKey = "missing"
	if _, err := service.Apply(t.Context(), "customer.a", invalidRequest, principal); err == nil {
		t.Fatal("Apply accepted invalid preparation")
	}
	provenance.getErr = errors.New("provenance")
	if _, err := service.Apply(t.Context(), "customer.a", request, principal); !errors.Is(err, provenance.getErr) {
		t.Fatalf("provenance lookup=%v", err)
	}
	provenance.getErr = nil
	prepared, err := service.Validate(t.Context(), "customer.a", request, principal)
	if err != nil {
		t.Fatal(err)
	}
	provenance.values["customer.a"] = businessseedmodel.BusinessSeedProvenance{SeedKey: "customer.a", ObjectKey: "other", RecordID: prepared.RecordID, ContentHash: prepared.ContentHash}
	if _, err := service.Apply(t.Context(), "customer.a", request, principal); err == nil {
		t.Fatal("object conflict accepted")
	}
	provenance.values["customer.a"] = businessseedmodel.BusinessSeedProvenance{SeedKey: "customer.a", ObjectKey: "customer", RecordID: prepared.RecordID, ContentHash: prepared.ContentHash}
	records.getErr = errors.New("existing get")
	if _, err := service.Apply(t.Context(), "customer.a", request, principal); !errors.Is(err, records.getErr) {
		t.Fatalf("existing lookup=%v", err)
	}
	records.getErr = nil
	if _, err := service.Apply(t.Context(), "customer.a", request, principal); err == nil {
		t.Fatal("orphaned existing provenance accepted")
	}
	delete(provenance.values, "customer.a")
	records.getErr = errors.New("record conflict lookup")
	if _, err := service.Apply(t.Context(), "customer.a", request, principal); !errors.Is(err, records.getErr) {
		t.Fatalf("record lookup=%v", err)
	}
	records.getErr = nil
	records.values[prepared.RecordID] = recordmodel.Record{ID: prepared.RecordID}
	if _, err := service.Apply(t.Context(), "customer.a", request, principal); err == nil {
		t.Fatal("record collision accepted")
	}
	delete(records.values, prepared.RecordID)
	records.insertErr = errors.New("insert")
	if _, err := service.Apply(t.Context(), "customer.a", request, principal); !errors.Is(err, records.insertErr) {
		t.Fatalf("insert=%v", err)
	}
	records.insertErr = nil
	provenance.upsertErr = errors.New("upsert")
	records.deleteErr = errors.New("delete")
	if _, err := service.Apply(t.Context(), "customer.a", request, principal); err == nil || !errors.Is(err, provenance.upsertErr) {
		t.Fatalf("compensation=%v", err)
	}
	records.deleteErr = nil
	provenance.upsertErr = nil
	delete(records.values, prepared.RecordID)
	if result, err := service.Apply(t.Context(), "customer.a", request, principal); err != nil || result.Replayed {
		t.Fatalf("auditless result=%#v err=%v", result, err)
	}
}

func TestBusinessSeedAuthoringPrepareAndReferenceEdges(t *testing.T) {
	principal := businessSeedEdgePrincipal()
	records := &businessSeedAuthoringRecordStore{values: map[string]recordmodel.Record{}}
	provenance := &businessSeedAuthoringProvenanceStore{values: map[string]businessseedmodel.BusinessSeedProvenance{"parent": {SeedKey: "parent", RecordID: "parent-record"}}}
	service := businessSeedEdgeService(records, provenance, func() []definitionmodel.ObjectSchema {
		return []definitionmodel.ObjectSchema{{Key: ""}, businessSeedEdgeObject()}
	})
	request := businessseedmodel.SeedRecordAuthoringRequest{ObjectKey: "customer", Data: map[string]any{"name": "A"}}
	wrongWorkspace := principal
	wrongWorkspace.WorkspaceID = "tenant"
	if _, err := service.Validate(t.Context(), "customer.a", request, wrongWorkspace); err == nil {
		t.Fatal("wrong workspace accepted")
	}
	blankWorkspace := principal
	blankWorkspace.WorkspaceID = " "
	if _, err := service.Validate(t.Context(), "customer.a", request, blankWorkspace); err == nil {
		t.Fatal("blank workspace accepted")
	}
	if _, err := service.Validate(t.Context(), "bad key", request, principal); err == nil {
		t.Fatal("bad seed key accepted")
	}
	noRequestKey := request
	noRequestKey.SourceKind = "manual"
	noRequestKey.SourceID = "explicit"
	result, err := service.Validate(t.Context(), "customer.a", noRequestKey, principal)
	if err != nil || result.SourceKind != "manual" || result.SourceID != "explicit" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	missingSource := request
	missingPrincipal := principal
	missingPrincipal.RequestID = ""
	if _, err := service.Validate(t.Context(), "customer.a", missingSource, missingPrincipal); err == nil {
		t.Fatal("missing source id accepted")
	}
	provenance.getErr = errors.New("reference get")
	if _, err := service.resolveReferenceValue(t.Context(), "$record:parent"); !errors.Is(err, provenance.getErr) {
		t.Fatalf("reference error=%v", err)
	}
	provenance.getErr = nil
	resolved, err := service.resolveReferenceValue(t.Context(), []any{"$record:parent", 1, "$record:"})
	if err != nil || len(resolved.([]any)) != 3 || resolved.([]any)[0] != "parent-record" {
		t.Fatalf("resolved=%#v err=%v", resolved, err)
	}
	provenance.getErr = errors.New("nested")
	if _, err := service.resolveReferenceValue(t.Context(), []any{map[string]any{"value": "$record:parent"}}); !errors.Is(err, provenance.getErr) {
		t.Fatalf("nested error=%v", err)
	}
	provenance.getErr = nil
	if _, err := service.resolveReferenceValue(t.Context(), map[string]any{"value": "$record:missing"}); err == nil {
		t.Fatal("missing nested reference accepted")
	}
	if _, found := service.object(""); found {
		t.Fatal("blank object found")
	}
	service.dependencies.Objects = nil
	if _, found := service.object("customer"); found {
		t.Fatal("object found without catalog")
	}
	if businessSeedAuthoringError("bad", "code") == nil || businessSeedAuthoringError("bad", "code", "key", "value") == nil {
		t.Fatal("error construction failed")
	}
}
