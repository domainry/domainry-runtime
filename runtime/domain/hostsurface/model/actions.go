// Package model declares Runtime-host process Actions that are mounted through
// modulehttp adapters but remain owned by Runtime rather than an external
// source module.
package model

import (
	"net/http"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
)

func ModuleInventoryAction() actioncontract.ActionDefinition {
	return actioncontract.ActionDefinition{
		Key: "runtime.discovery.modules.list", Owner: "runtime:builtin", SourceKind: "builtin_http", CapabilityKey: "runtime.discovery.modules", CapabilityLabel: "Runtime modules",
		OperationKey: "list", OperationLabel: "List Runtime modules", Label: "List Runtime modules", Exposures: []actioncontract.Exposure{actioncontract.ExposureOps},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated},
		HTTP:          &actioncontract.HTTPBinding{Method: http.MethodGet, RouteTemplate: "/discovery/modules"},
		Permission:    &actioncontract.PermissionDefinition{Key: "runtime.discovery.modules.list", Owner: "runtime:builtin", ResourceKey: "runtime.discovery.modules", OperationKey: "list", Label: "List Runtime modules", Category: "Runtime", LifecycleStatus: actioncontract.LifecycleActive},
		EffectClass:   actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "runtime_module_inventory_read", LifecycleStatus: actioncontract.LifecycleActive,
	}
}

func PermissionUsageQueryAction(identityAudience string) actioncontract.ActionDefinition {
	audiences := []string{}
	if audience := strings.TrimSpace(identityAudience); audience != "" {
		audiences = append(audiences, audience)
	}
	return actioncontract.ActionDefinition{
		Key: "runtime.action.permission_usages.query", Owner: "runtime:builtin", SourceKind: "builtin_http", CapabilityKey: "runtime.action.permission_usages", CapabilityLabel: "Action permission usages",
		OperationKey: "action_usages.query", OperationLabel: "Query Action usages", Label: "Query live Action usages", Exposures: []actioncontract.Exposure{actioncontract.ExposureManagement, actioncontract.ExposureOps},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationSigned, PolicyKey: "runtime.action.permission_usages.query", Audiences: audiences},
		HTTP:          &actioncontract.HTTPBinding{Method: http.MethodPost, RouteTemplate: "/action/permission-usages/query"},
		EffectClass:   actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "runtime_action_usage_read", LifecycleStatus: actioncontract.LifecycleActive,
	}
}

func PublicResourceReadAction() actioncontract.ActionDefinition {
	return actioncontract.ActionDefinition{
		Key: "runtime.public_resources.read", Owner: "runtime:builtin", SourceKind: "builtin_http", CapabilityKey: "runtime.public_resources", CapabilityLabel: "Public resources",
		OperationKey: "read", OperationLabel: "Read public resource", Label: "Read public resource", Exposures: []actioncontract.Exposure{actioncontract.ExposurePublic},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAnonymous},
		HTTP:          &actioncontract.HTTPBinding{Method: http.MethodGet, RouteTemplate: "/public-resources/{resourceKey}/{accessKey}"},
		EffectClass:   actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "public_resource_read", LifecycleStatus: actioncontract.LifecycleActive,
	}
}

func PublicResourceFileReadAction() actioncontract.ActionDefinition {
	return actioncontract.ActionDefinition{
		Key: "runtime.public_resources.files.read", Owner: "runtime:builtin", SourceKind: "builtin_http", CapabilityKey: "runtime.public_resources.files", CapabilityLabel: "Public resource files",
		OperationKey: "read", OperationLabel: "Read public resource file", Label: "Read public resource file", Exposures: []actioncontract.Exposure{actioncontract.ExposurePublic},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAnonymous},
		HTTP:          &actioncontract.HTTPBinding{Method: http.MethodGet, RouteTemplate: "/public-resources/{resourceKey}/{accessKey}/files/{fieldKey}"},
		EffectClass:   actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "public_resource_file_read", LifecycleStatus: actioncontract.LifecycleActive,
	}
}
