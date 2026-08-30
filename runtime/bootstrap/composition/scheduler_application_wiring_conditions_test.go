package composition

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type schedulerMetadataFunction func()

func (schedulerMetadataFunction) SnapshotRevision(context.Context, principalmodel.SystemScope) (string, error) {
	return "", nil
}
func (schedulerMetadataFunction) LoadManifest(context.Context, principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	return manifestmodel.ManifestSchema{}, nil
}
func (schedulerMetadataFunction) SyncManifest(context.Context, principalmodel.SystemScope, manifestmodel.ManifestSchema) error {
	return nil
}
func (schedulerMetadataFunction) MigrationPlan(context.Context, principalmodel.SystemScope, manifestmodel.ManifestSchema) ([]appschemamodel.ApplicationSchemaMigrationStep, error) {
	return nil, nil
}
func (schedulerMetadataFunction) ListDefinitions(context.Context, principalmodel.SystemScope, string) ([]appschemamodel.ApplicationDefinition, error) {
	return nil, nil
}
func (schedulerMetadataFunction) GetDefinition(context.Context, principalmodel.SystemScope, string, string) (appschemamodel.ApplicationDefinition, bool, error) {
	return appschemamodel.ApplicationDefinition{}, false, nil
}
func (schedulerMetadataFunction) ListLocalizedTexts(context.Context, string, appschemamodel.LocalizedTextQuery) ([]appschemamodel.LocalizedText, error) {
	return nil, nil
}

type schedulerMetadataSourceRepository struct {
	appschemarepository.ApplicationSchemaRepository
	definitions []appschemamodel.ApplicationDefinition
	definition  appschemamodel.ApplicationDefinition
	found       bool
	listErr     error
	getErr      error
}

func (r schedulerMetadataSourceRepository) ListDefinitions(context.Context, principalmodel.SystemScope, string) ([]appschemamodel.ApplicationDefinition, error) {
	return r.definitions, r.listErr
}
func (r schedulerMetadataSourceRepository) GetDefinition(context.Context, principalmodel.SystemScope, string, string) (appschemamodel.ApplicationDefinition, bool, error) {
	return r.definition, r.found, r.getErr
}

type schedulerMetadataMarker string

type schedulerMetadataDecoratedRepository struct {
	label string
	schedulerMetadataMarker
	appschemarepository.ApplicationSchemaRepository
}

type schedulerMetadataPointerRepository struct {
	*schedulerMetadataSourceRepository
}

func TestSchedulerApplicationDefinitionSourceConditions(t *testing.T) {
	wantErr := errors.New("metadata failure")
	base := schedulerMetadataFunction(nil)
	valid := appschemamodel.ApplicationDefinition{ResourceKey: "nightly", Payload: json.RawMessage(`{"enabled":true}`), CreatedAt: "created", UpdatedAt: "updated"}

	for _, source := range []schedulerApplicationDefinitionSource{{}, {repository: schedulerMetadataSourceRepository{}}} {
		if records, err := source.ListSchedulerDefinitions(t.Context()); err != nil || records != nil {
			t.Fatalf("unavailable list records=%#v err=%v", records, err)
		}
		if _, found, err := source.GetSchedulerDefinition(t.Context(), "nightly"); err != nil || found {
			t.Fatalf("unavailable get found=%v err=%v", found, err)
		}
	}

	cases := []struct {
		name string
		repo schedulerMetadataSourceRepository
		call func(schedulerApplicationDefinitionSource) error
	}{
		{"list error", schedulerMetadataSourceRepository{ApplicationSchemaRepository: base, listErr: wantErr}, func(source schedulerApplicationDefinitionSource) error {
			_, err := source.ListSchedulerDefinitions(t.Context())
			return err
		}},
		{"list decode", schedulerMetadataSourceRepository{ApplicationSchemaRepository: base, definitions: []appschemamodel.ApplicationDefinition{{ResourceKey: "bad", Payload: []byte("{")}}}, func(source schedulerApplicationDefinitionSource) error {
			_, err := source.ListSchedulerDefinitions(t.Context())
			return err
		}},
		{"get error", schedulerMetadataSourceRepository{ApplicationSchemaRepository: base, getErr: wantErr}, func(source schedulerApplicationDefinitionSource) error {
			_, _, err := source.GetSchedulerDefinition(t.Context(), "nightly")
			return err
		}},
		{"get decode", schedulerMetadataSourceRepository{ApplicationSchemaRepository: base, found: true, definition: appschemamodel.ApplicationDefinition{ResourceKey: "bad", Payload: []byte("{")}}, func(source schedulerApplicationDefinitionSource) error {
			_, _, err := source.GetSchedulerDefinition(t.Context(), "nightly")
			return err
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(schedulerApplicationDefinitionSource{repository: test.repo}); err == nil {
				t.Fatal("expected error")
			}
		})
	}

	repo := schedulerMetadataSourceRepository{
		ApplicationSchemaRepository: base,
		definitions:                 []appschemamodel.ApplicationDefinition{valid},
		definition:                  valid,
		found:                       true,
	}
	source := schedulerApplicationDefinitionSource{repository: repo}
	if records, err := source.ListSchedulerDefinitions(t.Context()); err != nil || len(records) != 1 || records[0].Data["key"] != "nightly" {
		t.Fatalf("records=%#v err=%v", records, err)
	}
	if record, found, err := source.GetSchedulerDefinition(t.Context(), "nightly"); err != nil || !found || record.ID != "nightly" {
		t.Fatalf("record=%#v found=%v err=%v", record, found, err)
	}
	missing := repo
	missing.found = false
	if _, found, err := (schedulerApplicationDefinitionSource{repository: missing}).GetSchedulerDefinition(t.Context(), "missing"); err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}
}

func TestSchedulerWiringAvailabilityAndSmallHelpers(t *testing.T) {
	base := schedulerMetadataFunction(nil)
	var typedNil *schedulerMetadataSourceRepository
	if schedulerMetadataRepositoryAvailable(nil) || schedulerMetadataRepositoryAvailable(typedNil) || schedulerMetadataRepositoryAvailable(schedulerMetadataSourceRepository{}) {
		t.Fatal("nil metadata repository reported available")
	}
	if !schedulerMetadataRepositoryAvailable(base) {
		t.Fatal("function metadata repository reported unavailable")
	}
	decorated := schedulerMetadataDecoratedRepository{ApplicationSchemaRepository: base}
	if !schedulerMetadataRepositoryAvailable(decorated) {
		t.Fatal("decorated metadata repository reported unavailable")
	}
	repo := schedulerMetadataSourceRepository{ApplicationSchemaRepository: base}
	if !schedulerMetadataRepositoryAvailable(&repo) {
		t.Fatal("non-nil pointer metadata repository reported unavailable")
	}
	if !schedulerMetadataRepositoryAvailable(schedulerMetadataPointerRepository{schedulerMetadataSourceRepository: &repo}) {
		t.Fatal("pointer-embedded metadata repository reported unavailable")
	}

	if _, err := schedulerMetadataRecord("bad", []byte("{"), "", ""); err == nil {
		t.Fatal("invalid scheduler metadata decoded")
	}
	objects := schemaObjectMap([]definitionmodel.ObjectSchema{{Key: "one"}, {Key: "two"}})
	if len(objects) != 2 || objects["two"].Key != "two" {
		t.Fatalf("objects=%#v", objects)
	}
	adapter := schedulerOperationRuntimeAdapter{}
	if err := adapter.ExecuteRecordTimer(t.Context(), schedulerapplication.RecordTimerExecution{}, principalmodel.Principal{}); err == nil {
		t.Fatal("missing timer runtime accepted")
	}
	called := false
	adapter.executeTimer = func(context.Context, schedulerapplication.RecordTimerExecution, principalmodel.Principal) error {
		called = true
		return nil
	}
	if err := adapter.ExecuteRecordTimer(t.Context(), schedulerapplication.RecordTimerExecution{}, principalmodel.Principal{}); err != nil || !called {
		t.Fatalf("timer called=%v err=%v", called, err)
	}
	if service := newSchedulerApplicationService(nil, nil, nil, nil, workerplatform.Dependencies{}); service == nil {
		t.Fatal("scheduler service is nil")
	}
}
