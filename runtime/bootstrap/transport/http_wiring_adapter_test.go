package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	"github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/transport/provision"
)

type businessSystemSnapshotReaderStub struct {
	value changeplanprojection.BusinessSystemSnapshot
	err   error
}

func (s businessSystemSnapshotReaderStub) Snapshot(*http.Request) (changeplanprojection.BusinessSystemSnapshot, error) {
	return s.value, s.err
}

type businessReferenceGraphReaderStub struct {
	value changeplanmodel.ReferenceGraph
	err   error
}

func (s businessReferenceGraphReaderStub) Graph(*http.Request) (changeplanmodel.ReferenceGraph, error) {
	return s.value, s.err
}

func TestChangePlanWiringAdaptersPreserveValuesAndErrors(t *testing.T) {
	wantErr := errors.New("unavailable")
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	snapshotSource := changePlanSnapshotSource{handler: businessSystemSnapshotReaderStub{err: wantErr}}
	if _, err := snapshotSource.Snapshot(request); !errors.Is(err, wantErr) {
		t.Fatalf("snapshot error=%v", err)
	}
	graphSource := changePlanGraphSource{handler: businessReferenceGraphReaderStub{err: wantErr}}
	if _, err := graphSource.Graph(request); !errors.Is(err, wantErr) {
		t.Fatalf("graph error=%v", err)
	}
}

type businessSeedReaderStub struct {
	results map[string]businessseedmodel.SeedRecordAuthoringResult
	err     error
}

func (s businessSeedReaderStub) Get(_ context.Context, key string, _ principalmodel.Principal) (businessseedmodel.SeedRecordAuthoringResult, error) {
	return s.results[key], s.err
}

func TestMetadataBusinessWiringHelpersCoverOptionalAndFailureEdges(t *testing.T) {
	useDirectAuthoringProjection(operationsapplication.NewOperationsApplicationService(nil, nil, nil, nil), nil)
	services := composition.NewRuntimeServices(t.Context(), composition.RuntimeServicesConfig{})
	if objects := runtimeObjectSchemas(services)(); len(objects) != 0 {
		t.Fatalf("unexpected runtime objects=%v", objects)
	}

	records, err := currentSeedRecordsCallback(nil)(t.Context(), nil, principalmodel.Principal{})
	if err != nil || len(records) != 0 {
		t.Fatalf("nil seed service records=%v err=%v", records, err)
	}
	wantErr := errors.New("seed unavailable")
	if _, err := currentSeedRecords(t.Context(), businessSeedReaderStub{err: wantErr}, []businessseedmodel.BusinessSeedProvenance{{SeedKey: "seed-1"}}, principalmodel.Principal{}); !errors.Is(err, wantErr) {
		t.Fatalf("seed error=%v", err)
	}
	records, err = currentSeedRecordsCallback(businessSeedReaderStub{results: map[string]businessseedmodel.SeedRecordAuthoringResult{
		"seed-1": {ObjectKey: "order", Data: map[string]any{"status": "draft"}, SourceKind: "template", SourceID: "fixture"},
	}})(t.Context(), []businessseedmodel.BusinessSeedProvenance{{SeedKey: "seed-1"}}, principalmodel.Principal{})
	if err != nil || len(records) != 1 || records[0].ObjectKey != "order" || records[0].SourceID != "fixture" {
		t.Fatalf("seed projection=%#v err=%v", records, err)
	}
}

func TestWithProvisionLifecycleRequiresReadableExistingState(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	called := false
	if err := withProvisionLifecycle(manifestPath, func() error { called = true; return nil }); err != nil || called {
		t.Fatalf("missing lifecycle err=%v called=%t", err, called)
	}
	if err := os.WriteFile(provision.LifecyclePath(manifestPath), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := withProvisionLifecycle(manifestPath, func() error { called = true; return nil }); err == nil || called {
		t.Fatalf("invalid lifecycle err=%v called=%t", err, called)
	}
	if err := os.WriteFile(provision.LifecyclePath(manifestPath), []byte(`{"version":"runtime-lifecycle-v1","status":"configuring"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	transitionErr := errors.New("transition failed")
	if err := withProvisionLifecycle(manifestPath, func() error { called = true; return transitionErr }); !errors.Is(err, transitionErr) || !called {
		t.Fatalf("transition err=%v called=%t", err, called)
	}

	if err := os.WriteFile(provision.LifecyclePath(manifestPath), []byte(`{"version":"runtime-lifecycle-v1","status":"configuring","builder_task_id":"task-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := beginAuthoringValidationCallback(manifestPath)("task-1"); err != nil {
		t.Fatal(err)
	}
	if err := completeAuthoringValidationCallback(manifestPath)("task-1", "snapshot-1", true); err != nil {
		t.Fatal(err)
	}
	if err := completeAuthoringDeliveryCallback(manifestPath)("task-1", "snapshot-1", true); err != nil {
		t.Fatal(err)
	}
}
