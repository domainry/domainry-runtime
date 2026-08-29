package composition

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
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
func (schedulerMetadataFunction) MigrationPlan(context.Context, principalmodel.SystemScope, manifestmodel.ManifestSchema) ([]metadatamodel.MetadataMigrationStep, error) {
	return nil, nil
}
func (schedulerMetadataFunction) PublishDefinition(context.Context, principalmodel.SystemScope, string, string, metadatamodel.MetadataDefinitionUpsertRequest, auditmodel.AuditEvent) (metadatamodel.MetadataDefinition, error) {
	return metadatamodel.MetadataDefinition{}, nil
}
func (schedulerMetadataFunction) CompleteDefinitionRefresh(context.Context, principalmodel.SystemScope, string, string, string, string) error {
	return nil
}
func (schedulerMetadataFunction) ApplyDefinitionMutations(context.Context, principalmodel.SystemScope, []metadatamodel.MetadataDefinitionMutation, []auditmodel.AuditEvent, *changeplanmodel.BusinessChangePlanPublication) ([]metadatamodel.MetadataDefinition, error) {
	return nil, nil
}
func (schedulerMetadataFunction) DisableDefinition(context.Context, principalmodel.SystemScope, string, string) error {
	return nil
}
func (schedulerMetadataFunction) ListDefinitions(context.Context, principalmodel.SystemScope, string) ([]metadatamodel.MetadataDefinition, error) {
	return nil, nil
}
func (schedulerMetadataFunction) GetDefinition(context.Context, principalmodel.SystemScope, string, string) (metadatamodel.MetadataDefinition, bool, error) {
	return metadatamodel.MetadataDefinition{}, false, nil
}
func (schedulerMetadataFunction) ListDefinitionVersions(context.Context, principalmodel.SystemScope, string, string) ([]metadatamodel.MetadataDefinitionVersion, error) {
	return nil, nil
}
func (schedulerMetadataFunction) RollbackDefinition(context.Context, principalmodel.SystemScope, string, string, metadatamodel.MetadataDefinitionRollbackRequest, auditmodel.AuditEvent) (metadatamodel.MetadataDefinition, error) {
	return metadatamodel.MetadataDefinition{}, nil
}
func (schedulerMetadataFunction) ListLocalizedTexts(context.Context, string, metadatamodel.LocalizedTextQuery) ([]metadatamodel.LocalizedText, error) {
	return nil, nil
}
func (schedulerMetadataFunction) UpsertLocalizedText(context.Context, string, metadatamodel.LocalizedTextUpsertRequest) (metadatamodel.LocalizedText, error) {
	return metadatamodel.LocalizedText{}, nil
}

type schedulerMetadataSourceRepository struct {
	metadatarepository.MetadataRepository
	definitions []metadatamodel.MetadataDefinition
	definition  metadatamodel.MetadataDefinition
	found       bool
	versions    []metadatamodel.MetadataDefinitionVersion
	listErr     error
	getErr      error
	versionsErr error
}

func (r schedulerMetadataSourceRepository) ListDefinitions(context.Context, principalmodel.SystemScope, string) ([]metadatamodel.MetadataDefinition, error) {
	return r.definitions, r.listErr
}
func (r schedulerMetadataSourceRepository) GetDefinition(context.Context, principalmodel.SystemScope, string, string) (metadatamodel.MetadataDefinition, bool, error) {
	return r.definition, r.found, r.getErr
}
func (r schedulerMetadataSourceRepository) ListDefinitionVersions(context.Context, principalmodel.SystemScope, string, string) ([]metadatamodel.MetadataDefinitionVersion, error) {
	return r.versions, r.versionsErr
}

type schedulerMetadataMarker string

type schedulerMetadataDecoratedRepository struct {
	label string
	schedulerMetadataMarker
	metadatarepository.MetadataRepository
}

type schedulerMetadataPointerRepository struct {
	*schedulerMetadataSourceRepository
}

func TestSchedulerMetadataDefinitionSourceConditions(t *testing.T) {
	wantErr := errors.New("metadata failure")
	base := schedulerMetadataFunction(nil)
	valid := metadatamodel.MetadataDefinition{ResourceKey: "nightly", Payload: json.RawMessage(`{"enabled":true}`), CreatedAt: "created", UpdatedAt: "updated"}

	for _, source := range []schedulerMetadataDefinitionSource{{}, {repository: schedulerMetadataSourceRepository{}}} {
		if records, err := source.ListSchedulerDefinitions(t.Context()); err != nil || records != nil {
			t.Fatalf("unavailable list records=%#v err=%v", records, err)
		}
		if _, found, err := source.GetSchedulerDefinition(t.Context(), "nightly"); err != nil || found {
			t.Fatalf("unavailable get found=%v err=%v", found, err)
		}
		if versions, err := source.ListSchedulerDefinitionVersions(t.Context(), "nightly"); err != nil || versions != nil {
			t.Fatalf("unavailable versions=%#v err=%v", versions, err)
		}
	}

	cases := []struct {
		name string
		repo schedulerMetadataSourceRepository
		call func(schedulerMetadataDefinitionSource) error
	}{
		{"list error", schedulerMetadataSourceRepository{MetadataRepository: base, listErr: wantErr}, func(source schedulerMetadataDefinitionSource) error {
			_, err := source.ListSchedulerDefinitions(t.Context())
			return err
		}},
		{"list decode", schedulerMetadataSourceRepository{MetadataRepository: base, definitions: []metadatamodel.MetadataDefinition{{ResourceKey: "bad", Payload: []byte("{")}}}, func(source schedulerMetadataDefinitionSource) error {
			_, err := source.ListSchedulerDefinitions(t.Context())
			return err
		}},
		{"get error", schedulerMetadataSourceRepository{MetadataRepository: base, getErr: wantErr}, func(source schedulerMetadataDefinitionSource) error {
			_, _, err := source.GetSchedulerDefinition(t.Context(), "nightly")
			return err
		}},
		{"get decode", schedulerMetadataSourceRepository{MetadataRepository: base, found: true, definition: metadatamodel.MetadataDefinition{ResourceKey: "bad", Payload: []byte("{")}}, func(source schedulerMetadataDefinitionSource) error {
			_, _, err := source.GetSchedulerDefinition(t.Context(), "nightly")
			return err
		}},
		{"versions error", schedulerMetadataSourceRepository{MetadataRepository: base, versionsErr: wantErr}, func(source schedulerMetadataDefinitionSource) error {
			_, err := source.ListSchedulerDefinitionVersions(t.Context(), "nightly")
			return err
		}},
		{"versions decode", schedulerMetadataSourceRepository{MetadataRepository: base, versions: []metadatamodel.MetadataDefinitionVersion{{SchemaVersion: "v1", Payload: []byte("{")}}}, func(source schedulerMetadataDefinitionSource) error {
			_, err := source.ListSchedulerDefinitionVersions(t.Context(), "nightly")
			return err
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(schedulerMetadataDefinitionSource{repository: test.repo}); err == nil {
				t.Fatal("expected error")
			}
		})
	}

	repo := schedulerMetadataSourceRepository{
		MetadataRepository: base,
		definitions:        []metadatamodel.MetadataDefinition{valid},
		definition:         valid,
		found:              true,
		versions:           []metadatamodel.MetadataDefinitionVersion{{SchemaVersion: "v1", Payload: json.RawMessage(`{"enabled":false}`), CreatedAt: "created"}},
	}
	source := schedulerMetadataDefinitionSource{repository: repo}
	if records, err := source.ListSchedulerDefinitions(t.Context()); err != nil || len(records) != 1 || records[0].Data["key"] != "nightly" {
		t.Fatalf("records=%#v err=%v", records, err)
	}
	if record, found, err := source.GetSchedulerDefinition(t.Context(), "nightly"); err != nil || !found || record.ID != "nightly" {
		t.Fatalf("record=%#v found=%v err=%v", record, found, err)
	}
	missing := repo
	missing.found = false
	if _, found, err := (schedulerMetadataDefinitionSource{repository: missing}).GetSchedulerDefinition(t.Context(), "missing"); err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}
	if versions, err := source.ListSchedulerDefinitionVersions(t.Context(), "nightly"); err != nil || len(versions) != 1 || versions[0].Data["enabled"] != false {
		t.Fatalf("versions=%#v err=%v", versions, err)
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
	decorated := schedulerMetadataDecoratedRepository{MetadataRepository: base}
	if !schedulerMetadataRepositoryAvailable(decorated) {
		t.Fatal("decorated metadata repository reported unavailable")
	}
	repo := schedulerMetadataSourceRepository{MetadataRepository: base}
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
