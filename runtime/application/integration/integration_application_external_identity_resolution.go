package integration

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (s *IntegrationApplicationService) ResolveIntegrationExternalIdentity(ctx context.Context, req integrationmodel.IntegrationExternalIdentityResolveRequest, principal principalmodel.Principal) (integrationmodel.IntegrationExternalIdentityResolveResult, principalmodel.Principal, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return integrationmodel.IntegrationExternalIdentityResolveResult{}, principalmodel.Principal{}, err
	}
	if !canInvokeEntrypoint(principal) {
		return integrationmodel.IntegrationExternalIdentityResolveResult{}, principalmodel.Principal{}, forbidden("auth.permission_denied")
	}
	workspaceID := principalWorkspaceID(principal)
	provider, subject := strings.TrimSpace(req.Provider), strings.TrimSpace(req.ExternalSubject)
	subjectType, err := normalizeExternalSubjectType(req.ExternalSubjectType)
	if err != nil {
		return integrationmodel.IntegrationExternalIdentityResolveResult{}, principalmodel.Principal{}, err
	}
	if provider == "" || subject == "" {
		return integrationmodel.IntegrationExternalIdentityResolveResult{}, principalmodel.Principal{}, badRequest("backend.integration.external_identity.missing_subject")
	}
	externalID := externalPrincipal(provider, subjectType, subject)
	identity, ok, err := s.findExternalIdentityBySubject(ctx, provider, subjectType, subject, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationExternalIdentityResolveResult{}, principalmodel.Principal{}, err
	}
	if !ok || identity.Status == "disabled" {
		onUnmapped, err := normalizeExternalIdentityUnmappedPolicy(req.OnUnmapped)
		if err != nil {
			return integrationmodel.IntegrationExternalIdentityResolveResult{}, principalmodel.Principal{}, err
		}
		result := integrationmodel.IntegrationExternalIdentityResolveResult{
			Mapped: false, Provider: provider, ExternalSubject: subject, ExternalSubjectType: subjectType,
			ExternalName: strings.TrimSpace(req.ExternalName), ExternalOrganization: strings.TrimSpace(req.ExternalOrganization),
			ExternalDepartment: strings.TrimSpace(req.ExternalDepartment), ExternalGroup: strings.TrimSpace(req.ExternalGroup),
			ExternalBotID: strings.TrimSpace(req.ExternalBotID), ExternalPrincipal: externalID, Status: "unmapped",
		}
		if onUnmapped == "read_only" {
			resolvedPrincipal := s.resolveUnmappedReadOnly(ctx, workspaceID, externalID)
			resolvedPrincipal.RequestID = principal.RequestID
			result.ActorID, result.RoleKey, result.Status = resolvedPrincipal.UserID, resolvedPrincipal.RoleKey, "unmapped_read_only"
			s.audit(ctx, "integration_external_identity_resolved_read_only", "integration_external_identity", "", resolvedPrincipal, "Resolved unmapped external identity as read-only "+externalID, nil, nil, externalIdentityResolutionMetadata(req, workspaceID, provider, subjectType, externalID, onUnmapped, false, result.Status, resolvedPrincipal))
			return result, resolvedPrincipal, nil
		}
		s.audit(ctx, "integration_external_identity_denied", "integration_external_identity", "", principal, "Denied unmapped external identity "+externalID, nil, nil, externalIdentityResolutionMetadata(req, workspaceID, provider, subjectType, externalID, onUnmapped, false, result.Status, principalmodel.Principal{}))
		return result, principalmodel.Principal{}, forbidden("backend.integration.external_identity.unmapped")
	}
	before := externalIdentityResolutionAuditShape(identity)
	identity.LastResolvedAt = time.Now().UTC().Format(time.RFC3339)
	if value := strings.TrimSpace(req.ExternalName); value != "" {
		identity.ExternalName = value
	}
	if value := strings.TrimSpace(req.ExternalOrganization); value != "" {
		identity.ExternalOrganization = value
	}
	if value := strings.TrimSpace(req.ExternalDepartment); value != "" {
		identity.ExternalDepartment = value
	}
	if value := strings.TrimSpace(req.ExternalGroup); value != "" {
		identity.ExternalGroup = value
	}
	if value := strings.TrimSpace(req.ExternalBotID); value != "" {
		identity.ExternalBotID = value
	}
	saved, err := s.configRepo.UpsertExternalIdentity(ctx, identity.WorkspaceID, identity)
	if err != nil {
		return integrationmodel.IntegrationExternalIdentityResolveResult{}, principalmodel.Principal{}, err
	}
	resolvedPrincipal := s.resolvePrincipal(ctx, saved.ActorID, saved.RoleKey, "")
	resolvedPrincipal.WorkspaceID, resolvedPrincipal.RequestID = workspaceID, principal.RequestID
	result := integrationmodel.IntegrationExternalIdentityResolveResult{
		Mapped: true, MappingKey: saved.Key, Provider: saved.Provider, ExternalSubject: saved.ExternalSubject,
		ExternalSubjectType: saved.ExternalSubjectType, ExternalName: saved.ExternalName,
		ExternalOrganization: saved.ExternalOrganization, ExternalDepartment: saved.ExternalDepartment,
		ExternalGroup: saved.ExternalGroup, ExternalBotID: saved.ExternalBotID, ActorID: saved.ActorID,
		RoleKey: saved.RoleKey, ExternalPrincipal: externalPrincipal(saved.Provider, saved.ExternalSubjectType, saved.ExternalSubject), Status: saved.Status,
	}
	s.audit(ctx, "integration_external_identity_resolved", "integration_external_identity", saved.Key, resolvedPrincipal, "Resolved integration external identity "+saved.Key, before, externalIdentityResolutionAuditShape(saved), map[string]any{
		"workspace_id": workspaceID, "provider": saved.Provider, "external_subject_type": saved.ExternalSubjectType,
		"external_principal": result.ExternalPrincipal, "external_name": saved.ExternalName,
		"external_organization": saved.ExternalOrganization, "external_department": saved.ExternalDepartment,
		"external_group": saved.ExternalGroup, "external_bot_id_set": strings.TrimSpace(saved.ExternalBotID) != "",
		"actor_id": saved.ActorID, "role_key": saved.RoleKey,
	})
	return result, resolvedPrincipal, nil
}

func canInvokeEntrypoint(principal principalmodel.Principal) bool {
	return principal.Known && (principal.HasPermission("workspace.admin") || principal.HasPermission("integration.entrypoint.invoke"))
}

func CanInvokeEntrypoint(principal principalmodel.Principal) bool {
	return canInvokeEntrypoint(principal)
}

func normalizeExternalIdentityUnmappedPolicy(value string) (string, error) {
	switch value = strings.TrimSpace(value); value {
	case "":
		return "reject", nil
	case "reject", "read_only":
		return value, nil
	default:
		return "", badRequest("backend.integration.external_identity.invalid_unmapped_policy")
	}
}

func externalIdentityResolutionMetadata(req integrationmodel.IntegrationExternalIdentityResolveRequest, workspaceID, provider, subjectType, externalID, onUnmapped string, mapped bool, status string, resolved principalmodel.Principal) map[string]any {
	metadata := map[string]any{
		"workspace_id": workspaceID, "provider": provider, "external_subject_type": subjectType,
		"external_principal": externalID, "external_name": strings.TrimSpace(req.ExternalName),
		"external_organization": strings.TrimSpace(req.ExternalOrganization), "external_department": strings.TrimSpace(req.ExternalDepartment),
		"external_group": strings.TrimSpace(req.ExternalGroup), "external_bot_id_set": strings.TrimSpace(req.ExternalBotID) != "",
		"on_unmapped": onUnmapped, "external_mapped": mapped, "external_status": status, "external_subject_set": true,
	}
	if resolved.UserID != "" {
		metadata["actor_id"], metadata["role_key"] = resolved.UserID, resolved.RoleKey
	}
	return metadata
}

func externalIdentityResolutionAuditShape(identity integrationmodel.IntegrationExternalIdentity) map[string]any {
	return map[string]any{
		"key": identity.Key, "workspace_id": identity.WorkspaceID, "provider": identity.Provider,
		"external_subject_type": valueOrDefault(strings.TrimSpace(identity.ExternalSubjectType), "user"),
		"external_principal":    externalPrincipal(identity.Provider, identity.ExternalSubjectType, identity.ExternalSubject),
		"external_name_set":     strings.TrimSpace(identity.ExternalName) != "", "external_organization": identity.ExternalOrganization,
		"external_department": identity.ExternalDepartment, "external_group": identity.ExternalGroup,
		"external_bot_id_set": strings.TrimSpace(identity.ExternalBotID) != "", "actor_id": identity.ActorID,
		"role_key": identity.RoleKey, "status": identity.Status,
	}
}
