package composition

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type schedulerMetadataDefinitions struct {
	values []metadatasdk.Definition
	err    error
}

func (s schedulerMetadataDefinitions) List(context.Context, metadatasdk.DefinitionQuery) ([]metadatasdk.Definition, error) {
	return s.values, s.err
}

func (s schedulerMetadataDefinitions) Get(_ context.Context, _, _, key string) (metadatasdk.Definition, bool, error) {
	if s.err != nil {
		return metadatasdk.Definition{}, false, s.err
	}
	for _, value := range s.values {
		if value.ResourceKey == key {
			return value, true, nil
		}
	}
	return metadatasdk.Definition{}, false, nil
}

func (s schedulerMetadataDefinitions) Snapshot(context.Context, metadatasdk.DefinitionQuery) (metadatasdk.DefinitionSnapshot, error) {
	return metadatasdk.DefinitionSnapshot{Definitions: s.values}, s.err
}

func TestSchedulerDefinitionSourceUsesMetadataBusinessPort(t *testing.T) {
	empty := schedulerDefinitionSourceAdapter{}
	if values, err := empty.ListSchedulerDefinitions(t.Context()); err != nil || values != nil {
		t.Fatalf("empty values=%#v err=%v", values, err)
	}
	wantErr := errors.New("metadata failure")
	if _, err := (schedulerDefinitionSourceAdapter{definitions: schedulerMetadataDefinitions{err: wantErr}}).ListSchedulerDefinitions(t.Context()); !errors.Is(err, wantErr) {
		t.Fatalf("list error=%v", err)
	}
	valid := metadatasdk.Definition{ResourceType: "scheduler", ResourceKey: "nightly", Payload: json.RawMessage(`{"enabled":true}`), CreatedAt: "created", UpdatedAt: "updated"}
	source := schedulerDefinitionSourceAdapter{definitions: schedulerMetadataDefinitions{values: []metadatasdk.Definition{valid}}}
	values, err := source.ListSchedulerDefinitions(t.Context())
	if err != nil || len(values) != 1 || values[0].Data["key"] != "nightly" {
		t.Fatalf("values=%#v err=%v", values, err)
	}
	value, found, err := source.GetSchedulerDefinition(t.Context(), "nightly")
	if err != nil || !found || value.Key != "nightly" {
		t.Fatalf("value=%#v found=%v err=%v", value, found, err)
	}
	if _, found, err := source.GetSchedulerDefinition(t.Context(), "missing"); err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}
	if _, err := (schedulerDefinitionSourceAdapter{definitions: schedulerMetadataDefinitions{values: []metadatasdk.Definition{{ResourceKey: "bad", Payload: []byte("{")}}}}).ListSchedulerDefinitions(t.Context()); err == nil {
		t.Fatal("invalid scheduler definition decoded")
	}
}

func TestSchedulerWiringSmallHelpers(t *testing.T) {
	if _, err := schedulerMetadataRecord("bad", []byte("{"), "", ""); err == nil {
		t.Fatal("invalid scheduler metadata decoded")
	}
	objects := schemaObjectMap([]definitionmodel.ObjectSchema{{Key: "one"}, {Key: "two"}})
	if len(objects) != 2 || objects["two"].Key != "two" {
		t.Fatalf("objects=%#v", objects)
	}
	if service := newTargetExecutionApplicationService(nil, workerplatform.Dependencies{}); service == nil {
		t.Fatal("target execution service is nil")
	}
}
