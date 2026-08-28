package integration

import (
	"context"
	"strings"
	"time"

	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (s *IntegrationApplicationService) WritebackWebhookExternalIdentity(ctx context.Context, connection integrationmodel.IntegrationConnection, incoming *integrationcontract.WebhookExternalIdentity) (*integrationmodel.IntegrationExternalIdentity, error) {
	if err := integrationAuthorizeWorkspaceCommand(connection.WorkspaceID); err != nil {
		return nil, err
	}
	if incoming == nil || strings.TrimSpace(incoming.Subject) == "" {
		return nil, nil
	}
	mappings, _ := connection.Config["external_identity_mappings"].(map[string]any)
	mapping, _ := mappings[incoming.Subject].(map[string]any)
	if len(mapping) == 0 {
		return nil, nil
	}
	actorID, _ := mapping["actor_id"].(string)
	roleKey, _ := mapping["role_key"].(string)
	actorID, roleKey = strings.TrimSpace(actorID), strings.TrimSpace(roleKey)
	if actorID == "" || roleKey == "" || !integrationExternalRoleKeyAllowed(roleKey) || !s.resolvePrincipal(ctx, actorID, roleKey, "").Known {
		return nil, badRequest("backend.integration.webhook.external_identity_mapping_invalid", "provider", connection.ProviderKey, "external_subject", incoming.Subject)
	}
	subjectType := valueOrDefault(strings.TrimSpace(incoming.SubjectType), "user")
	existing, found, err := s.findExternalIdentityBySubject(ctx, connection.ProviderKey, subjectType, incoming.Subject, connection.WorkspaceID)
	if err != nil {
		return nil, err
	}
	key, createdAt := sanitizeKey(connection.ProviderKey+"_"+incoming.Subject), ""
	if found {
		key, createdAt = existing.Key, existing.CreatedAt
	}
	identity := integrationmodel.IntegrationExternalIdentity{
		Key: key, WorkspaceID: connection.WorkspaceID, Provider: connection.ProviderKey, ExternalSubject: incoming.Subject,
		ExternalSubjectType: subjectType, ExternalName: strings.TrimSpace(incoming.Name), ExternalGroup: strings.TrimSpace(incoming.Group),
		ActorID: actorID, RoleKey: roleKey, Status: "active", LastResolvedAt: time.Now().UTC().Format(time.RFC3339),
		CreatedBy: "integration:webhook:" + connection.ProviderKey, CreatedAt: createdAt,
	}
	saved, err := s.configRepo.UpsertExternalIdentity(ctx, identity.WorkspaceID, identity)
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

func (s *IntegrationApplicationService) findExternalIdentityBySubject(ctx context.Context, provider, subjectType, subject, workspaceID string) (integrationmodel.IntegrationExternalIdentity, bool, error) {
	if s.configRepo == nil {
		return integrationmodel.IntegrationExternalIdentity{}, false, nil
	}
	identities, err := s.configRepo.ListExternalIdentities(ctx, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationExternalIdentity{}, false, err
	}
	provider, subjectType, subject = strings.TrimSpace(provider), valueOrDefault(strings.TrimSpace(subjectType), "user"), strings.TrimSpace(subject)
	for _, identity := range identities {
		if strings.TrimSpace(identity.Provider) == provider && valueOrDefault(strings.TrimSpace(identity.ExternalSubjectType), "user") == subjectType && strings.TrimSpace(identity.ExternalSubject) == subject {
			return identity, true, nil
		}
	}
	return integrationmodel.IntegrationExternalIdentity{}, false, nil
}
