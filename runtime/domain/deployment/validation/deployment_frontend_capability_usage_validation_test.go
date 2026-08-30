package validation

import (
	"strings"
	"testing"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

func TestValidateFrontendCapabilityManifestValidNormalizationAndWarnings(t *testing.T) {
	hash := strings.Repeat("a", 64)
	manifest := deploymentmodel.FrontendCapabilityManifest{
		ManifestVersion:         deploymentmodel.FrontendCapabilityManifestVersion,
		FrontendVersion:         "1.0.0",
		RuntimeContractVersions: []string{" other ", "runtime-v1"},
		DeploymentEvidence:      &deploymentmodel.FrontendDeploymentEvidence{AuditContractVersion: "audit-v1", DesignContractHash: hash, RouteRegistryHash: hash, FrontendSourceHash: hash, AuditArtifactHash: hash},
		Entries: []deploymentmodel.FrontendCapabilitySupportEntry{{
			SupportKey: " customer.list ", CapabilityKeys: []string{" customer.read ", "customer.write"}, Route: "/customers", RequiredPermissions: []string{" customer.read ", "customer.write"},
			FeatureModule: "src/customers", AcceptanceTests: []string{"tests/write.spec.ts", " tests/read.spec.ts "}, ActorRoles: []string{"sales"}, BusinessObjects: []string{"customer"}, ImplementedActions: []string{"customer.create"}, ReportKeys: []string{"customer.pipeline"}, FieldKeys: []string{"customer.name"}, AcceptanceClaims: []string{"list-visible"},
		}},
	}
	definitions := func() []deploymentmodel.FrontendCapabilityDefinition {
		return []deploymentmodel.FrontendCapabilityDefinition{
			{Key: "customer.read", FrontendSupportKey: "customer.list", Permissions: []string{"customer.read"}},
			{Key: "customer.write", FrontendSupportKey: "customer.list", Permissions: []string{"customer.write"}},
			{Key: "order.read", FrontendSupportKey: "order.list"},
			{Key: "internal"},
		}
	}
	result := ValidateFrontendCapabilityManifest(manifest, "runtime-v1", definitions)
	if !result.Valid || result.ContractVersion != "runtime-v1" || result.NormalizedManifest.Entries[0].SupportKey != "customer.list" {
		t.Fatalf("result = %#v", result)
	}
	assertFrontendIssue(t, result.Issues, "backend.frontend.support_missing", "warning")
	if got := result.NormalizedManifest.Entries[0].CapabilityKeys; len(got) != 2 || got[0] != "customer.read" {
		t.Fatalf("normalized capabilities = %#v", got)
	}
}

func TestValidateFrontendCapabilityManifestIdentityAndEvidenceFailures(t *testing.T) {
	plain := deploymentmodel.FrontendCapabilityManifest{ManifestVersion: deploymentmodel.FrontendCapabilityManifestVersion, FrontendVersion: "1", RuntimeContractVersions: []string{"runtime-v1"}}
	if result := ValidateFrontendCapabilityManifest(plain, "runtime-v1", nil); !result.Valid {
		t.Fatalf("plain manifest = %#v", result)
	}
	manifest := deploymentmodel.FrontendCapabilityManifest{ManifestVersion: "old", RuntimeContractVersions: []string{"other"}, Entries: []deploymentmodel.FrontendCapabilitySupportEntry{{ActorRoles: []string{"admin"}}}}
	result := ValidateFrontendCapabilityManifest(manifest, "runtime-v1", nil)
	for _, code := range []string{"backend.frontend.manifest_version_invalid", "backend.frontend.version_required", "backend.frontend.runtime_contract_unsupported", "backend.frontend.deployment_evidence_required"} {
		assertFrontendIssue(t, result.Issues, code, "error")
	}
	manifest.RuntimeContractVersions = nil
	manifest.DeploymentEvidence = &deploymentmodel.FrontendDeploymentEvidence{}
	result = ValidateFrontendCapabilityManifest(manifest, "runtime-v1", func() []deploymentmodel.FrontendCapabilityDefinition { return nil })
	assertFrontendIssue(t, result.Issues, "backend.frontend.runtime_contract_required", "error")
	assertFrontendIssue(t, result.Issues, "backend.frontend.deployment_evidence_required", "error")
	assertFrontendIssue(t, result.Issues, "backend.frontend.deployment_evidence_invalid", "error")
}

func TestFrontendEntryRouteEvidenceCapabilitiesAndPermissions(t *testing.T) {
	validator := frontendCapabilityUsageValidator{
		capabilities: map[string]deploymentmodel.FrontendCapabilityDefinition{
			"cap": {Key: "cap", FrontendSupportKey: "support", Permissions: []string{"read", "write"}},
		},
		expectedSupport: map[string]string{"cap": "support"}, registeredSupport: map[string]bool{}, contractVersion: "runtime-v1",
	}
	for _, route := range []string{"", "relative", "/bad?query", "/bad#fragment", "//double", "/tail/", "/a/../b", "/", "/valid"} {
		validator.validateEntryRoute("entry", deploymentmodel.FrontendCapabilitySupportEntry{SupportKey: "support", Route: route})
	}
	for _, entry := range []deploymentmodel.FrontendCapabilitySupportEntry{
		{SupportKey: "support", FeatureModule: ""},
		{SupportKey: "support", FeatureModule: "/absolute", AcceptanceTests: []string{""}},
		{SupportKey: "support", FeatureModule: "../escape", AcceptanceTests: []string{"/absolute"}},
		{SupportKey: "support", FeatureModule: "src/feature", AcceptanceTests: []string{"../escape", "same", "same", "valid"}},
	} {
		validator.validateEntryEvidence("entry", entry)
	}
	entry := deploymentmodel.FrontendCapabilitySupportEntry{SupportKey: "support", CapabilityKeys: []string{"", "cap", "cap", "unknown"}, RequiredPermissions: []string{"", "read", "read"}}
	validator.validateEntryCapabilities("entry", &entry)
	wrongSupport := deploymentmodel.FrontendCapabilitySupportEntry{SupportKey: "wrong", CapabilityKeys: []string{"cap"}}
	validator.validateEntryCapabilities("entry", &wrongSupport)
	validator.validateEntryPermissions("entry", &entry)
	for _, code := range []string{"backend.frontend.route_invalid", "backend.frontend.feature_module_invalid", "backend.frontend.evidence_required", "backend.frontend.acceptance_test_invalid", "backend.frontend.capability_duplicate", "backend.frontend.capability_binding_invalid", "backend.frontend.permission_invalid", "backend.frontend.permission_missing"} {
		assertFrontendIssue(t, validator.issues, code, "error")
	}
}

func TestFrontendBusinessBindingValidationAndSmallHelpers(t *testing.T) {
	validator := frontendCapabilityUsageValidator{}
	entry := deploymentmodel.FrontendCapabilitySupportEntry{
		SupportKey: "support", ActorRoles: []string{"", "sales", "sales", "bad role"}, BusinessObjects: []string{" customer "}, ImplementedActions: []string{"customer.create"}, ReportKeys: []string{"customer.report"},
		FieldKeys: []string{"", "customer", ".name", "customer.", "customer.name", "customer.name"}, AcceptanceClaims: []string{"claim"},
	}
	validator.validateEntryBusinessBindings("entry", &entry)
	assertFrontendIssue(t, validator.issues, "backend.frontend.business_binding_invalid", "error")
	if !manifestHasFrontendBusinessBindings(deploymentmodel.FrontendCapabilityManifest{Entries: []deploymentmodel.FrontendCapabilitySupportEntry{{}, {FieldKeys: []string{"customer.name"}}}}) || manifestHasFrontendBusinessBindings(deploymentmodel.FrontendCapabilityManifest{}) {
		t.Fatal("manifest business binding detection")
	}
	for _, entry := range []deploymentmodel.FrontendCapabilitySupportEntry{
		{ActorRoles: []string{"x"}}, {BusinessObjects: []string{"x"}}, {ImplementedActions: []string{"x"}}, {ReportKeys: []string{"x"}}, {FieldKeys: []string{"x"}}, {AcceptanceClaims: []string{"x"}}, {},
	} {
		_ = frontendCapabilityHasBusinessBindings(entry)
	}
	if !containsTrimmedString([]string{" other ", " target "}, "target") || containsTrimmedString([]string{"other"}, "target") || stringIndex(12) != "12" {
		t.Fatal("small helpers")
	}
}

func TestFrontendValidateEntriesDuplicateAndSort(t *testing.T) {
	validator := frontendCapabilityUsageValidator{
		manifest: deploymentmodel.FrontendCapabilityManifest{RuntimeContractVersions: []string{"z", "a"}, Entries: []deploymentmodel.FrontendCapabilitySupportEntry{
			{SupportKey: " z ", Route: "/z", FeatureModule: "z", AcceptanceTests: []string{"z"}},
			{SupportKey: "z", Route: "/z", FeatureModule: "z", AcceptanceTests: []string{"z"}},
			{SupportKey: "", Route: "/", FeatureModule: "root", AcceptanceTests: []string{"root"}},
		}},
		capabilities: map[string]deploymentmodel.FrontendCapabilityDefinition{}, expectedSupport: map[string]string{}, registeredSupport: map[string]bool{},
	}
	validator.validateEntries()
	validator.normalize()
	assertFrontendIssue(t, validator.issues, "backend.frontend.support_key_invalid", "error")
	if validator.manifest.RuntimeContractVersions[0] != "a" || validator.manifest.Entries[0].SupportKey != "" {
		t.Fatalf("normalized manifest = %#v", validator.manifest)
	}
}

func assertFrontendIssue(t *testing.T, issues []deploymentmodel.FrontendCapabilityUsageValidationIssue, code, severity string) {
	t.Helper()
	for _, issue := range issues {
		if issue.ErrorCode == code && issue.Severity == severity {
			return
		}
	}
	t.Fatalf("missing %s/%s in %#v", severity, code, issues)
}
