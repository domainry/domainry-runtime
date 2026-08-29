package metadata

import (
	"context"
	"strings"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadataprojection "github.com/domainry/domainry-runtime/runtime/domain/metadata/projection"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *ApplicationSchemaService) LocalizedTextCoverage(ctx context.Context, locale string, fallbackLocale string, principal principalmodel.Principal) (metadatamodel.LocalizedTextCoverageResult, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return metadatamodel.LocalizedTextCoverageResult{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return metadatamodel.LocalizedTextCoverageResult{}, forbidden("auth.permission_denied")
	}
	workspaceID := strings.TrimSpace(principal.WorkspaceID)
	locale = strings.TrimSpace(locale)
	if locale == "" {
		return metadatamodel.LocalizedTextCoverageResult{}, badRequest("metadata.localized_texts.locale_required")
	}
	fallbackLocale = strings.TrimSpace(fallbackLocale)
	if fallbackLocale == locale {
		fallbackLocale = ""
	}
	values, err := s.repository.ListLocalizedTexts(ctx, workspaceID, metadatamodel.LocalizedTextQuery{WorkspaceID: workspaceID})
	if err != nil {
		return metadatamodel.LocalizedTextCoverageResult{}, wrapMetadataError(err)
	}
	return metadataprojection.MetadataLocalizedTextCoverage(workspaceID, locale, fallbackLocale, s.runtime.Schema(), values), nil
}
