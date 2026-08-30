package appschema

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestMetadataShortCircuitAuthorizationAndParameterOutcomes(t *testing.T) {
	admin := metadataSchemaAdmin()
	unknownWithWorkspace := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-1"}}
	service := metadataLifecycleService(&metadataLifecycleFailureRepository{}, true, nil)
	request := appschemamodel.ApplicationDefinitionRollbackRequest{TargetVersion: "1", ExpectedSchemaHash: "hash", ExpectedReferenceGraphHash: "graph", BusinessReason: "reason", ChangePlanID: "plan", AuthoringContractVersion: "v1", AuthoringContractHash: "hash"}
	options := ApplicationSchemaRollbackDefinitionOptions{AuthoringContractVersion: "v1", AuthoringContractHash: "hash", ResolveImpact: func(context.Context, string, string, principalmodel.Principal) (RollbackReferenceImpact, error) {
		return RollbackReferenceImpact{GraphHash: "graph"}, nil
	}}
	for name, call := range map[string]func() error{
		"rollback": func() error {
			_, _, err := service.rollbackApplicationDefinitionWithOptions(t.Context(), "action", "key", request, unknownWithWorkspace, options)
			return err
		},
		"disable": func() error {
			return service.disableApplicationDefinitionWithOptions(t.Context(), "action", "key", unknownWithWorkspace, ApplicationSchemaDisableDefinitionOptions{})
		},
		"upsert": func() error {
			_, _, err := service.upsertApplicationDefinitionWithOptions(t.Context(), "action", "key", appschemamodel.ApplicationDefinitionUpsertRequest{}, unknownWithWorkspace, ApplicationSchemaUpsertDefinitionOptions{})
			return err
		},
		"localized": func() error {
			_, err := service.LocalizedTextCoverage(t.Context(), "en-US", "", unknownWithWorkspace)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); apperror.KindOf(err) != apperror.KindForbidden {
				t.Fatalf("error=%v", err)
			}
		})
	}

	for name, mutate := range map[string]func(*appschemamodel.ApplicationDefinitionRollbackRequest){
		"target":      func(value *appschemamodel.ApplicationDefinitionRollbackRequest) { value.TargetVersion = "" },
		"schema hash": func(value *appschemamodel.ApplicationDefinitionRollbackRequest) { value.ExpectedSchemaHash = "" },
		"plan":        func(value *appschemamodel.ApplicationDefinitionRollbackRequest) { value.ChangePlanID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			value := request
			mutate(&value)
			if _, _, err := service.rollbackApplicationDefinitionWithOptions(t.Context(), "action", "key", value, admin, options); apperror.CodeOf(err) != "backend.metadata.rollback_contract_required" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	stale := options
	stale.AuthoringContractVersion = "stale"
	if _, _, err := service.rollbackApplicationDefinitionWithOptions(t.Context(), "action", "key", request, admin, stale); apperror.CodeOf(err) != "backend.metadata.rollback_contract_stale" {
		t.Fatalf("stale version error=%v", err)
	}
	_ = metadataError(apperror.KindBadRequest, "bad", "", "ignored")
}

func TestMetadataRemainingLifecycleConditionOutcomes(t *testing.T) {
	admin := metadataSchemaAdmin()
	request := appschemamodel.ApplicationDefinitionRollbackRequest{TargetVersion: "2", ExpectedSchemaHash: "hash", ExpectedReferenceGraphHash: "graph", BusinessReason: "reason", ChangePlanID: "plan", AuthoringContractVersion: "v1", AuthoringContractHash: "hash"}
	base := upsertMetadataRepository{before: appschemamodel.ApplicationDefinition{ResourceKey: "key"}, versions: []appschemamodel.ApplicationDefinitionVersion{{SchemaVersion: "1"}, {SchemaVersion: "2"}}, rollback: appschemamodel.ApplicationDefinition{SchemaVersion: "2"}}
	options := ApplicationSchemaRollbackDefinitionOptions{AuthoringContractVersion: "v1", AuthoringContractHash: "hash", ResolveImpact: func(context.Context, string, string, principalmodel.Principal) (RollbackReferenceImpact, error) {
		return RollbackReferenceImpact{GraphHash: "graph"}, nil
	}}
	service := metadataLifecycleService(&metadataLifecycleFailureRepository{upsertMetadataRepository: base}, true, nil)
	if _, _, err := service.rollbackApplicationDefinitionWithOptions(t.Context(), "action", "key", request, admin, options); err != nil {
		t.Fatal(err)
	}
	if err := service.disableApplicationDefinitionWithOptions(t.Context(), "action", "key", admin, ApplicationSchemaDisableDefinitionOptions{ResolveImpact: func(context.Context, string, string, principalmodel.Principal) (DisableReferenceImpact, error) {
		return DisableReferenceImpact{}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	invalidPayload := appschemamodel.ApplicationDefinition{ResourceKey: "key", Payload: json.RawMessage(`{`)}
	if value := DefinitionAuditValue(invalidPayload, true); value["payload"] != nil {
		if _, exists := value["payload"]; exists {
			t.Fatalf("invalid payload projected: %v", value)
		}
	}

	nonAdmin := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: admin.WorkspaceID, UserID: admin.UserID}}
	schemaService := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: &metadataSchemaEdgeRepository{}, Dictionary: &metadataDictionaryEdgeRuntime{}})
	for name, call := range map[string]func() error{
		"definitions": func() error {
			_, err := schemaService.ListApplicationDefinitions(t.Context(), "object", "workspace-1", nonAdmin)
			return err
		},
		"texts": func() error {
			_, err := schemaService.ListLocalizedTexts(t.Context(), appschemamodel.LocalizedTextQuery{WorkspaceID: "workspace-1"}, nonAdmin)
			return err
		},
		"upsert": func() error {
			_, err := schemaService.UpsertLocalizedText(t.Context(), appschemamodel.LocalizedTextUpsertRequest{WorkspaceID: "workspace-1"}, nonAdmin)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); apperror.CodeOf(err) != "auth.permission_denied" {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestMetadataFieldMutationEarlyReturnConditions(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "existing", Type: "text", Required: true}}}}
	request := func(field definitionmodel.FieldSchema) appschemamodel.ApplicationDefinitionUpsertRequest {
		payload, _ := json.Marshal(field)
		return appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: "order", Payload: payload}
	}
	reader := func(context.Context, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		t.Fatal("early-return mutation read records")
		return recordmodel.RecordPageResult{}, nil
	}
	for _, field := range []definitionmodel.FieldSchema{
		{Key: "optional", Type: "text"},
		{Key: "default", Type: "text", Required: true, Default: "value"},
		{Key: "default_value", Type: "text", Required: true, DefaultValue: "value"},
		{Key: "existing", Type: "text", Required: true},
	} {
		if _, err := ApplicationSchemaNormalizeFieldMutation(t.Context(), request(field), objects, []string{"text"}, reader); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ApplicationSchemaNormalizeFieldMutation(t.Context(), request(definitionmodel.FieldSchema{Key: "optional", Type: "text"}), objects, []string{"text"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataFinalConditionOutcomes(t *testing.T) {
	validScope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "nil metadata application watcher")
	var nilApplication *ApplicationSchemaApplicationService
	ctx, cancel := context.WithCancel(t.Context())
	done := nilApplication.StartSnapshotWatcher(ctx, time.Millisecond, validScope)
	time.Sleep(2 * time.Millisecond)
	cancel()
	<-done

	validDictionary := json.RawMessage(`{"key":"dict","items":[{"key":"root","value":"Root"}]}`)
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{})
	if _, issues, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "dictionary", "dict", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: validDictionary}); err != nil || len(issues) != 0 {
		t.Fatalf("issues=%v err=%v", issues, err)
	}

	configuredPayload, _ := json.Marshal(definitionmodel.FieldSchema{Key: "required", Type: "text", Required: true, Config: map[string]any{"_definition_object_key": "order"}})
	if _, err := ApplicationSchemaNormalizeFieldMutation(t.Context(), appschemamodel.ApplicationDefinitionUpsertRequest{Payload: configuredPayload}, []definitionmodel.ObjectSchema{{Key: "other"}, {Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "required", Type: "text"}}}}, []string{"text"}, func(context.Context, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{{Data: map[string]any{"required": "value"}}}, Total: 1}, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataWatcherNilAndCanceledErrorConditions(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var nilWatcher *applicationSchemaSnapshotWatcherApplicationService
	done := nilWatcher.Start(ctx, time.Millisecond)
	time.Sleep(2 * time.Millisecond)
	cancel()
	<-done

	ctx, cancel = context.WithCancel(t.Context())
	watcher := newApplicationSchemaSnapshotWatcherApplicationService(ApplicationSchemaSnapshotWatcherDependencies{Revision: func(context.Context) (string, error) { return "", nil }})
	done = watcher.Start(ctx, time.Millisecond)
	time.Sleep(2 * time.Millisecond)
	cancel()
	<-done

	ctx, cancel = context.WithCancel(t.Context())
	watcher = newApplicationSchemaSnapshotWatcherApplicationService(ApplicationSchemaSnapshotWatcherDependencies{
		Revision: func(context.Context) (string, error) {
			cancel()
			return "", apperror.New(apperror.KindInternal, "revision", nil, nil)
		},
		Reload: func(context.Context) error { return nil },
	})
	done = watcher.Start(ctx, time.Millisecond)
	<-done

	ctx, cancel = context.WithCancel(t.Context())
	calls := 0
	watcher = newApplicationSchemaSnapshotWatcherApplicationService(ApplicationSchemaSnapshotWatcherDependencies{
		Revision: func(context.Context) (string, error) {
			calls++
			if calls == 1 {
				return "r1", nil
			}
			return "r2", nil
		},
		Reload: func(context.Context) error {
			cancel()
			return apperror.New(apperror.KindInternal, "reload", nil, nil)
		},
	})
	done = watcher.Start(ctx, time.Millisecond)
	<-done
}
