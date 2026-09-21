package appschema

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	businesscalendarmodel "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type metadataCandidateRepository struct {
	appschemarepository.ApplicationSchemaRepository
	manifest       manifestmodel.ManifestSchema
	err            error
	definitions    map[string][]appschemamodel.ApplicationDefinition
	definitionErrs map[string]error
}

func (r metadataCandidateRepository) LoadManifest(context.Context, principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	return r.manifest, r.err
}

func (r metadataCandidateRepository) ListDefinitions(_ context.Context, _ principalmodel.SystemScope, resourceType string) ([]appschemamodel.ApplicationDefinition, error) {
	return r.definitions[resourceType], r.definitionErrs[resourceType]
}

func loadMetadataCandidateFixture(t *testing.T) manifestmodel.ManifestSchema {
	t.Helper()
	return manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer", Description: "Customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Name: "Name", Type: "text"}}}},
	}
}

func TestMetadataCandidateValidatesResourcesCreatedTogetherAsOneGraph(t *testing.T) {
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{manifest: loadMetadataCandidateFixture(t)}})
	mutations := []appschemamodel.ApplicationDefinitionMutation{
		{Operation: "create", ResourceType: "object", ResourceKey: "project", Request: appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"project","name":"Project","description":"Project"}`)}},
		{Operation: "create", ResourceType: "field", ResourceKey: "project.name", Request: appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: "project", Payload: json.RawMessage(`{"key":"name","name":"Name","type":"text","required":true}`)}},
	}
	if err := service.ValidateMetadataCandidate(t.Context(), mutations); err != nil {
		t.Fatalf("composed candidate rejected: %#v", err)
	}
}

func TestMetadataCandidateRejectsDanglingReferencesBeforePersistence(t *testing.T) {
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{manifest: loadMetadataCandidateFixture(t)}})
	mutations := []appschemamodel.ApplicationDefinitionMutation{{Operation: "create", ResourceType: "field", ResourceKey: "missing.name", Request: appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: "missing", Payload: json.RawMessage(`{"key":"name","name":"Name","type":"text"}`)}}}
	err := service.ValidateMetadataCandidate(t.Context(), mutations)
	if apperror.CodeOf(err) != "backend.metadata.candidate_invalid" {
		t.Fatalf("error=%v", err)
	}
}

func TestMetadataCandidateRejectsModuleOwnedSchedulerMutation(t *testing.T) {
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{manifest: loadMetadataCandidateFixture(t)}})
	scheduler := `{"key":"customer.refresh","name":"Refresh","status":"enabled","target_type":"workflow","target_key":"scheduled:customer.refresh","schedule_type":"interval","interval_seconds":60,"max_attempts":3,"timeout_seconds":300}`
	mutations := []appschemamodel.ApplicationDefinitionMutation{
		{Operation: "create", ResourceType: "scheduler", ResourceKey: "customer.refresh", Request: appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(scheduler)}},
	}
	if err := service.ValidateMetadataCandidate(t.Context(), mutations); apperror.CodeOf(err) != "backend.metadata.candidate_invalid" {
		t.Fatalf("module-owned scheduler mutation error=%#v", err)
	}
}

func TestMetadataCandidateRequiresNewRevisionWhenBusinessCalendarSemanticsChange(t *testing.T) {
	calendar := businesscalendarmodel.BusinessCalendarSchema{
		Key: "operations", Name: "Operations", Revision: "1", Timezone: "UTC",
		WeeklyWorkingIntervals: []businesscalendarmodel.BusinessCalendarWeeklySchedule{{Weekday: "monday", Intervals: []businesscalendarmodel.BusinessCalendarTimeInterval{{Start: "09:00", End: "18:00"}}}},
	}
	manifest := loadMetadataCandidateFixture(t)
	manifest.BusinessCalendars = []businesscalendarmodel.BusinessCalendarSchema{calendar}
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{manifest: manifest}})
	sameRevision := []appschemamodel.ApplicationDefinitionMutation{{Operation: "update", ResourceType: "business_calendar", ResourceKey: "operations", Request: appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"operations","name":"Operations","revision":"1","timezone":"UTC","weekly_working_intervals":[{"weekday":"monday","intervals":[{"start":"10:00","end":"18:00"}]}]}`)}}}
	if err := service.ValidateMetadataCandidate(t.Context(), sameRevision); apperror.CodeOf(err) != "backend.metadata.candidate_invalid" {
		t.Fatalf("same revision change error=%v", err)
	}
	newRevision := sameRevision
	newRevision[0].Request.Payload = json.RawMessage(`{"key":"operations","name":"Operations","revision":"2","timezone":"UTC","weekly_working_intervals":[{"weekday":"monday","intervals":[{"start":"10:00","end":"18:00"}]}]}`)
	if err := service.ValidateMetadataCandidate(t.Context(), newRevision); err != nil {
		t.Fatalf("new revision change rejected: %v", err)
	}
}
