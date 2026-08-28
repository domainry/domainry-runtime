package integrationtest

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const salesforceCaseContractVersion = "runtime-salesforce-case-contract-v1"

type salesforceCaseContract struct {
	SchemaVersion             string                `json:"schema_version"`
	CaseID                    string                `json:"case_id"`
	Source                    salesforceCaseSource  `json:"source"`
	Extensions                []salesforceCaseItem  `json:"validation_extensions"`
	Runtime                   salesforceCaseRuntime `json:"runtime_mapping"`
	SyntheticDataPolicy       string                `json:"synthetic_data_policy"`
	AcceptanceAxes            []string              `json:"acceptance_axes"`
	ForbiddenProductionTokens []string              `json:"forbidden_production_tokens"`
}

type salesforceCaseSource struct {
	Title      string               `json:"title"`
	URL        string               `json:"url"`
	AccessedOn string               `json:"accessed_on"`
	Facts      []salesforceCaseFact `json:"source_facts"`
}

type salesforceCaseFact struct {
	ID   string `json:"id"`
	Fact string `json:"fact"`
}

type salesforceCaseItem struct {
	ID       string `json:"id"`
	Scenario string `json:"scenario"`
}

type salesforceCaseRuntime struct {
	Objects      []string `json:"objects"`
	Actions      []string `json:"actions"`
	Roles        []string `json:"roles"`
	Capabilities []string `json:"capabilities"`
}

func TestSalesforceCaseContractsSeparateFactsExtensionsAndGenericRuntimeMapping(t *testing.T) {
	repositoryRoot := contractRepositoryRoot(t)
	directory := "."
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read Salesforce Case contracts: %v", err)
	}
	want := map[string]bool{
		"salesforce_proposal_bulk_fulfillment":   false,
		"salesforce_partner_erp_shipment":        false,
		"salesforce_mobile_lending_collection":   false,
		"salesforce_referral_result_followup":    false,
		"salesforce_insurance_application_claim": false,
	}
	var contracts []salesforceCaseContract
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".salesforce-contract.json") {
			continue
		}
		contract := decodeSalesforceCaseContract(t, filepath.Join(directory, entry.Name()))
		if _, exists := want[contract.CaseID]; !exists {
			t.Errorf("unexpected Salesforce Case ID %q", contract.CaseID)
		} else if want[contract.CaseID] {
			t.Errorf("duplicate Salesforce Case ID %q", contract.CaseID)
		} else {
			want[contract.CaseID] = true
		}
		validateSalesforceCaseContract(t, entry.Name(), contract)
		contracts = append(contracts, contract)
	}
	for key, found := range want {
		if !found {
			t.Errorf("missing Salesforce Case contract %q", key)
		}
	}
	if len(contracts) != len(want) {
		t.Fatalf("Salesforce Case contract count=%d, want %d", len(contracts), len(want))
	}
	assertNoSalesforceCaseProductionSpecialization(t, repositoryRoot, contracts)
}

func decodeSalesforceCaseContract(t *testing.T, path string) salesforceCaseContract {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var contract salesforceCaseContract
	if err := decoder.Decode(&contract); err != nil {
		t.Fatalf("strict decode %s: %v", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("%s contains trailing JSON data", path)
	}
	return contract
}

func validateSalesforceCaseContract(t *testing.T, file string, contract salesforceCaseContract) {
	t.Helper()
	prefix := file + ": "
	if contract.SchemaVersion != salesforceCaseContractVersion {
		t.Errorf("%sschema_version=%q", prefix, contract.SchemaVersion)
	}
	if !strings.HasPrefix(contract.CaseID, "salesforce_") {
		t.Errorf("%scase_id must use salesforce_ namespace", prefix)
	}
	requireContractText(t, prefix+"source.title", contract.Source.Title)
	parsed, err := url.ParseRequestURI(contract.Source.URL)
	if err != nil || parsed.Scheme != "https" || !strings.HasSuffix(strings.ToLower(parsed.Host), "salesforce.com") {
		t.Errorf("%ssource.url must be an official Salesforce HTTPS URL, got %q", prefix, contract.Source.URL)
	}
	if _, err := time.Parse("2006-01-02", contract.Source.AccessedOn); err != nil {
		t.Errorf("%ssource.accessed_on must be YYYY-MM-DD", prefix)
	}
	if len(contract.Source.Facts) < 5 {
		t.Errorf("%ssource_facts must contain at least five independently identified facts", prefix)
	}
	if len(contract.Extensions) < 5 {
		t.Errorf("%svalidation_extensions must contain at least five Runtime stress scenarios", prefix)
	}
	factIDs := map[string]bool{}
	for index, fact := range contract.Source.Facts {
		requireContractText(t, prefix+"source_facts.id", fact.ID)
		requireContractText(t, prefix+"source_facts.fact", fact.Fact)
		if factIDs[fact.ID] {
			t.Errorf("%sduplicate source_fact ID %q at index %d", prefix, fact.ID, index)
		}
		factIDs[fact.ID] = true
	}
	extensionIDs := map[string]bool{}
	for index, extension := range contract.Extensions {
		requireContractText(t, prefix+"validation_extensions.id", extension.ID)
		requireContractText(t, prefix+"validation_extensions.scenario", extension.Scenario)
		if factIDs[extension.ID] {
			t.Errorf("%svalidation_extension ID %q collides with source_fact", prefix, extension.ID)
		}
		if extensionIDs[extension.ID] {
			t.Errorf("%sduplicate validation_extension ID %q at index %d", prefix, extension.ID, index)
		}
		extensionIDs[extension.ID] = true
	}
	requireUniqueContractValues(t, prefix+"runtime_mapping.objects", contract.Runtime.Objects, 3)
	requireUniqueContractValues(t, prefix+"runtime_mapping.actions", contract.Runtime.Actions, 3)
	requireUniqueContractValues(t, prefix+"runtime_mapping.roles", contract.Runtime.Roles, 3)
	requireUniqueContractValues(t, prefix+"runtime_mapping.capabilities", contract.Runtime.Capabilities, 5)
	policy := strings.ToLower(contract.SyntheticDataPolicy)
	if !(strings.Contains(policy, "fictional") || strings.Contains(policy, "synthetic")) || !(strings.Contains(policy, "never") || strings.Contains(policy, "no real") || strings.Contains(policy, "do not")) {
		t.Errorf("%ssynthetic_data_policy must require synthetic data and reject real customer data", prefix)
	}
	requireUniqueContractValues(t, prefix+"forbidden_production_tokens", contract.ForbiddenProductionTokens, 1)
	requireCaseAcceptanceAxes(t, prefix, contract.AcceptanceAxes)
}

func assertNoSalesforceCaseProductionSpecialization(t *testing.T, repositoryRoot string, contracts []salesforceCaseContract) {
	t.Helper()
	forbidden := map[string]string{}
	for _, contract := range contracts {
		for _, token := range contract.ForbiddenProductionTokens {
			forbidden[strings.ToLower(token)] = contract.CaseID
		}
	}
	var tokens []string
	for token := range forbidden {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	err := filepath.WalkDir(filepath.Join(repositoryRoot, "runtime"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		lower := strings.ToLower(string(raw))
		for _, token := range tokens {
			if strings.Contains(lower, token) {
				relative, _ := filepath.Rel(repositoryRoot, path)
				t.Errorf("Salesforce Case %s leaked token %q into Runtime production source %s", forbidden[token], token, relative)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan Runtime production sources: %v", err)
	}
}

func requireContractText(t *testing.T, field, value string) {
	t.Helper()
	if strings.TrimSpace(value) == "" {
		t.Errorf("%s must not be blank", field)
	}
}

func requireUniqueContractValues(t *testing.T, field string, values []string, minimum int) {
	t.Helper()
	if len(values) < minimum {
		t.Errorf("%s must contain at least %d values", field, minimum)
	}
	seen := map[string]bool{}
	for index, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			t.Errorf("%s[%d] must not be blank", field, index)
		} else if seen[normalized] {
			t.Errorf("%s contains duplicate value %q", field, value)
		}
		seen[normalized] = true
	}
}

func requireCaseAcceptanceAxes(t *testing.T, prefix string, actual []string) {
	t.Helper()
	required := map[string]bool{
		"permission": false, "idempotency": false, "transaction": false, "workspace": false,
		"async_execution": false, "external_side_effect": false, "failure_recovery": false, "audit": false,
	}
	for _, axis := range actual {
		if _, exists := required[axis]; !exists {
			t.Errorf("%sacceptance_axes contains unsupported axis %q", prefix, axis)
			continue
		}
		if required[axis] {
			t.Errorf("%sacceptance_axes contains duplicate axis %q", prefix, axis)
		}
		required[axis] = true
	}
	for axis, found := range required {
		if !found {
			t.Errorf("%sacceptance_axes is missing %q", prefix, axis)
		}
	}
	if len(actual) != len(required) {
		t.Errorf("%sacceptance_axes count=%d, want exactly %d", prefix, len(actual), len(required))
	}
}

func contractRepositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolved repository root %s is invalid: %v", root, err)
	}
	return root
}
