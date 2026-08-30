package appschema

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func candidateDefinition(resourceType, resourceKey, payload string) appschemamodel.ApplicationDefinition {
	return appschemamodel.ApplicationDefinition{ResourceType: resourceType, ResourceKey: resourceKey, Payload: []byte(payload)}
}

func candidateVersion(version, payload string) appschemamodel.ApplicationDefinitionVersion {
	return appschemamodel.ApplicationDefinitionVersion{SchemaVersion: version, Payload: []byte(payload)}
}

func candidateSchedulerPayload(key, targetType, targetKey string) string {
	return fmt.Sprintf(`{"key":%q,"name":"Timer","status":"enabled","target_type":%q,"target_key":%q,"schedule_type":"interval","interval_seconds":60,"max_attempts":3,"timeout_seconds":300}`, key, targetType, targetKey)
}

func TestMetadataCandidateServiceAvailabilityAndRepositoryFailures(t *testing.T) {
	var nilService *ApplicationSchemaApplicationService
	if code := apperror.CodeOf(nilService.ValidateMetadataCandidate(t.Context(), nil)); code != "backend.change_plan.candidate_invalid" {
		t.Fatalf("nil service code=%q", code)
	}
	if code := apperror.CodeOf((&ApplicationSchemaApplicationService{}).ValidateMetadataCandidate(t.Context(), nil)); code != "backend.change_plan.candidate_invalid" {
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
	if code := apperror.CodeOf(service.ValidateMetadataCandidate(t.Context(), nil)); code != "backend.change_plan.candidate_invalid" {
		t.Fatalf("invalid graph code=%q", code)
	}

	invalidConnector := loadMetadataCandidateFixture(t)
	invalidConnector.Integrations.Connectors = []integrationmodel.ConnectorSchema{{Key: "broken"}}
	service = NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{manifest: invalidConnector}})
	if code := apperror.CodeOf(service.ValidateMetadataCandidate(t.Context(), nil)); code != "backend.change_plan.candidate_invalid" {
		t.Fatalf("invalid connector code=%q", code)
	}
	validConnector := loadMetadataCandidateFixture(t)
	validConnector.Integrations.Connectors = []integrationmodel.ConnectorSchema{{
		Key: "api", Type: "http", Provider: "api",
		Operations: []integrationmodel.ConnectorOperationSchema{{
			Key: "read", Method: "GET", ExecutionMode: "sync", SideEffect: "read",
			TimeoutDefaultSeconds: 1, TimeoutMaxSeconds: 2,
			Input:  []definitionmodel.FieldSchema{{Key: "id", Type: "text"}},
			Output: []definitionmodel.FieldSchema{{Key: "result", Type: "json"}},
		}},
	}}
	service = NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{manifest: validConnector}})
	if err := service.ValidateMetadataCandidate(t.Context(), nil); err != nil {
		t.Fatalf("valid connector candidate: %v", err)
	}

	invalidAction := loadMetadataCandidateFixture(t)
	invalidAction.Actions = []definitionmodel.ActionSchema{{
		Key: "customer.invalid", ObjectKey: "customer", Label: "Invalid", Kind: "unsupported",
		RequiresPermission: "customer.read", AuditEvent: "customer.invalid",
	}}
	service = NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{manifest: invalidAction}})
	if code := apperror.CodeOf(service.ValidateMetadataCandidate(t.Context(), nil)); code != "backend.change_plan.candidate_invalid" {
		t.Fatalf("invalid action code=%q", code)
	}
}

func TestMetadataCandidateSchedulerCompositionFailureAndReferenceBoundaries(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Workflows: []definitionmodel.WorkflowSchema{{Key: "customer.refresh"}},
		Reports:   []reportmodel.ReportSchema{{Key: "customer.summary"}},
	}
	listFailure := errors.New("scheduler list failed")
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{definitionErrs: map[string]error{"scheduler": listFailure}}})
	if err := service.validateMetadataCandidateSchedulers(t.Context(), manifest, nil); !errors.Is(err, listFailure) {
		t.Fatalf("list failure=%v", err)
	}

	tests := []struct {
		name        string
		definitions []appschemamodel.ApplicationDefinition
		mutations   []appschemamodel.ApplicationDefinitionMutation
		contains    string
	}{
		{"active json", []appschemamodel.ApplicationDefinition{candidateDefinition("scheduler", "bad", `{`)}, nil, "decode scheduler"},
		{"mutation json", nil, []appschemamodel.ApplicationDefinitionMutation{candidateMutation("create", "scheduler", "bad", "", `{`)}, "decode scheduler"},
		{"key mismatch", nil, []appschemamodel.ApplicationDefinitionMutation{candidateMutation("create", "scheduler", "expected", "", candidateSchedulerPayload("actual", "workflow", "scheduled:*"))}, "key mismatch"},
		{"invalid contract", []appschemamodel.ApplicationDefinition{candidateDefinition("scheduler", "bad", `{"key":"bad"}`)}, nil, "is invalid"},
		{"missing workflow", []appschemamodel.ApplicationDefinition{candidateDefinition("scheduler", "timer", candidateSchedulerPayload("timer", "workflow", "scheduled:missing"))}, nil, "missing workflow"},
		{"missing report export", []appschemamodel.ApplicationDefinition{candidateDefinition("scheduler", "timer", candidateSchedulerPayload("timer", "report_export", "missing"))}, nil, "missing report"},
		{"missing report refresh", []appschemamodel.ApplicationDefinition{candidateDefinition("scheduler", "timer", candidateSchedulerPayload("timer", "report_snapshot_refresh", "missing"))}, nil, "missing report"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{definitions: map[string][]appschemamodel.ApplicationDefinition{"scheduler": test.definitions}}})
			err := service.validateMetadataCandidateSchedulers(t.Context(), manifest, test.mutations)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error=%v, want %q", err, test.contains)
			}
		})
	}

	valid := []appschemamodel.ApplicationDefinition{
		candidateDefinition("scheduler", "all", candidateSchedulerPayload("all", "workflow", "scheduled:*")),
		candidateDefinition("scheduler", "workflow", candidateSchedulerPayload("workflow", "workflow", "scheduled:customer.refresh")),
		candidateDefinition("scheduler", "report", candidateSchedulerPayload("report", "report_export", "customer.summary")),
		candidateDefinition("scheduler", "refresh", candidateSchedulerPayload("refresh", "report_snapshot_refresh", "customer.summary")),
	}
	mutations := []appschemamodel.ApplicationDefinitionMutation{
		candidateMutation("archive", "scheduler", "removed", "", `{`),
		candidateMutation("delete", "scheduler", "workflow", "", `{`),
		candidateMutation("create", "object", "ignored", "", `{`),
	}
	service = NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{definitions: map[string][]appschemamodel.ApplicationDefinition{"scheduler": valid}}})
	if err := service.validateMetadataCandidateSchedulers(t.Context(), manifest, mutations); err != nil {
		t.Fatalf("valid scheduler composition: %v", err)
	}
}
