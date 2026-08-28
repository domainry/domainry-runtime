package projection

import integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

func IntegrationEntrypointPayload(values map[string]any, resolved integrationmodel.IntegrationExternalIdentityResolveResult) map[string]any {
	out := integrationCloneMap(values)
	if out == nil {
		out = map[string]any{}
	}
	out["external_principal"] = resolved.ExternalPrincipal
	out["external_provider"] = resolved.Provider
	out["external_subject"] = resolved.ExternalSubject
	out["external_subject_type"] = resolved.ExternalSubjectType
	out["external_actor_id"] = resolved.ActorID
	out["external_role_key"] = resolved.RoleKey
	if resolved.ExternalName != "" {
		out["external_name"] = resolved.ExternalName
	}
	if resolved.ExternalOrganization != "" {
		out["external_organization"] = resolved.ExternalOrganization
	}
	if resolved.ExternalDepartment != "" {
		out["external_department"] = resolved.ExternalDepartment
	}
	if resolved.ExternalGroup != "" {
		out["external_group"] = resolved.ExternalGroup
	}
	if resolved.ExternalBotID != "" {
		out["external_bot_id"] = resolved.ExternalBotID
	}
	return out
}

func IntegrationEntrypointAuditMetadata(resolved integrationmodel.IntegrationExternalIdentityResolveResult, extra map[string]any) map[string]any {
	out := integrationCloneMap(extra)
	if out == nil {
		out = map[string]any{}
	}
	out["provider"] = resolved.Provider
	out["external_subject_type"] = resolved.ExternalSubjectType
	out["external_principal"] = resolved.ExternalPrincipal
	out["actor_id"] = resolved.ActorID
	out["role_key"] = resolved.RoleKey
	out["mapping_key"] = resolved.MappingKey
	out["external_mapped"] = resolved.Mapped
	return out
}

func integrationCloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}
