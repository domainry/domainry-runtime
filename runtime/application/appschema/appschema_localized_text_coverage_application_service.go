package appschema

import (
	"context"
	"strings"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemaprojection "github.com/domainry/domainry-runtime/runtime/domain/appschema/projection"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *ApplicationSchemaApplicationService) LocalizedTextCoverage(ctx context.Context, locale string, fallbackLocale string, principal principalmodel.Principal) (appschemamodel.LocalizedTextCoverageResult, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return appschemamodel.LocalizedTextCoverageResult{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return appschemamodel.LocalizedTextCoverageResult{}, forbidden("auth.permission_denied")
	}
	workspaceID := strings.TrimSpace(principal.WorkspaceID)
	locale = strings.TrimSpace(locale)
	if locale == "" {
		return appschemamodel.LocalizedTextCoverageResult{}, badRequest("metadata.localized_texts.locale_required")
	}
	fallbackLocale = strings.TrimSpace(fallbackLocale)
	if fallbackLocale == locale {
		fallbackLocale = ""
	}
	values, err := s.repository.ListLocalizedTexts(ctx, workspaceID, appschemamodel.LocalizedTextQuery{WorkspaceID: workspaceID})
	if err != nil {
		return appschemamodel.LocalizedTextCoverageResult{}, wrapMetadataError(err)
	}
	return appschemaprojection.ApplicationSchemaLocalizedTextCoverage(workspaceID, locale, fallbackLocale, s.runtime.Schema(), values), nil
}
