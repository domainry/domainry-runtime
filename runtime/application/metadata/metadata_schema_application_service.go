package metadata

import (
	"context"
	"encoding/json"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	metadatadomain "github.com/domainry/domainry-runtime/runtime/domain/metadata/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordprojection "github.com/domainry/domainry-runtime/runtime/domain/record/projection"
)

func (s *MetadataApplicationService) CurrentManifest(ctx context.Context, principal principalmodel.Principal) (manifestmodel.ManifestSchema, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return manifestmodel.ManifestSchema{}, forbidden("auth.permission_denied")
	}
	manifest, err := s.repository.LoadManifest(ctx, metadataInstallationScope("load current Runtime manifest for global validation"))
	return manifest, wrapMetadataError(err)
}

type MetadataSchemaApplicationService struct {
	*metadatadomain.MetadataSchemaDomainService
}

type MetadataSchemaProvider interface {
	SchemaForPrincipal(context.Context, principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot
}

func NewMetadataSchemaApplicationService(schema MetadataSchemaProvider, repository metadatarepository.MetadataRepository) *MetadataSchemaApplicationService {
	return &MetadataSchemaApplicationService{MetadataSchemaDomainService: metadatadomain.NewMetadataSchemaDomainService(schema, repository)}
}

func (s *MetadataSchemaApplicationService) FeaturePermissions(ctx context.Context, principal principalmodel.Principal) (recordcontract.RecordFeaturePermissionSnapshot, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return recordcontract.RecordFeaturePermissionSnapshot{}, err
	}
	snapshot := s.Snapshot(ctx)
	return recordprojection.RecordBuildFeaturePermissions(snapshot.Objects, snapshot.Actions, principal)
}

func (s *MetadataApplicationService) ReloadMetadata(ctx context.Context, principal principalmodel.Principal) (metadatamodel.MetadataSchemaSnapshot, error) {
	if err := metadataAuthorizeCommand(principal); err != nil {
		return metadatamodel.MetadataSchemaSnapshot{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return metadatamodel.MetadataSchemaSnapshot{}, forbidden("auth.permission_denied")
	}
	manifest, err := s.repository.LoadManifest(ctx, metadataInstallationScope("reload metadata manifest"))
	if err != nil {
		return metadatamodel.MetadataSchemaSnapshot{}, err
	}
	if err := s.repository.SyncManifest(ctx, metadataInstallationScope("synchronize metadata manifest"), manifest); err != nil {
		return metadatamodel.MetadataSchemaSnapshot{}, wrapMetadataError(err)
	}
	s.runtime.ApplyManifestMetadata(valueOrDefault(manifest.TemplateID, s.templateID), valueOrDefault(manifest.Version, s.version), valueOrDefault(manifest.Name, s.name), manifest.Objects, manifest.Views, manifest.Actions, manifest.Workflows, manifest.AutomationRules, manifest.Dictionaries, manifest.Integrations, manifest.Reports, manifest.EntryPoints, manifest.Skills, manifest.Agents, manifest.IdentityProfileExtensions)
	applyManifestAgentMetadata(s.runtime, manifest)
	workflowScope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "reload published workflow definitions")
	if err := s.workflows.InitializePublishedWorkflowDefinitions(ctx, manifest.Workflows, workflowScope); err != nil {
		return metadatamodel.MetadataSchemaSnapshot{}, err
	}
	snapshot := s.runtime.Schema()
	s.notifyReloadObservers(snapshot)
	return snapshot, nil
}

func (s *MetadataApplicationService) MetadataMigrationPlan(ctx context.Context, principal principalmodel.Principal) ([]metadatamodel.MetadataMigrationStep, error) {
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

// MetadataObjectRecordCount exposes only the aggregate needed to validate
// Schema changes. It deliberately bypasses business record data scopes so a
// metadata administrator never has to receive record payloads merely to decide
// whether a required field needs a default value.
func (s *MetadataApplicationService) MetadataObjectRecordCount(ctx context.Context, objectKey string, principal principalmodel.Principal) (int, error) {
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

func (s *MetadataApplicationService) ListMetadataDefinitions(ctx context.Context, resourceType, workspaceID string, principal principalmodel.Principal) ([]metadatamodel.MetadataDefinition, error) {
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

func (s *MetadataApplicationService) GetMetadataDefinition(ctx context.Context, resourceType, resourceKey string, principal principalmodel.Principal) (metadatamodel.MetadataDefinition, bool, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return metadatamodel.MetadataDefinition{}, false, err
	}
	if !principal.HasPermission("workspace.admin") {
		return metadatamodel.MetadataDefinition{}, false, forbidden("auth.permission_denied")
	}
	definition, found, err := s.repository.GetDefinition(ctx, metadataInstallationScope("get metadata definition"), resourceType, resourceKey)
	if err != nil || !found {
		return definition, found, wrapMetadataError(err)
	}
	definition = s.withEffectiveActionDefinitions(resourceType, []metadatamodel.MetadataDefinition{definition})[0]
	return definition, true, nil
}

func (s *MetadataApplicationService) withEffectiveActionDefinitions(resourceType string, definitions []metadatamodel.MetadataDefinition) []metadatamodel.MetadataDefinition {
	if strings.TrimSpace(resourceType) != "action" || s.actionDefinitions == nil || len(definitions) == 0 {
		return definitions
	}
	effective := map[string]definitionmodel.ActionSchema{}
	for _, action := range s.actionDefinitions() {
		if key := strings.TrimSpace(action.Key); key != "" {
			effective[key] = action
		}
	}
	result := append([]metadatamodel.MetadataDefinition(nil), definitions...)
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

func (s *MetadataApplicationService) ListMetadataDefinitionVersions(ctx context.Context, resourceType, resourceKey string, principal principalmodel.Principal) ([]metadatamodel.MetadataDefinitionVersion, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	if !principal.HasPermission("workspace.admin") {
		return nil, forbidden("auth.permission_denied")
	}
	versions, err := s.repository.ListDefinitionVersions(ctx, metadataInstallationScope("list metadata definition versions"), resourceType, resourceKey)
	return versions, wrapMetadataError(err)
}

func (s *MetadataApplicationService) ListLocalizedTexts(ctx context.Context, query metadatamodel.LocalizedTextQuery, principal principalmodel.Principal) ([]metadatamodel.LocalizedText, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query.WorkspaceID) != strings.TrimSpace(principal.WorkspaceID) || !principal.HasPermission("workspace.admin") {
		return nil, forbidden("auth.permission_denied")
	}
	values, err := s.repository.ListLocalizedTexts(ctx, query.WorkspaceID, query)
	return values, wrapMetadataError(err)
}

func (s *MetadataApplicationService) LocalizedTextsForLocale(ctx context.Context, workspaceID, locale string) ([]metadatamodel.LocalizedText, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if err := metadataAuthorizeWorkspaceQuery(workspaceID); err != nil {
		return nil, err
	}
	locale = strings.TrimSpace(locale)
	if locale == "" {
		return nil, nil
	}
	values, err := s.repository.ListLocalizedTexts(ctx, workspaceID, metadatamodel.LocalizedTextQuery{WorkspaceID: workspaceID, Locale: locale})
	return values, wrapMetadataError(err)
}

func (s *MetadataApplicationService) UpsertLocalizedText(ctx context.Context, req metadatamodel.LocalizedTextUpsertRequest, principal principalmodel.Principal) (metadatamodel.LocalizedText, error) {
	if err := metadataAuthorizeCommand(principal); err != nil {
		return metadatamodel.LocalizedText{}, err
	}
	if strings.TrimSpace(req.WorkspaceID) != strings.TrimSpace(principal.WorkspaceID) || !principal.HasPermission("workspace.admin") {
		return metadatamodel.LocalizedText{}, forbidden("auth.permission_denied")
	}
	value, err := s.repository.UpsertLocalizedText(ctx, req.WorkspaceID, req)
	if err != nil {
		return value, wrapMetadataError(err)
	}
	s.dictionary.Invalidate()
	return value, nil
}

func (s *MetadataApplicationService) DictionaryItems(ctx context.Context, dictionaryKey, locale string, principal principalmodel.Principal) (metadatamodel.DictionaryItemsResult, bool, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return metadatamodel.DictionaryItemsResult{}, false, err
	}
	return s.dictionary.Items(ctx, s.repository, dictionaryKey, locale, principal)
}
