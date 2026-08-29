package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type metadataReloadFailureRepository struct {
	metadatarepository.MetadataRepository
	manifest manifestmodel.ManifestSchema
	err      error
}

func (r metadataReloadFailureRepository) LoadManifest(context.Context, principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	return r.manifest, r.err
}

func TestMetadataCanonicalCandidateRemainingOperations(t *testing.T) {
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{manifest: loadMetadataCandidateFixture(t)}})
	mutations := []metadatamodel.MetadataDefinitionMutation{
		candidateMutation("create", "object", "project", "", `{ "key": "project", "name": "Project", "description": "Project" }`),
		candidateMutation("create", "field", "project.name", "project", `{ "key": "name", "name": "Name", "type": "text" }`),
		candidateMutation("noop", "object", "ignored", "", `{`),
		candidateMutation("archive", "action", "missing.archive", "", `{`),
		candidateMutation("delete", "action", "missing.delete", "", `{`),
	}
	canonical, err := service.CanonicalizeMetadataCandidate(t.Context(), mutations)
	if err != nil || !json.Valid(canonical[0].Request.Payload) || string(canonical[2].Request.Payload) != "{" {
		t.Fatalf("canonical=%+v err=%v", canonical, err)
	}
	if _, err := service.CanonicalizeMetadataCandidate(t.Context(), []metadatamodel.MetadataDefinitionMutation{candidateMutation("create", "unknown", "bad", "", `{}`)}); err == nil {
		t.Fatal("invalid candidate canonicalized")
	}
	action := definitionmodel.ActionSchema{Key: "customer.approve", ObjectKey: "customer", Label: "Approve", Kind: "record_operation", RequiresPermission: "customer.approve", AuditEvent: "customer.approved"}
	actionPayload, _ := json.Marshal(action)
	actionMutation := candidateMutation("create", "action", action.Key, "", string(actionPayload))
	actionCanonical, err := service.CanonicalizeMetadataCandidate(t.Context(), []metadatamodel.MetadataDefinitionMutation{actionMutation})
	if err != nil || len(actionCanonical) != 1 {
		t.Fatalf("action canonical=%+v err=%v", actionCanonical, err)
	}
}

func TestMetadataCandidateAndDefinitionValidationSuccessConditions(t *testing.T) {
	manifest := loadMetadataCandidateFixture(t)
	manifest.Actions = []definitionmodel.ActionSchema{{Key: "customer.approve", ObjectKey: "customer", Label: "Approve", Kind: "record_operation", RequiresPermission: "customer.approve", AuditEvent: "customer.approved"}}
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{manifest: manifest}})
	if err := service.ValidateMetadataCandidate(t.Context(), nil); err != nil {
		t.Fatalf("valid action candidate rejected: %v", err)
	}
	if err := service.ValidateCurrentRuntimeDefinitions(t.Context(), nil); err != nil {
		t.Fatalf("current definitions rejected: %v", err)
	}
	validAction := json.RawMessage(`{"key":"customer.approve","object_key":"customer","label":"Approve","kind":"record_operation","requires_permission":"customer.approve","audit_event":"customer.approved"}`)
	if _, err := decodeActionDefinitionPayload(validAction); err != nil {
		t.Fatalf("valid action decode: %v", err)
	}
	if _, err := decodeActionDefinitionPayload(append(validAction, []byte(` {}`)...)); err == nil {
		t.Fatal("trailing action JSON accepted")
	}
	if _, err := decodeActionDefinitionPayload(append(validAction, []byte(` x`)...)); err == nil {
		t.Fatal("malformed trailing action JSON accepted")
	}
	runtime := &upsertMetadataRuntime{snapshot: metadatamodel.ApplicationSchemaSnapshot{Objects: manifest.Objects}}
	validation := NewApplicationSchemaService(ApplicationSchemaDependencies{Runtime: runtime, Integrations: &metadataIntegrationValidationRepository{}})
	if _, err := validation.ValidateMetadataDefinitionPayload(t.Context(), "action", metadatamodel.MetadataDefinitionUpsertRequest{Payload: validAction}); err != nil {
		t.Fatalf("valid action payload rejected: %v", err)
	}
	if _, err := validation.ValidateMetadataDefinitionPayload(t.Context(), "automation_rule", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid automation JSON accepted")
	}
	if _, err := validation.ValidateMetadataDefinitionPayload(t.Context(), "automation_rule", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("invalid empty automation accepted")
	}
	_ = validation.ValidateAutomationRuleDefinition(t.Context(), automationmodel.AutomationRuleSchema{})
	if _, err := validation.ValidateMetadataDefinitionPayload(t.Context(), "report", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid report JSON accepted")
	}
	if _, err := validation.ValidateMetadataDefinitionPayload(t.Context(), "report", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("invalid empty report accepted")
	}
	report := reportmodel.ReportSchema{Key: "orders", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}}}
	reportPayload, _ := json.Marshal(report)
	_, _, _ = validation.ValidateMetadataDefinitionRequestPayload(t.Context(), "report", report.Key, metadatamodel.MetadataDefinitionUpsertRequest{Payload: reportPayload})
}

func TestMetadataUpsertIdempotencyAndFieldReaderRemainingBlocks(t *testing.T) {
	repository := &upsertMetadataRepository{before: metadatamodel.MetadataDefinition{ResourceKey: "order", SourceID: "source", Payload: json.RawMessage(`{"value":1}`)}}
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{Repository: repository})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	request := metadatamodel.MetadataDefinitionUpsertRequest{SourceKind: "builder_v4", SourceID: "source", Payload: json.RawMessage(`{"value":2}`)}
	_, _, err := service.upsertMetadataDefinitionWithOptions(t.Context(), "object", "order", request, admin, MetadataUpsertDefinitionOptions{Normalize: func(_ context.Context, _, _ string, request metadatamodel.MetadataDefinitionUpsertRequest) (metadatamodel.MetadataDefinitionUpsertRequest, error) {
		return request, nil
	}})
	if err == nil {
		t.Fatal("reused builder source accepted")
	}

	records := &metadataRequiredFieldRecordRepository{}
	fieldService := NewApplicationSchemaService(ApplicationSchemaDependencies{
		Runtime: &upsertMetadataRuntime{snapshot: metadatamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "order"}}}}, Records: records,
	})
	payload := json.RawMessage(`{"key":"required","name":"Required","type":"text","required":true}`)
	_, _ = fieldService.ValidateMetadataDefinitionPayload(t.Context(), "field", metadatamodel.MetadataDefinitionUpsertRequest{ObjectKey: "order", Payload: payload})
	if records.calls == 0 {
		t.Fatal("required-field normalization skipped record reader")
	}
}

func TestInstalledAuthorizationRemainingMismatchConditions(t *testing.T) {
	installed := manifestmodel.ManifestSchema{
		Actions: []definitionmodel.ActionSchema{{Key: "customer.approve", RequiresPermission: "customer.approve"}},
	}
	persisted := installed
	persisted.Actions = []definitionmodel.ActionSchema{{Key: "customer.approve", RequiresPermission: "customer.other"}}
	if err := validateInstalledActionAuthorization(installed, persisted); err == nil {
		t.Fatal("permission mismatch accepted")
	}
	persisted = installed
	persisted.Actions = nil
	if err := validateInstalledActionAuthorization(installed, persisted); err == nil {
		t.Fatal("missing persisted action accepted")
	}
}

func TestMetadataServiceObserverAndIdempotencyRemainingConditions(t *testing.T) {
	var nilService *ApplicationSchemaService
	nilService.UseActionDefinitionSource(func() []definitionmodel.ActionSchema { return nil })
	nilService.AddReloadObserver(func(metadatamodel.ApplicationSchemaSnapshot) {})
	service := &ApplicationSchemaService{}
	service.AddReloadObserver(nil)
	called := 0
	service.AddReloadObserver(func(metadatamodel.ApplicationSchemaSnapshot) { called++ })
	service.notifyReloadObservers(metadatamodel.ApplicationSchemaSnapshot{})
	if called != 1 {
		t.Fatalf("observer calls=%d", called)
	}

	before := metadatamodel.MetadataDefinition{SourceID: "source", Payload: []byte(`{"key":"value"}`)}
	for _, request := range []metadatamodel.MetadataDefinitionUpsertRequest{
		{SourceKind: "other", SourceID: "source"},
		{SourceKind: "builder_v4", SourceID: ""},
		{SourceKind: "builder_v4", SourceID: "other"},
	} {
		if err := metadataBuilderIdempotencyConflict(before, true, request); err != nil {
			t.Fatalf("unrelated idempotency request rejected: %v", err)
		}
	}
	invalid := metadatamodel.MetadataDefinitionUpsertRequest{SourceKind: "builder_v4", SourceID: "source", Payload: []byte(`{`)}
	before.Payload = []byte(`{`)
	if err := metadataBuilderIdempotencyConflict(before, true, invalid); err != nil {
		t.Fatalf("identical invalid replay rejected: %v", err)
	}
}

func TestMetadataSnapshotReloadRemainingFailures(t *testing.T) {
	loadErr := errors.New("load manifest")
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{Repository: metadataReloadFailureRepository{err: loadErr}})
	if err := service.reloadMetadataFromSource(t.Context()); !errors.Is(err, loadErr) {
		t.Fatalf("load err=%v", err)
	}
	workflowErr := errors.New("initialize workflows")
	service = NewApplicationSchemaService(ApplicationSchemaDependencies{
		Repository: metadataReloadFailureRepository{manifest: manifestmodel.ManifestSchema{}},
		Runtime:    &upsertMetadataRuntime{}, Workflows: upsertWorkflowInitializer{err: workflowErr},
	})
	if err := service.reloadMetadataFromSource(t.Context()); !errors.Is(err, workflowErr) {
		t.Fatalf("workflow err=%v", err)
	}
}
