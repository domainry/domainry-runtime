package appschema

import (
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func candidateDefinition(resourceType, resourceKey, payload string) appschemamodel.ApplicationDefinition {
	return appschemamodel.ApplicationDefinition{ResourceType: resourceType, ResourceKey: resourceKey, Payload: []byte(payload)}
}

func TestMetadataCandidateServiceAvailabilityAndRepositoryFailures(t *testing.T) {
	var nilService *ApplicationSchemaApplicationService
	if code := apperror.CodeOf(nilService.ValidateMetadataCandidate(t.Context(), nil)); code != "backend.metadata.candidate_invalid" {
		t.Fatalf("nil service code=%q", code)
	}
	if code := apperror.CodeOf((&ApplicationSchemaApplicationService{}).ValidateMetadataCandidate(t.Context(), nil)); code != "backend.metadata.candidate_invalid" {
		t.Fatalf("nil repository code=%q", code)
	}
	want := errors.New("manifest unavailable")
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{err: want}})
	if err := service.ValidateMetadataCandidate(t.Context(), nil); !errors.Is(err, want) {
		t.Fatalf("load manifest error=%v", err)
	}

	invalid := loadMetadataCandidateFixture(t)
	invalid.Objects = append(invalid.Objects, invalid.Objects[0])
	service = NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{manifest: invalid}})
	if code := apperror.CodeOf(service.ValidateMetadataCandidate(t.Context(), nil)); code != "backend.metadata.candidate_invalid" {
		t.Fatalf("invalid graph code=%q", code)
	}

	invalidAction := loadMetadataCandidateFixture(t)
	invalidAction.Actions = []definitionmodel.ActionSchema{{
		Key: "customer.invalid", ObjectKey: "customer", Label: "Invalid", Kind: "unsupported",
		RequiresPermission: "customer.read", AuditEvent: "customer.invalid",
	}}
	service = NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{manifest: invalidAction}})
	if code := apperror.CodeOf(service.ValidateMetadataCandidate(t.Context(), nil)); code != "backend.metadata.candidate_invalid" {
		t.Fatalf("invalid action code=%q", code)
	}
}
