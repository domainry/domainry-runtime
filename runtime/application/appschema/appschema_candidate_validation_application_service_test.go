package appschema

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
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
	versions       map[string][]appschemamodel.ApplicationDefinitionVersion
	versionErrs    map[string]error
}

func (r metadataCandidateRepository) LoadManifest(context.Context, principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	return r.manifest, r.err
}

func (r metadataCandidateRepository) ListDefinitions(_ context.Context, _ principalmodel.SystemScope, resourceType string) ([]appschemamodel.ApplicationDefinition, error) {
	return r.definitions[resourceType], r.definitionErrs[resourceType]
}

func (r metadataCandidateRepository) ListDefinitionVersions(_ context.Context, _ principalmodel.SystemScope, resourceType, resourceKey string) ([]appschemamodel.ApplicationDefinitionVersion, error) {
	key := fmt.Sprintf("%s/%s", resourceType, resourceKey)
	return r.versions[key], r.versionErrs[key]
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
		{Operation: "create", ResourceType: "view", ResourceKey: "project_list", Request: appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"project_list","name":"Projects","object_key":"project","type":"table","config":{"columns":["name"]}}`)}},
	}
	if err := service.ValidateMetadataCandidate(t.Context(), mutations); err != nil {
		t.Fatalf("composed candidate rejected: %#v", err)
	}
}

func TestMetadataCandidateRejectsDanglingReferencesBeforePersistence(t *testing.T) {
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{manifest: loadMetadataCandidateFixture(t)}})
	mutations := []appschemamodel.ApplicationDefinitionMutation{{Operation: "create", ResourceType: "field", ResourceKey: "missing.name", Request: appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: "missing", Payload: json.RawMessage(`{"key":"name","name":"Name","type":"text"}`)}}}
	err := service.ValidateMetadataCandidate(t.Context(), mutations)
	if apperror.CodeOf(err) != "backend.change_plan.candidate_invalid" {
		t.Fatalf("error=%v", err)
	}
}

func TestMetadataCandidateResolvesSchedulerTargetCreatedInSameDraft(t *testing.T) {
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{manifest: loadMetadataCandidateFixture(t)}})
	workflow := `{"key":"customer.refresh","name":"Refresh","enabled":true,"trigger":{"type":"scheduled"},"trigger_contract":{"type":"scheduled"},"action":{"type":"workflow_graph"},"idempotency_keys":["scheduled_at"],"graph":{"version":2,"nodes":[{"id":"trigger","type":"trigger"}],"edges":[]}}`
	scheduler := `{"key":"customer.refresh","name":"Refresh","status":"enabled","target_type":"workflow","target_key":"scheduled:customer.refresh","schedule_type":"interval","interval_seconds":60,"max_attempts":3,"timeout_seconds":300}`
	mutations := []appschemamodel.ApplicationDefinitionMutation{
		{Operation: "create", ResourceType: "workflow", ResourceKey: "customer.refresh", Request: appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(workflow)}},
		{Operation: "create", ResourceType: "scheduler", ResourceKey: "customer.refresh", Request: appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(scheduler)}},
	}
	if err := service.ValidateMetadataCandidate(t.Context(), mutations); err != nil {
		t.Fatalf("same-draft scheduler/workflow rejected: %#v", err)
	}
	mutations = mutations[1:]
	if err := service.ValidateMetadataCandidate(t.Context(), mutations); apperror.CodeOf(err) != "backend.change_plan.candidate_invalid" || !strings.Contains(apperror.ParamsOf(err)["diagnostic"], "missing workflow") {
		t.Fatalf("missing scheduler target error=%#v", err)
	}
}
