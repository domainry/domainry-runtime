package integration

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (s *IntegrationApplicationService) DisableIntegrationExternalIdentity(ctx context.Context, identityKey string, principal principalmodel.Principal) (integrationmodel.IntegrationExternalIdentity, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationExternalIdentity{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return integrationmodel.IntegrationExternalIdentity{}, forbidden("auth.permission_denied")
	}
	workspaceID := principalWorkspaceID(principal)
	identity, ok, err := s.findExternalIdentity(ctx, identityKey, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationExternalIdentity{}, err
	}
	if !ok {
		return integrationmodel.IntegrationExternalIdentity{}, notFound("backend.integration.external_identity.not_found")
	}
	before := externalIdentityAuditShape(identity)
	identity.Status, identity.DisabledAt = "disabled", time.Now().UTC().Format(time.RFC3339)
	saved, err := s.configRepo.UpsertExternalIdentity(ctx, identity.WorkspaceID, identity)
	if err != nil {
		return integrationmodel.IntegrationExternalIdentity{}, err
	}
	s.audit(ctx, "integration_external_identity_disabled", "integration_external_identity", saved.Key, principal, "Disabled integration external identity "+saved.Key, before, externalIdentityAuditShape(saved), map[string]any{
		"workspace_id": workspaceID, "provider": saved.Provider, "external_subject_type": saved.ExternalSubjectType,
		"external_principal": externalPrincipal(saved.Provider, saved.ExternalSubjectType, saved.ExternalSubject),
		"actor_id":           saved.ActorID, "role_key": saved.RoleKey,
	})
	return saved, nil
}

func (s *IntegrationApplicationService) UpsertIntegrationExternalIdentity(ctx context.Context, identityKey string, req integrationmodel.IntegrationExternalIdentityUpsertRequest, principal principalmodel.Principal) (integrationmodel.IntegrationExternalIdentity, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationExternalIdentity{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return integrationmodel.IntegrationExternalIdentity{}, forbidden("auth.permission_denied")
	}
	provider := strings.TrimSpace(req.Provider)
	subject := strings.TrimSpace(req.ExternalSubject)
	subjectType, err := normalizeExternalSubjectType(req.ExternalSubjectType)
	if err != nil {
		return integrationmodel.IntegrationExternalIdentity{}, err
	}
	actorID, roleKey := strings.TrimSpace(req.ActorID), strings.TrimSpace(req.RoleKey)
	if provider == "" || subject == "" {
		return integrationmodel.IntegrationExternalIdentity{}, badRequest("backend.integration.external_identity.missing_subject")
	}
	if actorID == "" {
		return integrationmodel.IntegrationExternalIdentity{}, badRequest("backend.integration.external_identity.missing_actor")
	}
	if roleKey == "" {
		return integrationmodel.IntegrationExternalIdentity{}, badRequest("backend.integration.external_identity.missing_role")
	}
	if !s.resolvePrincipal(ctx, actorID, roleKey, "").Known {
		return integrationmodel.IntegrationExternalIdentity{}, badRequest("backend.integration.external_identity.unknown_role")
	}
	status, err := normalizeExternalIdentityStatus(req.Status)
	if err != nil {
		return integrationmodel.IntegrationExternalIdentity{}, err
	}
	key := strings.TrimSpace(identityKey)
	if key == "" {
		key = strings.TrimSpace(req.Key)
	}
	if key == "" {
		key = sanitizeKey(provider + "_" + subjectType + "_" + subject)
	}
	workspaceID := principalWorkspaceID(principal)
	identity := integrationmodel.IntegrationExternalIdentity{
		Key: key, WorkspaceID: workspaceID, Provider: provider, ExternalSubject: subject,
		ExternalSubjectType: subjectType, ExternalName: strings.TrimSpace(req.ExternalName),
		ExternalOrganization: strings.TrimSpace(req.ExternalOrganization), ExternalDepartment: strings.TrimSpace(req.ExternalDepartment),
		ExternalGroup: strings.TrimSpace(req.ExternalGroup), ExternalBotID: strings.TrimSpace(req.ExternalBotID),
		ActorID: actorID, RoleKey: roleKey, Status: status, CreatedBy: strings.TrimSpace(principal.UserID),
	}
	existing, existed, err := s.findExternalIdentity(ctx, key, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationExternalIdentity{}, err
	}
	saved, err := s.configRepo.UpsertExternalIdentity(ctx, identity.WorkspaceID, identity)
	if err != nil {
		return integrationmodel.IntegrationExternalIdentity{}, err
	}
	var before map[string]any
	if existed {
		before = externalIdentityAuditShape(existing)
	}
	s.audit(ctx, "integration_external_identity_upserted", "integration_external_identity", saved.Key, principal, "Upserted integration external identity "+saved.Key, before, externalIdentityAuditShape(saved), map[string]any{
		"workspace_id": workspaceID, "provider": saved.Provider, "external_subject_type": saved.ExternalSubjectType,
		"external_principal":    externalPrincipal(saved.Provider, saved.ExternalSubjectType, saved.ExternalSubject),
		"external_organization": saved.ExternalOrganization, "external_department": saved.ExternalDepartment,
		"external_group": saved.ExternalGroup, "external_bot_id_set": strings.TrimSpace(saved.ExternalBotID) != "",
		"actor_id": saved.ActorID, "role_key": saved.RoleKey,
	})
	return saved, nil
}

func (s *IntegrationApplicationService) findExternalIdentity(ctx context.Context, key, workspaceID string) (integrationmodel.IntegrationExternalIdentity, bool, error) {
	identities, err := s.configRepo.ListExternalIdentities(ctx, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationExternalIdentity{}, false, err
	}
	key = strings.TrimSpace(key)
	for _, identity := range identities {
		if identity.Key == key {
			return identity, true, nil
		}
	}
	return integrationmodel.IntegrationExternalIdentity{}, false, nil
}

func externalPrincipal(provider, subjectType, subject string) string {
	provider, subjectType, subject = strings.TrimSpace(provider), strings.TrimSpace(subjectType), strings.TrimSpace(subject)
	if subjectType == "" {
		subjectType = "user"
	}
	if provider == "" {
		return subject
	}
	if subject == "" {
		return provider
	}
	if subjectType != "user" {
		return provider + ":" + subjectType + ":" + subject
	}
	return provider + ":" + subject
}

func normalizeExternalSubjectType(value string) (string, error) {
	switch value = strings.TrimSpace(value); value {
	case "", "user":
		return "user", nil
	case "organization", "department", "group", "bot", "service_account", "device":
		return value, nil
	default:
		return "", badRequest("backend.integration.external_identity.invalid_subject_type")
	}
}

func normalizeExternalIdentityStatus(value string) (string, error) {
	switch value = strings.TrimSpace(value); value {
	case "":
		return "active", nil
	case "active", "disabled":
		return value, nil
	default:
		return "", badRequest("backend.integration.external_identity.invalid_status")
	}
}

func externalIdentityAuditShape(identity integrationmodel.IntegrationExternalIdentity) map[string]any {
	return map[string]any{
		"key": identity.Key, "workspace_id": identity.WorkspaceID, "provider": identity.Provider,
		"external_subject": identity.ExternalSubject, "external_subject_type": identity.ExternalSubjectType,
		"external_name": identity.ExternalName, "external_organization": identity.ExternalOrganization,
		"external_department": identity.ExternalDepartment, "external_group": identity.ExternalGroup,
		"external_bot_id_set": strings.TrimSpace(identity.ExternalBotID) != "", "actor_id": identity.ActorID,
		"role_key": identity.RoleKey, "status": identity.Status, "last_resolved_at": identity.LastResolvedAt,
		"created_at": identity.CreatedAt, "updated_at": identity.UpdatedAt, "disabled_at": identity.DisabledAt,
	}
}
