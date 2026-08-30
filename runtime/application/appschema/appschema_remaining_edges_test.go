package appschema

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func TestMetadataRuntimeRestorationAuthorizationAndPreparation(t *testing.T) {
	prepared := PrepareInstalledManifest(manifestmodel.ManifestSchema{TemplateID: "template"})
	if prepared.TemplateID != "template" {
		t.Fatalf("prepared=%+v", prepared)
	}
	service := NewApplicationSchemaRuntimeRestorationApplicationService(runtimeNotificationRepositoryStub{}, runtimeMetadataRepositoryStub{})
	if _, err := service.Restore(t.Context(), manifestmodel.ManifestSchema{}, principalmodel.SystemScope{}); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("invalid scope error=%v", err)
	}
	wrong := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "wrong restoration scope")
	if _, err := service.Restore(t.Context(), manifestmodel.ManifestSchema{}, wrong); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("wrong scope error=%v", err)
	}
}

func TestMetadataLocalizedTextCoverageBoundaries(t *testing.T) {
	admin := metadataSchemaAdmin()
	nonAdmin := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: admin.WorkspaceID, UserID: admin.UserID}}
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: &metadataSchemaEdgeRepository{}, Runtime: &upsertMetadataRuntime{}})
	if _, err := service.LocalizedTextCoverage(t.Context(), "en-US", "", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error=%v", err)
	}
	if _, err := service.LocalizedTextCoverage(t.Context(), "en-US", "", nonAdmin); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error=%v", err)
	}
	if _, err := service.LocalizedTextCoverage(t.Context(), " ", "", admin); apperror.CodeOf(err) != "metadata.localized_texts.locale_required" {
		t.Fatalf("locale error=%v", err)
	}
	result, err := service.LocalizedTextCoverage(t.Context(), "en-US", " en-US ", admin)
	if err != nil || result.Locale != "en-US" || result.FallbackLocale != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	edgeErr := errors.New("localized texts failed")
	service.repository = &metadataSchemaEdgeRepository{err: edgeErr}
	if _, err := service.LocalizedTextCoverage(t.Context(), "en-US", "fr-FR", admin); !errors.Is(err, edgeErr) {
		t.Fatalf("repository error=%v", err)
	}
}

func TestMetadataValidationSupportReportAndAutomation(t *testing.T) {
	snapshot := appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}}}
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Runtime: &upsertMetadataRuntime{snapshot: snapshot}})
	report := reportmodel.ReportSchema{Key: "orders", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "order", Alias: "order"}}}
	_ = service.validateReportDefinitionIssues(t.Context(), report)
	_ = validateReportDefinitionForSnapshot(t.Context(), snapshot, nil, report)
	if err := service.ValidateAutomationRuleDefinition(t.Context(), automationmodel.AutomationRuleSchema{}); err == nil {
		t.Fatal("empty automation rule unexpectedly valid")
	}
}

func TestMetadataSnapshotWatcherInvalidAndNilDependenciesStop(t *testing.T) {
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{})
	select {
	case <-service.StartSnapshotWatcher(t.Context(), 0, principalmodel.SystemScope{}):
	default:
		t.Fatal("invalid-scope watcher did not return stopped channel")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := service.StartSnapshotWatcher(ctx, -1, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "nil watcher dependencies"))
	cancel()
	<-done
}

type metadataRequiredFieldRecordRepository struct {
	recordrepository.RecordRepository
	calls int
	err   error
}

type metadataIntegrationValidationRepository struct {
	integrationrepository.IntegrationConfigRepository
	calls int
}

func (r *metadataIntegrationValidationRepository) ListConnections(context.Context, string) ([]integrationmodel.IntegrationConnection, error) {
	r.calls++
	return nil, nil
}

func (r *metadataRequiredFieldRecordRepository) ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	r.calls++
	return recordmodel.RecordPageResult{Items: []recordmodel.Record{{Data: map[string]any{"required": "value"}}}, Total: 1}, r.err
}

func TestMetadataRemainingAuthorizedAndSuccessfulValidationPaths(t *testing.T) {
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Repository: &metadataSchemaEdgeRepository{}, Dictionary: &metadataDictionaryEdgeRuntime{}})
	for name, call := range map[string]func() error{
		"versions": func() error {
			_, err := service.ListApplicationDefinitionVersions(t.Context(), "object", "order", principalmodel.Principal{})
			return err
		},
		"texts": func() error {
			_, err := service.ListLocalizedTexts(t.Context(), appschemamodel.LocalizedTextQuery{}, principalmodel.Principal{})
			return err
		},
		"upsert": func() error {
			_, err := service.UpsertLocalizedText(t.Context(), appschemamodel.LocalizedTextUpsertRequest{}, principalmodel.Principal{})
			return err
		},
		"dictionary": func() error {
			_, _, err := service.DictionaryItems(t.Context(), "status", "en-US", principalmodel.Principal{})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); apperror.CodeOf(err) != "backend.workspace_scope_required" {
				t.Fatalf("error=%v", err)
			}
		})
	}

	operation := integrationmodel.ConnectorOperationSchema{Key: "read", Method: "GET", ExecutionMode: "sync", SideEffect: "read", TimeoutDefaultSeconds: 1, TimeoutMaxSeconds: 2, Input: []definitionmodel.FieldSchema{{Key: "id", Type: "text"}}, Output: []definitionmodel.FieldSchema{{Key: "result", Type: "json"}}}
	connector := integrationmodel.ConnectorSchema{Key: "api", Type: "http", Provider: "api", Operations: []integrationmodel.ConnectorOperationSchema{operation}}
	payload, _ := json.Marshal(connector)
	validationService := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Runtime: &upsertMetadataRuntime{}})
	if normalized, issues, err := validationService.ValidateApplicationDefinitionRequestPayload(t.Context(), "connector", "api", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: payload}); err != nil || len(issues) != 0 || len(normalized) == 0 {
		t.Fatalf("normalized=%s issues=%v err=%v", normalized, issues, err)
	}
	if _, err := validationService.ValidateApplicationDefinitionPayload(t.Context(), "connector", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: payload}); err != nil {
		t.Fatal(err)
	}
	actionPayload, _ := json.Marshal(definitionmodel.ActionSchema{Key: "order.submit", ObjectKey: "order", Kind: "record_operation", RequiresPermission: "order.update", AuditEvent: "order_submitted"})
	if normalized, issues, err := validationService.ValidateApplicationDefinitionRequestPayload(t.Context(), "action", "order.submit", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: actionPayload}); err != nil || len(issues) != 0 || len(normalized) == 0 {
		t.Fatalf("metadata-only action normalized=%s issues=%v err=%v", normalized, issues, err)
	}
}

func TestMetadataSnapshotWatcherRevisionAndReloadFailuresRecover(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var mu sync.Mutex
	revisionCalls, reloadCalls := 0, 0
	watcher := newApplicationSchemaSnapshotWatcherApplicationService(ApplicationSchemaSnapshotWatcherDependencies{
		Revision: func(context.Context) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			revisionCalls++
			switch revisionCalls {
			case 1:
				return "", errors.New("revision failed")
			case 2:
				return "", nil
			case 3, 4:
				return "r1", nil
			default:
				return "r2", nil
			}
		},
		Reload: func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			reloadCalls++
			if reloadCalls == 1 {
				return errors.New("reload failed")
			}
			return nil
		},
	})
	done := watcher.Start(ctx, time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		calls := reloadCalls
		mu.Unlock()
		if calls >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watcher did not recover: revision=%d reload=%d", revisionCalls, calls)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
}

func TestMetadataSnapshotWatcherNilDependencyTick(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := newApplicationSchemaSnapshotWatcherApplicationService(ApplicationSchemaSnapshotWatcherDependencies{}).Start(ctx, time.Millisecond)
	time.Sleep(3 * time.Millisecond)
	cancel()
	<-done
}
