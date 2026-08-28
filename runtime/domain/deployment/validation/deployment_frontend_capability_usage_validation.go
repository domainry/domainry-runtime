package validation

import deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var frontendEvidenceHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type frontendCapabilityUsageValidator struct {
	manifest              deploymentmodel.FrontendCapabilityManifest
	issues                []deploymentmodel.FrontendCapabilityUsageValidationIssue
	capabilities          map[string]deploymentmodel.FrontendCapabilityDefinition
	expectedSupport       map[string]string
	registeredSupport     map[string]bool
	contractVersion       string
	capabilityDefinitions func() []deploymentmodel.FrontendCapabilityDefinition
}

func ValidateFrontendCapabilityManifest(manifest deploymentmodel.FrontendCapabilityManifest, contractVersion string, capabilityDefinitions func() []deploymentmodel.FrontendCapabilityDefinition) deploymentmodel.FrontendCapabilityManifestValidationResult {
	validator := frontendCapabilityUsageValidator{manifest: manifest, issues: []deploymentmodel.FrontendCapabilityUsageValidationIssue{}, capabilities: map[string]deploymentmodel.FrontendCapabilityDefinition{}, expectedSupport: map[string]string{}, registeredSupport: map[string]bool{}, contractVersion: contractVersion}
	validator.capabilityDefinitions = capabilityDefinitions
	validator.loadRuntimeContract()
	validator.validateManifestIdentity()
	validator.validateEntries()
	validator.appendMissingSupportWarnings()
	validator.normalize()
	valid := true
	for _, issue := range validator.issues {
		if issue.Severity == "error" {
			valid = false
		}
	}
	return deploymentmodel.FrontendCapabilityManifestValidationResult{Valid: valid, NormalizedManifest: validator.manifest, Issues: validator.issues, ContractVersion: validator.contractVersion}
}

func (validator *frontendCapabilityUsageValidator) loadRuntimeContract() {
	if validator.capabilityDefinitions == nil {
		return
	}
	for _, capability := range validator.capabilityDefinitions() {
		validator.capabilities[capability.Key] = capability
		if capability.FrontendSupportKey != "" {
			validator.expectedSupport[capability.Key] = capability.FrontendSupportKey
		}
	}
}

func (validator *frontendCapabilityUsageValidator) validateManifestIdentity() {
	if strings.TrimSpace(validator.manifest.ManifestVersion) != deploymentmodel.FrontendCapabilityManifestVersion {
		validator.issue("error", "", "manifest_version", "backend.frontend.manifest_version_invalid", "", map[string]string{"expected": deploymentmodel.FrontendCapabilityManifestVersion, "actual": validator.manifest.ManifestVersion})
	}
	if strings.TrimSpace(validator.manifest.FrontendVersion) == "" {
		validator.issue("error", "", "frontend_version", "backend.frontend.version_required", "", nil)
	}
	if len(validator.manifest.RuntimeContractVersions) == 0 {
		validator.issue("error", "", "runtime_contract_versions", "backend.frontend.runtime_contract_required", "", nil)
	} else if !containsTrimmedString(validator.manifest.RuntimeContractVersions, validator.contractVersion) {
		validator.issue("error", "", "runtime_contract_versions", "backend.frontend.runtime_contract_unsupported", "", map[string]string{"expected": validator.contractVersion, "actual": strings.Join(validator.manifest.RuntimeContractVersions, ",")})
	}
	if manifestHasFrontendBusinessBindings(validator.manifest) {
		validator.validateDeploymentEvidence()
	}
}

func manifestHasFrontendBusinessBindings(manifest deploymentmodel.FrontendCapabilityManifest) bool {
	for _, entry := range manifest.Entries {
		if frontendCapabilityHasBusinessBindings(entry) {
			return true
		}
	}
	return false
}

func (validator *frontendCapabilityUsageValidator) validateDeploymentEvidence() {
	evidence := validator.manifest.DeploymentEvidence
	if evidence == nil {
		validator.issue("error", "", "deployment_evidence", "backend.frontend.deployment_evidence_required", "", nil)
		return
	}
	values := map[string]string{
		"audit_contract_version": evidence.AuditContractVersion,
		"design_contract_hash":   evidence.DesignContractHash,
		"route_registry_hash":    evidence.RouteRegistryHash,
		"frontend_source_hash":   evidence.FrontendSourceHash,
		"audit_artifact_hash":    evidence.AuditArtifactHash,
	}
	for field, value := range values {
		value = strings.TrimSpace(value)
		if field == "audit_contract_version" {
			if value == "" {
				validator.issue("error", "", "deployment_evidence."+field, "backend.frontend.deployment_evidence_required", "", map[string]string{"field": field})
			}
			continue
		}
		if !frontendEvidenceHashPattern.MatchString(value) {
			validator.issue("error", "", "deployment_evidence."+field, "backend.frontend.deployment_evidence_invalid", "", map[string]string{"field": field})
		}
	}
}

func (validator *frontendCapabilityUsageValidator) validateEntries() {
	for index := range validator.manifest.Entries {
		entry := &validator.manifest.Entries[index]
		entry.SupportKey = strings.TrimSpace(entry.SupportKey)
		path := "entries[" + stringIndex(index) + "]"
		if entry.SupportKey == "" || validator.registeredSupport[entry.SupportKey] {
			validator.issue("error", entry.SupportKey, path+".support_key", "backend.frontend.support_key_invalid", "", map[string]string{"actual": entry.SupportKey})
		}
		validator.registeredSupport[entry.SupportKey] = true
		validator.validateEntryRoute(path, *entry)
		validator.validateEntryEvidence(path, *entry)
		validator.validateEntryCapabilities(path, entry)
		validator.validateEntryPermissions(path, entry)
		validator.validateEntryBusinessBindings(path, entry)
	}
}

func (validator *frontendCapabilityUsageValidator) normalize() {
	sort.Strings(validator.manifest.RuntimeContractVersions)
	for index := range validator.manifest.Entries {
		entry := &validator.manifest.Entries[index]
		sort.Strings(entry.CapabilityKeys)
		sort.Strings(entry.RequiredPermissions)
		sort.Strings(entry.AcceptanceTests)
		sort.Strings(entry.ActorRoles)
		sort.Strings(entry.BusinessObjects)
		sort.Strings(entry.ViewKeys)
		sort.Strings(entry.ImplementedActions)
		sort.Strings(entry.ReportKeys)
		sort.Strings(entry.FieldKeys)
		sort.Strings(entry.AcceptanceClaims)
	}
	sort.Slice(validator.manifest.Entries, func(i, j int) bool {
		return validator.manifest.Entries[i].SupportKey < validator.manifest.Entries[j].SupportKey
	})
}

func (validator *frontendCapabilityUsageValidator) issue(severity, supportKey, path, code, capability string, params map[string]string) {
	validator.issues = append(validator.issues, deploymentmodel.FrontendCapabilityUsageValidationIssue{Severity: severity, EntrySupportKey: supportKey, FieldPath: path, ErrorCode: code, MessageKey: code, CapabilityKey: capability, ContractVersion: validator.contractVersion, Params: params})
}

func containsTrimmedString(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func frontendCapabilityHasBusinessBindings(entry deploymentmodel.FrontendCapabilitySupportEntry) bool {
	return len(entry.ActorRoles) > 0 || len(entry.BusinessObjects) > 0 || len(entry.ViewKeys) > 0 ||
		len(entry.ImplementedActions) > 0 || len(entry.ReportKeys) > 0 || len(entry.FieldKeys) > 0 || len(entry.AcceptanceClaims) > 0
}

func stringIndex(index int) string { return strconv.Itoa(index) }
