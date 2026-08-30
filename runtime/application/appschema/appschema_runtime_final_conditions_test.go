package appschema

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type metadataReloadFailureRepository struct {
	appschemarepository.ApplicationSchemaRepository
	manifest manifestmodel.ManifestSchema
	err      error
}

func (r metadataReloadFailureRepository) LoadManifest(context.Context, principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	return r.manifest, r.err
}

func TestMetadataCanonicalCandidateRemainingOperations(t *testing.T) {
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{manifest: loadMetadataCandidateFixture(t)}})
	mutations := []appschemamodel.ApplicationDefinitionMutation{
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
	if _, err := service.CanonicalizeMetadataCandidate(t.Context(), []appschemamodel.ApplicationDefinitionMutation{candidateMutation("create", "unknown", "bad", "", `{}`)}); err == nil {
		t.Fatal("invalid candidate canonicalized")
	}
	action := definitionmodel.ActionSchema{Key: "customer.approve", ObjectKey: "customer", Label: "Approve", Kind: "record_operation", RequiresPermission: "customer.approve", AuditEvent: "customer.approved"}
	actionPayload, _ := json.Marshal(action)
	actionMutation := candidateMutation("create", "action", action.Key, "", string(actionPayload))
	actionCanonical, err := service.CanonicalizeMetadataCandidate(t.Context(), []appschemamodel.ApplicationDefinitionMutation{actionMutation})
	if err != nil || len(actionCanonical) != 1 {
		t.Fatalf("action canonical=%+v err=%v", actionCanonical, err)
	}
}

func TestMetadataCandidateAndDefinitionValidationSuccessConditions(t *testing.T) {
	manifest := loadMetadataCandidateFixture(t)
	manifest.Actions = []definitionmodel.ActionSchema{{Key: "customer.approve", ObjectKey: "customer", Label: "Approve", Kind: "record_operation", RequiresPermission: "customer.approve", AuditEvent: "customer.approved"}}
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataCandidateRepository{manifest: manifest}})
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
	runtime := &upsertMetadataRuntime{snapshot: appschemamodel.ApplicationSchemaSnapshot{Objects: manifest.Objects}}
	validation := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Runtime: runtime, Integrations: &metadataIntegrationValidationRepository{}})
	if _, err := validation.ValidateApplicationDefinitionPayload(t.Context(), "action", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: validAction}); err != nil {
		t.Fatalf("valid action payload rejected: %v", err)
	}
	if _, err := validation.ValidateApplicationDefinitionPayload(t.Context(), "automation_rule", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid automation JSON accepted")
	}
	if _, err := validation.ValidateApplicationDefinitionPayload(t.Context(), "automation_rule", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("invalid empty automation accepted")
	}
	_ = validation.ValidateAutomationRuleDefinition(t.Context(), automationmodel.AutomationRuleSchema{})
	if _, err := validation.ValidateApplicationDefinitionPayload(t.Context(), "report", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid report JSON accepted")
	}
	if _, err := validation.ValidateApplicationDefinitionPayload(t.Context(), "report", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("invalid empty report accepted")
	}
	report := reportmodel.ReportSchema{Key: "orders", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}}}
	reportPayload, _ := json.Marshal(report)
	_, _, _ = validation.ValidateApplicationDefinitionRequestPayload(t.Context(), "report", report.Key, appschemamodel.ApplicationDefinitionUpsertRequest{Payload: reportPayload})
}

func TestMetadataUpsertIdempotencyAndFieldReaderRemainingBlocks(t *testing.T) {
	repository := &upsertMetadataRepository{before: appschemamodel.ApplicationDefinition{ResourceKey: "order", SourceID: "source", Payload: json.RawMessage(`{"value":1}`)}}
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: repository})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	request := appschemamodel.ApplicationDefinitionUpsertRequest{SourceKind: "builder_v4", SourceID: "source", Payload: json.RawMessage(`{"value":2}`)}
	_, _, err := service.upsertApplicationDefinitionWithOptions(t.Context(), "object", "order", request, admin, ApplicationSchemaUpsertDefinitionOptions{Normalize: func(_ context.Context, _, _ string, request appschemamodel.ApplicationDefinitionUpsertRequest) (appschemamodel.ApplicationDefinitionUpsertRequest, error) {
		return request, nil
	}})
	if err == nil {
		t.Fatal("reused builder source accepted")
	}

	records := &metadataRequiredFieldRecordRepository{}
	fieldService := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{
		Runtime: &upsertMetadataRuntime{snapshot: appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "order"}}}}, Records: records,
	})
	payload := json.RawMessage(`{"key":"required","name":"Required","type":"text","required":true}`)
	_, _ = fieldService.ValidateApplicationDefinitionPayload(t.Context(), "field", appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: "order", Payload: payload})
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
	var nilService *ApplicationSchemaApplicationService
	nilService.UseActionDefinitionSource(func() []definitionmodel.ActionSchema { return nil })
	nilService.AddReloadObserver(func(appschemamodel.ApplicationSchemaSnapshot) {})
	service := &ApplicationSchemaApplicationService{}
	service.AddReloadObserver(nil)
	called := 0
	service.AddReloadObserver(func(appschemamodel.ApplicationSchemaSnapshot) { called++ })
	service.notifyReloadObservers(appschemamodel.ApplicationSchemaSnapshot{})
	if called != 1 {
		t.Fatalf("observer calls=%d", called)
	}

	before := appschemamodel.ApplicationDefinition{SourceID: "source", Payload: []byte(`{"key":"value"}`)}
	for _, request := range []appschemamodel.ApplicationDefinitionUpsertRequest{
		{SourceKind: "other", SourceID: "source"},
		{SourceKind: "builder_v4", SourceID: ""},
		{SourceKind: "builder_v4", SourceID: "other"},
	} {
		if err := metadataBuilderIdempotencyConflict(before, true, request); err != nil {
			t.Fatalf("unrelated idempotency request rejected: %v", err)
		}
	}
	invalid := appschemamodel.ApplicationDefinitionUpsertRequest{SourceKind: "builder_v4", SourceID: "source", Payload: []byte(`{`)}
	before.Payload = []byte(`{`)
	if err := metadataBuilderIdempotencyConflict(before, true, invalid); err != nil {
		t.Fatalf("identical invalid replay rejected: %v", err)
	}
}

func TestMetadataSnapshotReloadRemainingFailures(t *testing.T) {
	loadErr := errors.New("load manifest")
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: metadataReloadFailureRepository{err: loadErr}})
	if err := service.reloadMetadataFromSource(t.Context()); !errors.Is(err, loadErr) {
		t.Fatalf("load err=%v", err)
	}
	workflowErr := errors.New("initialize workflows")
	service = NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{
		Repository: metadataReloadFailureRepository{manifest: manifestmodel.ManifestSchema{}},
		Runtime:    &upsertMetadataRuntime{}, Workflows: upsertWorkflowInitializer{err: workflowErr},
	})
	if err := service.reloadMetadataFromSource(t.Context()); !errors.Is(err, workflowErr) {
		t.Fatalf("workflow err=%v", err)
	}
}
