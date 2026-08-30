package changeplan

import changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *ChangePlanReferenceApplicationService) addFrontendCapabilityReferences(ctx context.Context, builder *changeplanprojection.ChangePlanReferenceGraphBuilder, principal principalmodel.Principal) error {
	if s.frontend == nil {
		return nil
	}
	snapshot, err := s.frontend.FrontendReferenceSnapshot(ctx, principal)
	if err != nil {
		return err
	}
	if snapshot.Manifest == nil {
		return nil
	}
	for _, entry := range snapshot.Manifest.Entries {
		builder.Node("frontend_support", entry.SupportKey, "", entry.SupportKey, "source_owned_frontend")
		builder.Node("frontend_route", entry.Route, "", entry.Route, "source_owned_frontend")
		builder.Node("frontend_feature", entry.FeatureModule, "", entry.FeatureModule, "source_owned_frontend")
		builder.Edge("frontend_route", entry.Route, "frontend_support", entry.SupportKey, "uses_support", "route")
		builder.Edge("frontend_feature", entry.FeatureModule, "frontend_support", entry.SupportKey, "implements_support", "feature_module")
		for index, capabilityKey := range entry.CapabilityKeys {
			builder.Edge("frontend_support", entry.SupportKey, "platform_capability", capabilityKey, "implements_capability", "capability_keys["+stringIndex(index)+"]")
		}
		for index, permission := range entry.RequiredPermissions {
			builder.Edge("frontend_support", entry.SupportKey, "permission", permission, "requires_permission", "required_permissions["+stringIndex(index)+"]")
		}
		for index, role := range entry.ActorRoles {
			builder.Edge("frontend_route", entry.Route, "role", role, "visible_to_role", "actor_roles["+stringIndex(index)+"]")
		}
		for index, objectKey := range entry.BusinessObjects {
			builder.Edge("frontend_route", entry.Route, "object", objectKey, "presents_object", "business_objects["+stringIndex(index)+"]")
		}
		for index, actionKey := range entry.ImplementedActions {
			builder.Edge("frontend_route", entry.Route, "action", actionKey, "exposes_action", "implemented_actions["+stringIndex(index)+"]")
		}
		for index, reportKey := range entry.ReportKeys {
			builder.Edge("frontend_route", entry.Route, "report", reportKey, "presents_report", "report_keys["+stringIndex(index)+"]")
		}
		for index, fieldKey := range entry.FieldKeys {
			builder.Edge("frontend_route", entry.Route, "field", fieldKey, "binds_field", "field_keys["+stringIndex(index)+"]")
		}
		for index, claim := range entry.AcceptanceClaims {
			builder.Edge("frontend_route", entry.Route, "acceptance_scenario", claim, "proves_acceptance", "acceptance_claims["+stringIndex(index)+"]")
		}
	}
	return nil
}
