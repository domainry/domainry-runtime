package appschema

import (
	"context"
	"encoding/json"
	"strings"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	metadatadomain "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordprojection "github.com/domainry/domainry-runtime/runtime/domain/record/projection"
)

func (s *ApplicationSchemaApplicationService) CurrentManifest(ctx context.Context, principal principalmodel.Principal) (manifestmodel.ManifestSchema, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return manifestmodel.ManifestSchema{}, forbidden("auth.permission_denied")
	}
	manifest, err := s.repository.LoadManifest(ctx, metadataInstallationScope("load current Runtime manifest for global validation"))
	return manifest, wrapMetadataError(err)
}

type ApplicationSchemaQueryApplicationService struct {
	*metadatadomain.ApplicationSchemaDomainService
}

type ApplicationSchemaProvider interface {
	SchemaForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot
}

func NewApplicationSchemaQueryApplicationService(schema ApplicationSchemaProvider, repository appschemarepository.ApplicationSchemaRepository) *ApplicationSchemaQueryApplicationService {
	return &ApplicationSchemaQueryApplicationService{ApplicationSchemaDomainService: metadatadomain.NewApplicationSchemaDomainService(schema, repository)}
}

func (s *ApplicationSchemaQueryApplicationService) FeaturePermissions(ctx context.Context, principal principalmodel.Principal) (recordcontract.RecordFeaturePermissionSnapshot, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return recordcontract.RecordFeaturePermissionSnapshot{}, err
	}
	snapshot := s.Snapshot(ctx)
	return recordprojection.RecordBuildFeaturePermissions(snapshot.Objects, snapshot.Actions, principal)
}

func (s *ApplicationSchemaApplicationService) ReloadApplicationSchema(ctx context.Context, principal principalmodel.Principal) (appschemamodel.ApplicationSchemaSnapshot, error) {
	if err := metadataAuthorizeCommand(principal); err != nil {
		return appschemamodel.ApplicationSchemaSnapshot{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return appschemamodel.ApplicationSchemaSnapshot{}, forbidden("auth.permission_denied")
	}
	manifest, err := s.repository.LoadManifest(ctx, metadataInstallationScope("reload metadata manifest"))
	if err != nil {
		return appschemamodel.ApplicationSchemaSnapshot{}, err
	}
	if err := s.repository.SyncManifest(ctx, metadataInstallationScope("synchronize metadata manifest"), manifest); err != nil {
		return appschemamodel.ApplicationSchemaSnapshot{}, wrapMetadataError(err)
	}
	s.runtime.ApplyManifestMetadata(valueOrDefault(manifest.TemplateID, s.templateID), valueOrDefault(manifest.Version, s.version), valueOrDefault(manifest.Name, s.name), manifest.Objects, manifest.Views, manifest.Actions, manifest.Workflows, manifest.AutomationRules, manifest.Dictionaries, manifest.Integrations, manifest.Reports, manifest.EntryPoints, manifest.Skills, manifest.Agents, manifest.IdentityProfileExtensions)
	applyManifestAgentMetadata(s.runtime, manifest)
	workflowScope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "reload published workflow definitions")
	if err := s.workflows.InitializePublishedWorkflowDefinitions(ctx, manifest.Workflows, workflowScope); err != nil {
		return appschemamodel.ApplicationSchemaSnapshot{}, err
	}
	snapshot := s.runtime.Schema()
	s.notifyReloadObservers(snapshot)
	return snapshot, nil
}

func (s *ApplicationSchemaApplicationService) ApplicationSchemaMigrationPlan(ctx context.Context, principal principalmodel.Principal) ([]appschemamodel.ApplicationSchemaMigrationStep, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	if !principal.HasPermission("workspace.admin") {
		return nil, forbidden("auth.permission_denied")
	}
	manifest, err := s.repository.LoadManifest(ctx, metadataInstallationScope("load metadata migration manifest"))
	if err != nil {
		return nil, err
	}
	steps, err := s.repository.MigrationPlan(ctx, metadataInstallationScope("plan metadata migration"), manifest)
	return steps, wrapMetadataError(err)
}

// ApplicationSchemaObjectRecordCount exposes only the aggregate needed to validate
// Schema changes. It deliberately bypasses business record data scopes so a
// metadata administrator never has to receive record payloads merely to decide
// whether a required field needs a default value.
func (s *ApplicationSchemaApplicationService) ApplicationSchemaObjectRecordCount(ctx context.Context, objectKey string, principal principalmodel.Principal) (int, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return 0, err
	}
	if !principal.HasPermission("metadata.read") {
		return 0, forbidden("auth.permission_denied")
	}
	objectKey = strings.TrimSpace(objectKey)
	var objectFound bool
	var object definitionmodel.ObjectSchema
	for _, candidate := range s.runtime.Schema().Objects {
		if candidate.Key == objectKey {
			object, objectFound = candidate, true
			break
		}
	}
	if !objectFound {
		return 0, notFound("backend.metadata.object_not_found", "object", objectKey)
	}
	if s.records == nil {
		return 0, metadataInternalError("count metadata object records")
	}
	page, err := s.records.ListRecords(ctx, principalmodel.InstallationWorkspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 1})
	if err != nil {
		return 0, wrapMetadataError(err)
	}
	return page.Total, nil
}

func (s *ApplicationSchemaApplicationService) ListApplicationDefinitions(ctx context.Context, resourceType, workspaceID string, principal principalmodel.Principal) ([]appschemamodel.ApplicationDefinition, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID != strings.TrimSpace(principal.WorkspaceID) || !principal.HasPermission("workspace.admin") {
		return nil, forbidden("auth.permission_denied")
	}
	definitions, err := s.repository.ListDefinitions(ctx, metadataInstallationScope("list metadata definitions"), resourceType)
	if err != nil {
		return nil, wrapMetadataError(err)
	}
	return s.withEffectiveActionDefinitions(resourceType, definitions), nil
}

func (s *ApplicationSchemaApplicationService) GetApplicationDefinition(ctx context.Context, resourceType, resourceKey string, principal principalmodel.Principal) (appschemamodel.ApplicationDefinition, bool, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return appschemamodel.ApplicationDefinition{}, false, err
	}
	if !principal.HasPermission("workspace.admin") {
		return appschemamodel.ApplicationDefinition{}, false, forbidden("auth.permission_denied")
	}
	definition, found, err := s.repository.GetDefinition(ctx, metadataInstallationScope("get metadata definition"), resourceType, resourceKey)
	if err != nil || !found {
		return definition, found, wrapMetadataError(err)
	}
	definition = s.withEffectiveActionDefinitions(resourceType, []appschemamodel.ApplicationDefinition{definition})[0]
	return definition, true, nil
}

func (s *ApplicationSchemaApplicationService) withEffectiveActionDefinitions(resourceType string, definitions []appschemamodel.ApplicationDefinition) []appschemamodel.ApplicationDefinition {
	if strings.TrimSpace(resourceType) != "action" || s.actionDefinitions == nil || len(definitions) == 0 {
		return definitions
	}
	effective := map[string]definitionmodel.ActionSchema{}
	for _, action := range s.actionDefinitions() {
		if key := strings.TrimSpace(action.Key); key != "" {
			effective[key] = action
		}
	}
	result := append([]appschemamodel.ApplicationDefinition(nil), definitions...)
	for index := range result {
		action, ok := effective[strings.TrimSpace(result[index].ResourceKey)]
		if !ok {
			continue
		}
		payload, err := json.Marshal(action)
		if err == nil {
			result[index].Payload = payload
		}
	}
	return result
}

func (s *ApplicationSchemaApplicationService) ListApplicationDefinitionVersions(ctx context.Context, resourceType, resourceKey string, principal principalmodel.Principal) ([]appschemamodel.ApplicationDefinitionVersion, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	if !principal.HasPermission("workspace.admin") {
		return nil, forbidden("auth.permission_denied")
	}
	versions, err := s.repository.ListDefinitionVersions(ctx, metadataInstallationScope("list metadata definition versions"), resourceType, resourceKey)
	return versions, wrapMetadataError(err)
}

func (s *ApplicationSchemaApplicationService) ListLocalizedTexts(ctx context.Context, query appschemamodel.LocalizedTextQuery, principal principalmodel.Principal) ([]appschemamodel.LocalizedText, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query.WorkspaceID) != strings.TrimSpace(principal.WorkspaceID) || !principal.HasPermission("workspace.admin") {
		return nil, forbidden("auth.permission_denied")
	}
	values, err := s.repository.ListLocalizedTexts(ctx, query.WorkspaceID, query)
	return values, wrapMetadataError(err)
}

func (s *ApplicationSchemaApplicationService) LocalizedTextsForLocale(ctx context.Context, workspaceID, locale string) ([]appschemamodel.LocalizedText, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if err := metadataAuthorizeWorkspaceQuery(workspaceID); err != nil {
		return nil, err
	}
	locale = strings.TrimSpace(locale)
	if locale == "" {
		return nil, nil
	}
	values, err := s.repository.ListLocalizedTexts(ctx, workspaceID, appschemamodel.LocalizedTextQuery{WorkspaceID: workspaceID, Locale: locale})
	return values, wrapMetadataError(err)
}

func (s *ApplicationSchemaApplicationService) UpsertLocalizedText(ctx context.Context, req appschemamodel.LocalizedTextUpsertRequest, principal principalmodel.Principal) (appschemamodel.LocalizedText, error) {
	if err := metadataAuthorizeCommand(principal); err != nil {
		return appschemamodel.LocalizedText{}, err
	}
	if strings.TrimSpace(req.WorkspaceID) != strings.TrimSpace(principal.WorkspaceID) || !principal.HasPermission("workspace.admin") {
		return appschemamodel.LocalizedText{}, forbidden("auth.permission_denied")
	}
	value, err := s.repository.UpsertLocalizedText(ctx, req.WorkspaceID, req)
	if err != nil {
		return value, wrapMetadataError(err)
	}
	s.dictionary.Invalidate()
	return value, nil
}

func (s *ApplicationSchemaApplicationService) DictionaryItems(ctx context.Context, dictionaryKey, locale string, principal principalmodel.Principal) (appschemamodel.DictionaryItemsResult, bool, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return appschemamodel.DictionaryItemsResult{}, false, err
	}
	return s.dictionary.Items(ctx, s.repository, dictionaryKey, locale, principal)
}
