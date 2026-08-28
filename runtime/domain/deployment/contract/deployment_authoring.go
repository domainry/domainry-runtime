package contract

import (
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

func DeploymentFrontendSupportObservationAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "maintenance.frontend_support_observation", Status: "supported", Lifecycle: "source_owned_frontend_registration",
		Parameters: []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "manifest_version", Type: "string", Required: true}, {Key: "frontend_version", Type: "string", Required: true},
			{Key: "runtime_contract_versions", Type: "array", Required: true, ItemSchema: "contract_version"},
			{Key: "entries", Type: "array", Required: true, ItemSchema: "frontend_capability_support_entry"},
		},
		Permissions: []string{"workspace.admin"}, ValidationEndpoint: "POST /frontend-capability-manifest/validate",
		ConfigurationRoutes: []string{"GET /frontend-capability-manifest", "POST /frontend-capability-manifest/validate", "PUT /frontend-capability-manifest"},
		InputSchema:         deploymentFrontendManifestInputSchema(), OutputSchema: deploymentFrontendManifestValidationOutputSchema(),
		OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "valid", JSONPointer: "/valid", Type: "boolean", VisibleTo: "subsequent_capability_calls"}, {Name: "normalized_manifest", JSONPointer: "/normalized_manifest", Type: "frontend_capability_manifest", VisibleTo: "subsequent_capability_calls"}},
		Execution:       &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"runtime.authoring_capabilities", "deployment.frontend_manifest"}, WriteSet: []string{"deployment.frontend_manifest"}, Transaction: "deployment_manifest_transaction", Idempotency: "manifest_contract_hash", SideEffects: []string{"frontend_capability_manifest_registered"}, SideEffectLevel: "internal", Compensation: "restore_previous_manifest_revision", PermissionModel: "workspace.admin"},
		Examples:        deploymentFrontendManifestExamples(),
		Errors: []capabilitycontract.CapabilityAuthoringError{
			{Code: "backend.frontend.manifest_version_invalid", FieldPath: "manifest_version", ParameterKeys: []string{"expected", "actual"}, MessageKey: "backend.frontend.manifest_version_invalid"},
			{Code: "backend.frontend.version_required", FieldPath: "frontend_version", MessageKey: "backend.frontend.version_required"},
			{Code: "backend.frontend.runtime_contract_required", FieldPath: "runtime_contract_versions", MessageKey: "backend.frontend.runtime_contract_required"},
			{Code: "backend.frontend.runtime_contract_unsupported", FieldPath: "runtime_contract_versions", ParameterKeys: []string{"expected", "actual"}, MessageKey: "backend.frontend.runtime_contract_unsupported"},
			{Code: "backend.frontend.support_key_invalid", FieldPath: "entries[].support_key", ParameterKeys: []string{"actual"}, MessageKey: "backend.frontend.support_key_invalid"},
			{Code: "backend.frontend.route_invalid", FieldPath: "entries[].route", ParameterKeys: []string{"actual"}, MessageKey: "backend.frontend.route_invalid"},
			{Code: "backend.frontend.feature_module_invalid", FieldPath: "entries[].feature_module", ParameterKeys: []string{"actual"}, MessageKey: "backend.frontend.feature_module_invalid"},
			{Code: "backend.frontend.evidence_required", FieldPath: "entries[].acceptance_tests", MessageKey: "backend.frontend.evidence_required"},
			{Code: "backend.frontend.deployment_evidence_required", FieldPath: "deployment_evidence", ParameterKeys: []string{"field"}, MessageKey: "backend.frontend.deployment_evidence_required"},
			{Code: "backend.frontend.deployment_evidence_invalid", FieldPath: "deployment_evidence.*_hash", ParameterKeys: []string{"field"}, MessageKey: "backend.frontend.deployment_evidence_invalid"},
			{Code: "backend.frontend.acceptance_test_invalid", FieldPath: "entries[].acceptance_tests[]", ParameterKeys: []string{"actual"}, MessageKey: "backend.frontend.acceptance_test_invalid"},
			{Code: "backend.frontend.capability_binding_invalid", FieldPath: "entries[].capability_keys[]", ParameterKeys: []string{"capability", "support_key"}, MessageKey: "backend.frontend.capability_binding_invalid"},
			{Code: "backend.frontend.capability_duplicate", FieldPath: "entries[].capability_keys[]", ParameterKeys: []string{"actual"}, MessageKey: "backend.frontend.capability_duplicate"},
			{Code: "backend.frontend.permission_invalid", FieldPath: "entries[].required_permissions[]", ParameterKeys: []string{"actual"}, MessageKey: "backend.frontend.permission_invalid"},
			{Code: "backend.frontend.permission_missing", FieldPath: "entries[].required_permissions", ParameterKeys: []string{"permission", "capability"}, MessageKey: "backend.frontend.permission_missing"},
			{Code: "backend.frontend.business_binding_invalid", FieldPath: "entries[].{actor_roles,business_objects,view_keys,implemented_actions,report_keys,field_keys,acceptance_claims}[]", ParameterKeys: []string{"kind", "actual"}, MessageKey: "backend.frontend.business_binding_invalid"},
			{Code: "backend.frontend.business_binding_unknown", FieldPath: "entries[].{actor_roles,business_objects,view_keys,implemented_actions,report_keys,field_keys}[]", ParameterKeys: []string{"kind", "actual"}, MessageKey: "backend.frontend.business_binding_unknown"},
			{Code: "backend.frontend.support_missing", FieldPath: "entries", ParameterKeys: []string{"support_key", "capability"}, MessageKey: "backend.frontend.support_missing"},
		},
		Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "validation", Path: "runtime/domain/deployment/validation/deployment_frontend_capability_usage_validation.go", Symbol: "frontendCapabilityUsageValidator"}, {Kind: "service", Path: "runtime/application/deployment/deployment_frontend_capability_application_service.go", Symbol: "DeploymentFrontendCapabilityApplicationService.RegisterManifest"}, {Kind: "ui", Path: "frontend/domainry-admin/src/data/frontend-capability-support.ts", Symbol: "FRONTEND_CAPABILITY_SUPPORT_MANIFEST"}},
	}
}

func deploymentFrontendManifestInputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	stringArray := func() capabilitycontract.CapabilityAuthoringSchema {
		return capabilitycontract.CapabilityAuthoringSchema{Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string"}}
	}
	evidence := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"audit_contract_version", "design_contract_hash", "route_registry_hash", "frontend_source_hash", "audit_artifact_hash"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"audit_contract_version": {Type: "string"}, "design_contract_hash": {Type: "string"}, "route_registry_hash": {Type: "string"}, "frontend_source_hash": {Type: "string"}, "audit_artifact_hash": {Type: "string"},
	}}
	entry := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"support_key", "capability_keys", "route", "required_permissions", "feature_module", "acceptance_tests"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"support_key": {Type: "string"}, "capability_keys": stringArray(), "route": {Type: "string"}, "required_permissions": stringArray(), "feature_module": {Type: "string"}, "acceptance_tests": stringArray(),
		"actor_roles": stringArray(), "business_objects": stringArray(), "view_keys": stringArray(), "implemented_actions": stringArray(), "report_keys": stringArray(), "field_keys": stringArray(), "acceptance_claims": stringArray(),
	}}
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"manifest_version", "frontend_version", "runtime_contract_versions", "entries"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"manifest_version": {Type: "string", Const: deploymentmodel.FrontendCapabilityManifestVersion}, "frontend_version": {Type: "string"}, "runtime_contract_versions": stringArray(), "deployment_evidence": evidence, "entries": {Type: "array", Items: &entry},
	}, Definitions: map[string]capabilitycontract.CapabilityAuthoringSchema{"frontend_deployment_evidence": evidence, "frontend_capability_support_entry": entry}}
}

func deploymentFrontendManifestValidationOutputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	manifest := *deploymentFrontendManifestInputSchema()
	manifest.Schema = ""
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"valid", "normalized_manifest", "issues", "contract_version"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"valid": {Type: "boolean"}, "normalized_manifest": manifest, "issues": {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}}, "contract_version": {Type: "string"}}}
}

func deploymentFrontendManifestExamples() []capabilitycontract.CapabilityAuthoringExample {
	minimal := map[string]any{"manifest_version": deploymentmodel.FrontendCapabilityManifestVersion, "frontend_version": "domainry-admin-0.1.0", "runtime_contract_versions": []any{"$runtime.contract_version"}, "entries": []any{}}
	representative := map[string]any{"manifest_version": deploymentmodel.FrontendCapabilityManifestVersion, "frontend_version": "domainry-admin-0.1.0", "runtime_contract_versions": []any{"$runtime.contract_version"}, "entries": []any{map[string]any{"support_key": "maintenance.reference-impact.v1", "capability_keys": []any{"maintenance.reference_impact"}, "route": "/maintenance/reference-impact", "required_permissions": []any{"workspace.admin"}, "feature_module": "maintenance", "acceptance_tests": []any{"frontend/maintenance/reference-impact.spec.ts"}}}}
	invalid := map[string]any{"manifest_version": "unsupported", "frontend_version": "domainry-admin-0.1.0", "runtime_contract_versions": []any{"$runtime.contract_version"}, "entries": []any{}}
	return []capabilitycontract.CapabilityAuthoringExample{{Name: "minimal_valid", Value: minimal}, {Name: "representative", Value: representative}, {Name: "invalid_with_repair", Value: invalid, ExpectedErrorCodes: []string{"backend.frontend.manifest_version_invalid"}}}
}
