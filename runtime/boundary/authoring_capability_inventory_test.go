package boundary_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
)

type authoringCapabilityInventory struct {
	Version               string                              `json:"version"`
	TargetModel           string                              `json:"target_model"`
	CatalogSource         string                              `json:"catalog_source"`
	GenericContractSource string                              `json:"generic_contract_source"`
	DomainCoverage        []authoringCapabilityDomainCoverage `json:"domain_coverage"`
	OwnerGroups           []authoringCapabilityOwnerGroup     `json:"owner_groups"`
	UnpublishedRequired   []authoringCapabilityMissingEntry   `json:"unpublished_required_capabilities"`
	DynamicReferenceAudit authoringCapabilityGapAudit         `json:"dynamic_reference_audit"`
	ProvisioningAudit     authoringCapabilityGapAudit         `json:"provisioning_audit"`
	GlobalValidationAudit authoringCapabilityGapAudit         `json:"global_validation_audit"`
}

type authoringCapabilityDomainCoverage struct {
	Domain                  string `json:"domain"`
	CapabilityCount         int    `json:"capability_count"`
	ValidationEndpointCount int    `json:"validation_endpoint_count"`
	SimulationEndpointCount int    `json:"simulation_endpoint_count"`
	ConfigurationRouteCount int    `json:"configuration_route_count"`
	ExampleCount            int    `json:"example_count"`
}

type authoringCapabilityOwnerGroup struct {
	CatalogDomain           string   `json:"catalog_domain"`
	Owner                   string   `json:"owner"`
	OwnershipStatus         string   `json:"ownership_status"`
	Capabilities            []string `json:"capabilities"`
	CurrentDefinitionFiles  []string `json:"current_definition_files"`
	TargetContractDirectory string   `json:"target_contract_directory"`
	ValidationAnchors       []string `json:"validation_anchors"`
	ApplicationAnchors      []string `json:"application_anchors"`
	TransportAnchors        []string `json:"transport_anchors"`
	PersistenceAnchors      []string `json:"persistence_anchors"`
	TestAnchors             []string `json:"test_anchors"`
	SchemaContractStatus    string   `json:"schema_contract_status"`
	KnownGaps               []string `json:"known_gaps"`
}

type authoringCapabilityGapAudit struct {
	Status                    string   `json:"status"`
	RequiredKinds             []string `json:"required_kinds"`
	CurrentInstanceProjection string   `json:"current_instance_projection"`
	EvidenceAnchors           []string `json:"evidence_anchors"`
	KnownGaps                 []string `json:"known_gaps"`
}

type authoringCapabilityMissingEntry struct {
	Capability      string   `json:"capability"`
	Owner           string   `json:"owner"`
	Status          string   `json:"status"`
	EvidenceAnchors []string `json:"evidence_anchors"`
	RequiredFor     string   `json:"required_for"`
}

func TestRuntimeAuthoringCapabilityInventoryMatchesPublishedCatalog(t *testing.T) {
	inventory := readAuthoringCapabilityInventory(t)
	if inventory.Version != "runtime.authoring-capability-inventory.v1" || strings.TrimSpace(inventory.TargetModel) == "" {
		t.Fatalf("authoring inventory header=%#v", inventory)
	}
	repositoryRoot := filepath.Join("..", "..")
	assertAuthoringInventoryPath(t, repositoryRoot, inventory.CatalogSource)
	assertAuthoringInventoryPath(t, repositoryRoot, inventory.GenericContractSource)

	published := capabilityapplication.RuntimeAuthoringCapabilities()
	publishedByDomain := map[string][]string{}
	coverageByDomain := map[string]authoringCapabilityDomainCoverage{}
	for _, domain := range published.Domains {
		coverage := authoringCapabilityDomainCoverage{Domain: domain.Key, CapabilityCount: len(domain.Capabilities)}
		for _, definition := range domain.Capabilities {
			publishedByDomain[domain.Key] = append(publishedByDomain[domain.Key], definition.Key)
			if definition.ValidationEndpoint != "" {
				coverage.ValidationEndpointCount++
			}
			if definition.SimulationEndpoint != "" {
				coverage.SimulationEndpointCount++
			}
			if len(definition.ConfigurationRoutes) > 0 {
				coverage.ConfigurationRouteCount++
			}
			if len(definition.Examples) > 0 {
				coverage.ExampleCount++
			}
		}
		sort.Strings(publishedByDomain[domain.Key])
		coverageByDomain[domain.Key] = coverage
	}

	inventoryCoverage := map[string]authoringCapabilityDomainCoverage{}
	for _, coverage := range inventory.DomainCoverage {
		if strings.TrimSpace(coverage.Domain) == "" {
			t.Fatal("authoring inventory has empty coverage domain")
		}
		if _, duplicated := inventoryCoverage[coverage.Domain]; duplicated {
			t.Fatalf("authoring inventory duplicates coverage domain %s", coverage.Domain)
		}
		inventoryCoverage[coverage.Domain] = coverage
	}
	if !equalAuthoringCoverage(coverageByDomain, inventoryCoverage) {
		t.Fatalf("authoring inventory endpoint coverage drifted\npublished=%#v\ninventory=%#v", coverageByDomain, inventoryCoverage)
	}

	inventoriedByDomain := map[string][]string{}
	seenCapabilities := map[string]bool{}
	for index, group := range inventory.OwnerGroups {
		if strings.TrimSpace(group.CatalogDomain) == "" || strings.TrimSpace(group.Owner) == "" || group.OwnershipStatus != "owner_owned" {
			t.Fatalf("authoring owner group[%d] has incomplete identity: %#v", index, group)
		}
		if len(group.Capabilities) == 0 || len(group.CurrentDefinitionFiles) == 0 || len(group.ValidationAnchors) == 0 || len(group.ApplicationAnchors) == 0 || len(group.TransportAnchors) == 0 || len(group.PersistenceAnchors) == 0 || len(group.TestAnchors) == 0 || group.KnownGaps == nil {
			t.Fatalf("authoring owner group %s/%s lacks executable evidence or gap classification", group.CatalogDomain, group.Owner)
		}
		if strings.TrimSpace(group.SchemaContractStatus) == "" || strings.TrimSpace(group.TargetContractDirectory) == "" {
			t.Fatalf("authoring owner group %s/%s lacks schema or target contract status", group.CatalogDomain, group.Owner)
		}
		paths := append([]string{}, group.CurrentDefinitionFiles...)
		paths = append(paths, group.ValidationAnchors...)
		paths = append(paths, group.ApplicationAnchors...)
		paths = append(paths, group.TransportAnchors...)
		paths = append(paths, group.PersistenceAnchors...)
		paths = append(paths, group.TestAnchors...)
		for _, path := range paths {
			assertAuthoringInventoryPath(t, repositoryRoot, path)
		}
		ownerDefinitionPrefix := "runtime/domain/" + group.Owner + "/"
		for _, path := range group.CurrentDefinitionFiles {
			if !strings.HasPrefix(path, ownerDefinitionPrefix) {
				t.Fatalf("authoring capability definition for %s/%s is not owner-owned: %s must be under %s", group.CatalogDomain, group.Owner, path, ownerDefinitionPrefix)
			}
		}
		assertAuthoringInventoryPath(t, repositoryRoot, group.TargetContractDirectory)
		for _, capability := range group.Capabilities {
			if seenCapabilities[capability] {
				t.Fatalf("authoring capability %s has more than one owner", capability)
			}
			seenCapabilities[capability] = true
			inventoriedByDomain[group.CatalogDomain] = append(inventoriedByDomain[group.CatalogDomain], capability)
		}
	}
	for domain := range inventoriedByDomain {
		sort.Strings(inventoriedByDomain[domain])
	}
	if !equalAuthoringCapabilities(publishedByDomain, inventoriedByDomain) {
		t.Fatalf("authoring capability owner inventory drifted\npublished=%#v\ninventory=%#v", publishedByDomain, inventoriedByDomain)
	}

	missingRequired := map[string]bool{}
	for _, entry := range inventory.UnpublishedRequired {
		if _, required := missingRequired[entry.Capability]; !required || entry.Status != "missing_from_catalog" || strings.TrimSpace(entry.Owner) == "" || strings.TrimSpace(entry.RequiredFor) == "" || len(entry.EvidenceAnchors) == 0 {
			t.Fatalf("authoring missing-capability entry is incomplete or unexpected: %#v", entry)
		}
		if seenCapabilities[entry.Capability] {
			t.Fatalf("authoring capability %s is both published and classified as missing", entry.Capability)
		}
		missingRequired[entry.Capability] = true
		for _, path := range entry.EvidenceAnchors {
			assertAuthoringInventoryPath(t, repositoryRoot, path)
		}
	}
	for capability, found := range missingRequired {
		if !found {
			t.Fatalf("required unpublished authoring capability %s is not inventoried", capability)
		}
	}

	for name, audit := range map[string]authoringCapabilityGapAudit{"dynamic_reference": inventory.DynamicReferenceAudit, "provisioning": inventory.ProvisioningAudit, "global_validation": inventory.GlobalValidationAudit} {
		status := strings.TrimSpace(audit.Status)
		if status == "" || (status != "complete" && len(audit.KnownGaps) == 0) {
			t.Fatalf("authoring %s audit has no status or gaps", name)
		}
		for _, path := range audit.EvidenceAnchors {
			assertAuthoringInventoryPath(t, repositoryRoot, path)
		}
		if audit.CurrentInstanceProjection != "" {
			assertAuthoringInventoryPath(t, repositoryRoot, audit.CurrentInstanceProjection)
		}
	}
	if len(inventory.DynamicReferenceAudit.RequiredKinds) == 0 {
		t.Fatal("authoring dynamic reference inventory is empty")
	}
}

func TestRuntimeAuthoringCentralCatalogDoesNotOwnBusinessFacts(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	files, err := filepath.Glob(filepath.Join(repositoryRoot, "runtime", "application", "capability", "capability_authoring_*.go"))
	if err != nil {
		t.Fatalf("glob central authoring catalog: %v", err)
	}
	for _, path := range files {
		name := filepath.Base(path)
		if strings.HasSuffix(name, "_test.go") || name == "capability_authoring_catalog.go" || name == "capability_authoring_instance.go" || name == "capability_authoring_contract.go" || name == "capability_authoring_error.go" {
			continue
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		content := string(raw)
		for _, businessFact := range []string{"Parameters:", "Errors:", "Examples:", "InputSchema:", "OutputSchema:", "ReferenceContracts:", "Execution:"} {
			if strings.Contains(content, businessFact) {
				t.Errorf("central authoring aggregation %s owns business fact %s", name, businessFact)
			}
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, raw, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		allowedTopLevelExports := map[string]bool{
			"RuntimeAuthoringCapabilities": true, "CapabilityAuthoringInstanceHash": true,
			"SortAuthoringContract": true, "ContractHash": true, "RuntimeAuthoringErrorContract": true,
			"RuntimeAuthoringProjection": true, "CapabilityLegacyObjectKinds": true,
			"NewCapabilityAuthoringApplicationService": true,
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || !function.Name.IsExported() {
				continue
			}
			if !allowedTopLevelExports[function.Name.Name] {
				t.Errorf("central authoring aggregation %s re-exports owner capability fact through %s", name, function.Name.Name)
			}
		}
	}
}

func readAuthoringCapabilityInventory(t *testing.T) authoringCapabilityInventory {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "runtime_authoring_capability_inventory_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory authoringCapabilityInventory
	if err := json.Unmarshal(raw, &inventory); err != nil {
		t.Fatal(err)
	}
	return inventory
}

func assertAuthoringInventoryPath(t *testing.T, repositoryRoot, path string) {
	t.Helper()
	if strings.TrimSpace(path) == "" || filepath.IsAbs(path) || strings.Contains(path, "..") {
		t.Fatalf("authoring inventory has unsafe path %q", path)
	}
	if _, err := os.Stat(filepath.Join(repositoryRoot, filepath.FromSlash(path))); err != nil {
		t.Fatalf("authoring inventory evidence path %s is missing: %v", path, err)
	}
}

func equalAuthoringCoverage(left, right map[string]authoringCapabilityDomainCoverage) bool {
	if len(left) != len(right) {
		return false
	}
	for key, expected := range left {
		if actual, ok := right[key]; !ok || actual != expected {
			return false
		}
	}
	return true
}

func equalAuthoringCapabilities(left, right map[string][]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, expected := range left {
		actual, ok := right[key]
		if !ok || strings.Join(expected, "\x00") != strings.Join(actual, "\x00") {
			return false
		}
	}
	return true
}
