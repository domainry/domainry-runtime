package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type metadataSchemaEdgeRepository struct {
	metadatarepository.MetadataRepository
	err         error
	loadErr     error
	syncErr     error
	manifest    manifestmodel.ManifestSchema
	definitions []metadatamodel.MetadataDefinition
	versions    []metadatamodel.MetadataDefinitionVersion
	texts       []metadatamodel.LocalizedText
	upserted    metadatamodel.LocalizedText
	syncCalls   int
}

func (r *metadataSchemaEdgeRepository) LoadManifest(context.Context, principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	if r.loadErr != nil {
		return r.manifest, r.loadErr
	}
	return r.manifest, r.err
}
func (r *metadataSchemaEdgeRepository) SyncManifest(context.Context, principalmodel.SystemScope, manifestmodel.ManifestSchema) error {
	r.syncCalls++
	if r.syncErr != nil {
		return r.syncErr
	}
	return r.err
}
func (r *metadataSchemaEdgeRepository) MigrationPlan(context.Context, principalmodel.SystemScope, manifestmodel.ManifestSchema) ([]metadatamodel.MetadataMigrationStep, error) {
	return []metadatamodel.MetadataMigrationStep{{Operation: "create"}}, r.err
}
func (r *metadataSchemaEdgeRepository) ListDefinitions(context.Context, principalmodel.SystemScope, string) ([]metadatamodel.MetadataDefinition, error) {
	return r.definitions, r.err
}
func (r *metadataSchemaEdgeRepository) GetDefinition(context.Context, principalmodel.SystemScope, string, string) (metadatamodel.MetadataDefinition, bool, error) {
	if len(r.definitions) == 0 {
		return metadatamodel.MetadataDefinition{}, false, r.err
	}
	return r.definitions[0], true, r.err
}
func (r *metadataSchemaEdgeRepository) ListDefinitionVersions(context.Context, principalmodel.SystemScope, string, string) ([]metadatamodel.MetadataDefinitionVersion, error) {
	return r.versions, r.err
}
func (r *metadataSchemaEdgeRepository) ListLocalizedTexts(context.Context, string, metadatamodel.LocalizedTextQuery) ([]metadatamodel.LocalizedText, error) {
	return r.texts, r.err
}
func (r *metadataSchemaEdgeRepository) UpsertLocalizedText(context.Context, string, metadatamodel.LocalizedTextUpsertRequest) (metadatamodel.LocalizedText, error) {
	return r.upserted, r.err
}
func (r *metadataSchemaEdgeRepository) ApplyDefinitionMutations(context.Context, principalmodel.SystemScope, []metadatamodel.MetadataDefinitionMutation, []auditmodel.AuditEvent, *changeplanmodel.BusinessChangePlanPublication) ([]metadatamodel.MetadataDefinition, error) {
	return nil, r.err
}

type metadataDictionaryEdgeRuntime struct {
	invalidations int
	err           error
}

func (d *metadataDictionaryEdgeRuntime) Invalidate() { d.invalidations++ }
func (d *metadataDictionaryEdgeRuntime) Items(context.Context, metadatarepository.MetadataRepository, string, string, principalmodel.Principal) (metadatamodel.DictionaryItemsResult, bool, error) {
	return metadatamodel.DictionaryItemsResult{}, d.err == nil, d.err
}

func metadataSchemaAdmin() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "metadata.read"}})
}

func TestMetadataSchemaApplicationAuthorizedDelegation(t *testing.T) {
	repository := &metadataSchemaEdgeRepository{
		manifest:    manifestmodel.ManifestSchema{TemplateID: "template", Version: "1", Name: "Runtime"},
		definitions: []metadatamodel.MetadataDefinition{{ResourceType: "object", ResourceKey: "order"}},
		versions:    []metadatamodel.MetadataDefinitionVersion{{SchemaVersion: "1"}},
		texts:       []metadatamodel.LocalizedText{{WorkspaceID: "workspace-1", Locale: "en-US", Text: "Order"}},
		upserted:    metadatamodel.LocalizedText{WorkspaceID: "workspace-1", Locale: "en-US", Text: "Saved"},
	}
	runtime := &upsertMetadataRuntime{snapshot: metadatamodel.ApplicationSchemaSnapshot{Name: "Runtime", Objects: []definitionmodel.ObjectSchema{{Key: "order"}}}}
	dictionary := &metadataDictionaryEdgeRuntime{}
	records := &metadataRequiredFieldRecordRepository{}
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{Repository: repository, Runtime: runtime, Workflows: upsertWorkflowInitializer{}, Dictionary: dictionary, Records: records})
	admin := metadataSchemaAdmin()

	if snapshot, err := service.ReloadMetadata(t.Context(), admin); err != nil || snapshot.Name != "Runtime" || repository.syncCalls != 1 {
		t.Fatalf("snapshot=%+v sync=%d err=%v", snapshot, repository.syncCalls, err)
	}
	if steps, err := service.MetadataMigrationPlan(t.Context(), admin); err != nil || len(steps) != 1 {
		t.Fatalf("steps=%v err=%v", steps, err)
	}
	if count, err := service.MetadataObjectRecordCount(t.Context(), " order ", admin); err != nil || count != 1 || records.calls != 1 {
		t.Fatalf("count=%d calls=%d err=%v", count, records.calls, err)
	}
	if values, err := service.ListMetadataDefinitions(t.Context(), "object", " workspace-1 ", admin); err != nil || len(values) != 1 {
		t.Fatalf("definitions=%v err=%v", values, err)
	}
	if value, found, err := service.GetMetadataDefinition(t.Context(), "object", "order", admin); err != nil || !found || value.ResourceKey != "order" {
		t.Fatalf("definition=%+v found=%v err=%v", value, found, err)
	}
	if values, err := service.ListMetadataDefinitionVersions(t.Context(), "object", "order", admin); err != nil || len(values) != 1 {
		t.Fatalf("versions=%v err=%v", values, err)
	}
	query := metadatamodel.LocalizedTextQuery{WorkspaceID: "workspace-1", Locale: "en-US"}
	if values, err := service.ListLocalizedTexts(t.Context(), query, admin); err != nil || len(values) != 1 {
		t.Fatalf("texts=%v err=%v", values, err)
	}
	if values, err := service.LocalizedTextsForLocale(t.Context(), "workspace-1", " en-US "); err != nil || len(values) != 1 {
		t.Fatalf("locale texts=%v err=%v", values, err)
	}
	if values, err := service.LocalizedTextsForLocale(t.Context(), "workspace-1", " "); err != nil || values != nil {
		t.Fatalf("empty locale=%v err=%v", values, err)
	}
	request := metadatamodel.LocalizedTextUpsertRequest{WorkspaceID: "workspace-1", Locale: "en-US", Text: "Saved"}
	if value, err := service.UpsertLocalizedText(t.Context(), request, admin); err != nil || value.Text != "Saved" || dictionary.invalidations != 1 {
		t.Fatalf("value=%+v invalidations=%d err=%v", value, dictionary.invalidations, err)
	}
	if _, found, err := service.DictionaryItems(t.Context(), "status", "en-US", admin); err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

func TestMetadataSchemaApplicationProjectsEffectiveActionExecutionContract(t *testing.T) {
	repository := &metadataSchemaEdgeRepository{definitions: []metadatamodel.MetadataDefinition{{
		ResourceType: "action",
		ResourceKey:  "order.reserve",
		SourceKind:   "generated",
		SourceID:     "source-project",
		Payload:      json.RawMessage(`{"key":"order.reserve","object_key":"order"}`),
	}}}
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{Repository: repository})
	service.UseActionDefinitionSource(func() []definitionmodel.ActionSchema {
		return []definitionmodel.ActionSchema{{
			Key: "order.reserve", ObjectKey: "order",
			EffectSet: &definitionmodel.ActionEffectSet{
				Read:  []definitionmodel.ActionObjectEffect{{ObjectKey: "order", Operations: []string{"get_for_update"}}},
				Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "reservation", Operations: []string{"create"}}},
			},
		}}
	})

	values, err := service.ListMetadataDefinitions(t.Context(), "action", "workspace-1", metadataSchemaAdmin())
	if err != nil || len(values) != 1 {
		t.Fatalf("definitions=%#v error=%v", values, err)
	}
	var action definitionmodel.ActionSchema
	if err := json.Unmarshal(values[0].Payload, &action); err != nil || action.EffectSet == nil || len(action.EffectSet.Read) != 1 || len(action.EffectSet.Write) != 1 {
		t.Fatalf("effective action=%#v error=%v", action, err)
	}
	if values[0].SourceKind != "generated" || values[0].SourceID != "source-project" {
		t.Fatalf("source ownership changed: %#v", values[0])
	}

	value, found, err := service.GetMetadataDefinition(t.Context(), "action", "order.reserve", metadataSchemaAdmin())
	if err != nil || !found || !json.Valid(value.Payload) || string(value.Payload) == string(repository.definitions[0].Payload) {
		t.Fatalf("definition=%#v found=%v error=%v", value, found, err)
	}
}

func TestMetadataSchemaApplicationAuthorizationAndRepositoryErrors(t *testing.T) {
	edgeErr := errors.New("repository failed")
	repository := &metadataSchemaEdgeRepository{err: edgeErr}
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{Repository: repository, Runtime: &upsertMetadataRuntime{}, Workflows: upsertWorkflowInitializer{}, Dictionary: &metadataDictionaryEdgeRuntime{err: edgeErr}})
	admin := metadataSchemaAdmin()
	nonAdmin := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: admin.WorkspaceID, UserID: admin.UserID}}

	for name, call := range map[string]func() error{
		"reload":    func() error { _, err := service.ReloadMetadata(t.Context(), nonAdmin); return err },
		"migration": func() error { _, err := service.MetadataMigrationPlan(t.Context(), nonAdmin); return err },
		"record count": func() error {
			_, err := service.MetadataObjectRecordCount(t.Context(), "order", nonAdmin)
			return err
		},
		"definitions": func() error {
			_, err := service.ListMetadataDefinitions(t.Context(), "object", "other", admin)
			return err
		},
		"definition": func() error {
			_, _, err := service.GetMetadataDefinition(t.Context(), "object", "order", nonAdmin)
			return err
		},
		"versions": func() error {
			_, err := service.ListMetadataDefinitionVersions(t.Context(), "object", "order", nonAdmin)
			return err
		},
		"texts": func() error {
			_, err := service.ListLocalizedTexts(t.Context(), metadatamodel.LocalizedTextQuery{WorkspaceID: "other"}, admin)
			return err
		},
		"upsert text": func() error {
			_, err := service.UpsertLocalizedText(t.Context(), metadatamodel.LocalizedTextUpsertRequest{WorkspaceID: "other"}, admin)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); apperror.KindOf(err) != apperror.KindForbidden {
				t.Fatalf("error=%v", err)
			}
		})
	}
	for name, call := range map[string]func() error{
		"reload":    func() error { _, err := service.ReloadMetadata(t.Context(), admin); return err },
		"migration": func() error { _, err := service.MetadataMigrationPlan(t.Context(), admin); return err },
		"definitions": func() error {
			_, err := service.ListMetadataDefinitions(t.Context(), "object", "workspace-1", admin)
			return err
		},
		"definition": func() error {
			_, _, err := service.GetMetadataDefinition(t.Context(), "object", "order", admin)
			return err
		},
		"versions": func() error {
			_, err := service.ListMetadataDefinitionVersions(t.Context(), "object", "order", admin)
			return err
		},
		"texts": func() error {
			_, err := service.ListLocalizedTexts(t.Context(), metadatamodel.LocalizedTextQuery{WorkspaceID: "workspace-1"}, admin)
			return err
		},
		"locale texts": func() error {
			_, err := service.LocalizedTextsForLocale(t.Context(), "workspace-1", "en-US")
			return err
		},
		"upsert text": func() error {
			_, err := service.UpsertLocalizedText(t.Context(), metadatamodel.LocalizedTextUpsertRequest{WorkspaceID: "workspace-1"}, admin)
			return err
		},
		"dictionary": func() error { _, _, err := service.DictionaryItems(t.Context(), "status", "en-US", admin); return err },
	} {
		t.Run("repository "+name, func(t *testing.T) {
			if err := call(); !errors.Is(err, edgeErr) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, err := service.LocalizedTextsForLocale(t.Context(), "", "en-US"); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace error=%v", err)
	}
	if _, err := service.MetadataObjectRecordCount(t.Context(), "missing", admin); apperror.CodeOf(err) != "backend.metadata.object_not_found" {
		t.Fatalf("missing object error=%v", err)
	}
	nilRecordsService := NewApplicationSchemaService(ApplicationSchemaDependencies{
		Runtime: &upsertMetadataRuntime{snapshot: metadatamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "order"}}}},
	})
	if _, err := nilRecordsService.MetadataObjectRecordCount(t.Context(), "order", admin); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("nil records error=%v", err)
	}
}

func TestMetadataCurrentManifestAuthorizationAndRepositoryBoundaries(t *testing.T) {
	edgeErr := errors.New("manifest unavailable")
	admin := metadataSchemaAdmin()
	nonAdmin := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: admin.WorkspaceID, UserID: admin.UserID}}

	for name, principal := range map[string]principalmodel.Principal{
		"unknown principal": principalmodel.Principal{},
		"non admin":         nonAdmin,
	} {
		t.Run(name, func(t *testing.T) {
			service := NewApplicationSchemaService(ApplicationSchemaDependencies{Repository: &metadataSchemaEdgeRepository{}})
			if _, err := service.CurrentManifest(t.Context(), principal); apperror.KindOf(err) != apperror.KindForbidden {
				t.Fatalf("error=%v", err)
			}
		})
	}

	repository := &metadataSchemaEdgeRepository{
		manifest: manifestmodel.ManifestSchema{TemplateID: "template-1"},
		loadErr:  edgeErr,
	}
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{Repository: repository})
	if _, err := service.CurrentManifest(t.Context(), admin); !errors.Is(err, edgeErr) {
		t.Fatalf("repository error=%v", err)
	}
	repository.loadErr = nil
	manifest, err := service.CurrentManifest(t.Context(), admin)
	if err != nil || manifest.TemplateID != "template-1" {
		t.Fatalf("manifest=%+v error=%v", manifest, err)
	}
}

func TestMetadataReloadReportsManifestSyncFailure(t *testing.T) {
	edgeErr := errors.New("manifest sync failed")
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{
		Repository: &metadataSchemaEdgeRepository{
			manifest: manifestmodel.ManifestSchema{TemplateID: "template-1"},
			syncErr:  edgeErr,
		},
		Runtime:   &upsertMetadataRuntime{},
		Workflows: upsertWorkflowInitializer{},
	})
	if _, err := service.ReloadMetadata(t.Context(), metadataSchemaAdmin()); !errors.Is(err, edgeErr) {
		t.Fatalf("sync error=%v", err)
	}
}

func TestMetadataObjectRecordCountAuthorizationLookupAndRepositoryFailures(t *testing.T) {
	admin := metadataSchemaAdmin()
	if _, err := NewApplicationSchemaService(ApplicationSchemaDependencies{}).
		MetadataObjectRecordCount(t.Context(), "order", principalmodel.Principal{}); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("authorization error=%v", err)
	}

	edgeErr := errors.New("record count failed")
	records := &metadataRequiredFieldRecordRepository{err: edgeErr}
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{
		Runtime: &upsertMetadataRuntime{snapshot: metadatamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{
			{Key: "other"},
			{Key: "order"},
		}}},
		Records: records,
	})
	if _, err := service.MetadataObjectRecordCount(t.Context(), "order", admin); !errors.Is(err, edgeErr) || records.calls != 1 {
		t.Fatalf("record error=%v calls=%d", err, records.calls)
	}
}

func TestMetadataEffectiveActionProjectionPreservesUnmatchedAndUnserializableDefinitions(t *testing.T) {
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{})
	original := []metadatamodel.MetadataDefinition{{ResourceType: "action", ResourceKey: "original"}}
	if projected := service.withEffectiveActionDefinitions("action", original); len(projected) != 1 || projected[0].ResourceKey != "original" {
		t.Fatalf("nil-source projection=%#v", projected)
	}
	service.UseActionDefinitionSource(func() []definitionmodel.ActionSchema {
		return []definitionmodel.ActionSchema{
			{Key: " "},
			{Key: "invalid", Defaults: map[string]any{"unsupported": make(chan int)}},
		}
	})
	if projected := service.withEffectiveActionDefinitions("action", nil); projected != nil {
		t.Fatalf("empty projection=%#v", projected)
	}
	definitions := []metadatamodel.MetadataDefinition{
		{ResourceType: "action", ResourceKey: "unmatched", Payload: json.RawMessage(`{"original":"unmatched"}`)},
		{ResourceType: "action", ResourceKey: "invalid", Payload: json.RawMessage(`{"original":"invalid"}`)},
	}
	projected := service.withEffectiveActionDefinitions("action", definitions)
	if len(projected) != 2 ||
		string(projected[0].Payload) != string(definitions[0].Payload) ||
		string(projected[1].Payload) != string(definitions[1].Payload) {
		t.Fatalf("projected=%#v", projected)
	}
}

func TestMetadataGetDefinitionReportsCleanNotFound(t *testing.T) {
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{Repository: &metadataSchemaEdgeRepository{}})
	value, found, err := service.GetMetadataDefinition(t.Context(), "object", "missing", metadataSchemaAdmin())
	if err != nil || found || value.ResourceKey != "" {
		t.Fatalf("definition=%#v found=%v error=%v", value, found, err)
	}
}
