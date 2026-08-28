package openapi

import (
	"net/http"
	"strings"

	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
)

type openAPISurfaceMetadata struct {
	Surfaces  []surfacemodel.ProductSurface
	Audiences []string
}

func openAPIProductSurfaces(surfaces ...surfacemodel.ProductSurface) openAPISurfaceMetadata {
	audiences := make([]string, 0, len(surfaces))
	for _, surface := range surfaces {
		audience, ok := surface.RequiredAudience()
		if ok {
			audiences = append(audiences, string(audience))
		}
	}
	return openAPISurfaceMetadata{Surfaces: surfaces, Audiences: audiences}
}

func openAPIProtocolAudience(audiences ...string) openAPISurfaceMetadata {
	return openAPISurfaceMetadata{Surfaces: []surfacemodel.ProductSurface{}, Audiences: audiences}
}

func openAPISurfaceMetadataForTag(tag string) (openAPISurfaceMetadata, bool) {
	businessPortal := openAPIProductSurfaces(
		surfacemodel.ProductSurfaceBusinessWorkspace,
		surfacemodel.ProductSurfaceConsumerPortal,
	)
	allProductSurfaces := openAPIProductSurfaces(surfacemodel.ProductSurfaces()...)
	switch tag {
	case "Actions", "Files", "Objects", "Party", "Surfaces":
		return businessPortal, true
	case "Reports":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceBusinessWorkspace), true
	case "Automation", "Business maintenance", "Capabilities", "Dictionaries", "Identity", "Lifecycle", "Metadata", "Provision":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceAdminConsole), true
	case "Integrations":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceAdminConsole), true
	case "Audit":
		return openAPIProductSurfaces(
			surfacemodel.ProductSurfaceAdminConsole,
			surfacemodel.ProductSurfaceAdminConsole,
		), true
	case "Health", "Operations":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceAdminConsole), true
	case "Open API", "OpenAPI":
		return openAPIProductSurfaces(
			surfacemodel.ProductSurfaceAdminConsole,
			surfacemodel.ProductSurfaceAdminConsole,
		), true
	case "Schema":
		return openAPIProductSurfaces(
			surfacemodel.ProductSurfaceBusinessWorkspace,
			surfacemodel.ProductSurfaceAdminConsole,
			surfacemodel.ProductSurfaceConsumerPortal,
		), true
	case "Permissions", "Auth", "I18n":
		return allProductSurfaces, true
	case "Workflow", "Workflows":
		// Workflow participant, authoring, and recovery endpoints remain
		// explicitly multi-Surface until their P4 use cases are split.
		return openAPIProductSurfaces(
			surfacemodel.ProductSurfaceBusinessWorkspace,
			surfacemodel.ProductSurfaceAdminConsole,
			surfacemodel.ProductSurfaceAdminConsole,
		), true
	case "Workflow Business":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceBusinessWorkspace), true
	case "Workflow Administration":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceAdminConsole), true
	case "Workflow Operations":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceAdminConsole), true
	case "Audit Business":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceBusinessWorkspace), true
	case "Audit Administration":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceAdminConsole), true
	case "Audit Operations":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceAdminConsole), true
	case "Business Runtime Schema":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceBusinessWorkspace), true
	case "Portal Runtime Schema":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceConsumerPortal), true
	case "Metadata Administration":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceAdminConsole), true
	case "Metadata Operations":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceAdminConsole), true
	case "Scheduler":
		return openAPIProductSurfaces(
			surfacemodel.ProductSurfaceAdminConsole,
			surfacemodel.ProductSurfaceAdminConsole,
		), true
	case "Scheduler Administration":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceAdminConsole), true
	case "Scheduler Operations":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceAdminConsole), true
	case "Integration Entrypoints":
		return openAPIProtocolAudience("integration_principal"), true
	case "Integration Webhooks":
		return openAPIProtocolAudience("signed_webhook"), true
	case "Integration OAuth":
		return openAPIProtocolAudience("google_oauth_provider"), true
	case "Integration Administration":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceAdminConsole), true
	case "Integration Operations":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceAdminConsole), true
	case "Integration Business":
		return openAPIProductSurfaces(surfacemodel.ProductSurfaceBusinessWorkspace), true
	default:
		return openAPISurfaceMetadata{}, false
	}
}

func openAPISurfaceExtension(operationID string, metadata openAPISurfaceMetadata) map[string]any {
	surfaces := make([]string, 0, len(metadata.Surfaces))
	for _, surface := range metadata.Surfaces {
		surfaces = append(surfaces, string(surface))
	}
	return map[string]any{
		"contract_version":  surfacemodel.ContractVersion,
		"endpoint_identity": operationID,
		"target_surfaces":   surfaces,
		"actor_audiences":   append([]string(nil), metadata.Audiences...),
	}
}

func applyCompiledEndpointSurfaceContracts(paths map[string]any) {
	for path, rawPathItem := range paths {
		pathItem, ok := rawPathItem.(map[string]any)
		if !ok {
			continue
		}
		for method, rawOperation := range pathItem {
			upperMethod := strings.ToUpper(method)
			switch upperMethod {
			case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			default:
				continue
			}
			operation, ok := rawOperation.(map[string]any)
			if !ok {
				continue
			}
			contract, classified := surfacemodel.EndpointContracts[upperMethod+" "+path]
			if !classified {
				continue
			}
			operation["x-domainry-surface-contract"] = compiledEndpointSurfaceExtension(contract, operation["operationId"])
			applyHighRiskOperationHeaders(operation, contract)
		}
	}
}

func applyHighRiskOperationHeaders(operation map[string]any, contract surfacemodel.RuntimeEndpointContractV1) {
	if contract.HighRiskPolicy == surfacemodel.HighRiskActionNone {
		return
	}
	parameters, _ := operation["parameters"].([]map[string]any)
	parameters = upsertOpenAPIHeaderParameter(parameters, openAPIHeaderParameter("X-Operation-Reason", "Human-supplied auditable operator reason", true))
	switch contract.HighRiskPolicy {
	case surfacemodel.HighRiskActionConfirmRequired:
		confirmation := openAPIHeaderParameter("X-Operation-Confirmation", "Explicit confirmation required by the compiled endpoint contract", true)
		confirmation["schema"] = map[string]any{"type": "string", "enum": []string{"confirmed"}}
		parameters = upsertOpenAPIHeaderParameter(parameters, confirmation)
	case surfacemodel.HighRiskActionBreakGlass:
		confirmation := openAPIHeaderParameter("X-Operation-Confirmation", "Explicit break-glass confirmation required by the compiled endpoint contract", true)
		confirmation["schema"] = map[string]any{"type": "string", "enum": []string{"break-glass"}}
		parameters = upsertOpenAPIHeaderParameter(parameters, confirmation)
	}
	operation["parameters"] = parameters
}

func upsertOpenAPIHeaderParameter(parameters []map[string]any, replacement map[string]any) []map[string]any {
	for index, parameter := range parameters {
		if parameter["in"] == "header" && parameter["name"] == replacement["name"] {
			parameters[index] = replacement
			return parameters
		}
	}
	return append(parameters, replacement)
}

func compiledEndpointSurfaceExtension(contract surfacemodel.RuntimeEndpointContractV1, operationID any) map[string]any {
	projections := make([]map[string]any, 0, len(contract.Projections))
	surfaces := make([]string, 0, len(contract.Projections))
	audiences := append([]string(nil), contract.ProtocolAudiences...)
	exposures := make([]string, 0, len(contract.Projections))
	for _, projection := range contract.Projections {
		projections = append(projections, map[string]any{
			"surface":        string(projection.Surface),
			"actor_audience": string(projection.ActorAudience),
			"exposure_class": string(projection.ExposureClass),
		})
		surfaces = append(surfaces, string(projection.Surface))
		audiences = append(audiences, string(projection.ActorAudience))
		exposures = append(exposures, string(projection.ExposureClass))
	}
	return map[string]any{
		"contract_version":        contract.ContractVersion,
		"endpoint_identity":       contract.EndpointIdentity,
		"operation_id":            operationID,
		"target_surfaces":         surfaces,
		"actor_audiences":         audiences,
		"surface_projections":     projections,
		"required_permissions":    append([]string(nil), contract.RequiredPermissions...),
		"permission_policy_ref":   contract.PermissionPolicyRef,
		"exposure_classes":        exposures,
		"effect_class":            string(contract.EffectClass),
		"high_risk_action_policy": string(contract.HighRiskPolicy),
		"idempotency_decision":    contract.IdempotencyDecision,
		"audit_class":             contract.AuditClass,
	}
}
