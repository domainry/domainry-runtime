package capabilityprovider

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/modulecapability"
	"github.com/domainry/domainry-foundation/modulecapability/contracttest"
	actionprojection "github.com/domainry/domainry-runtime/runtime/domain/action/projection"
	appschemacontract "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"
	automationpolicy "github.com/domainry/domainry-runtime/runtime/domain/automation/policy"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
	profilebindingcontract "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/contract"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

func TestBindingsConformAndAreDeterministic(t *testing.T) {
	first, err := Bindings()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Bindings()
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{
		"action_registry", "automation", "business_references", "business_system", "discovery", "notification_bridge", "profile_binding", "publication", "realtime", "records",
		"runtime_core", "runtime_dispatch", "runtime_openapi", "runtime_operations", "runtime_schema", "uploads", "workflow", "workspace_provision",
	}
	actual := make([]string, 0, len(first))
	firstDigests := map[string]string{}
	for _, binding := range first {
		contracttest.VerifyBinding(t, binding)
		summary, summaryErr := binding.CapabilitySummary(t.Context())
		if summaryErr != nil {
			t.Fatal(summaryErr)
		}
		actual = append(actual, summary.Identity.Key)
		firstDigests[summary.Identity.Key] = summary.Identity.ContractSHA256
		assertRuntimeCategoryOperations(t, binding, summary)
	}
	sort.Strings(actual)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("Runtime provider keys=%v, want %v", actual, expected)
	}
	for _, binding := range second {
		summary, summaryErr := binding.CapabilitySummary(t.Context())
		if summaryErr != nil {
			t.Fatal(summaryErr)
		}
		if firstDigests[summary.Identity.Key] != summary.Identity.ContractSHA256 {
			t.Fatalf("provider %s digest is nondeterministic: %s != %s", summary.Identity.Key, firstDigests[summary.Identity.Key], summary.Identity.ContractSHA256)
		}
	}
}

func TestProviderSpecsOwnEveryRuntimeEndpointExactlyOnce(t *testing.T) {
	specs, err := providerSpecs()
	if err != nil {
		t.Fatal(err)
	}
	for identity, contract := range endpointmodel.EndpointContracts {
		owners := []string{}
		for _, spec := range specs {
			for _, category := range spec.categories {
				if category.selectEndpoints == nil || !category.selectEndpoints(contract) {
					continue
				}
				if spec.sourceOwner != contract.SourceOwner {
					t.Errorf("%s selected by provider %s owner=%s, endpoint owner=%s", identity, spec.key, spec.sourceOwner, contract.SourceOwner)
				}
				owners = append(owners, spec.key+"/"+category.key)
			}
		}
		if len(owners) != 1 {
			t.Errorf("%s has %d Runtime capability owners: %v", identity, len(owners), owners)
		}
	}
}

func TestAuthoringProjectionsContainOnlyModelOwnedInput(t *testing.T) {
	definitions := []capabilitycontract.CapabilityAuthoringDefinition{
		authoringDefinition(t, appschemacontract.ApplicationSchemaAuthoringDomain(), "schema.object"),
		authoringDefinition(t, actionprojection.ActionAuthoringDomain(), "action.definition"),
		authoringDefinition(t, workflowpolicy.WorkflowAuthoringDomain(), "workflow.definition"),
		authoringDefinition(t, automationpolicy.AutomationAuthoringDomain(), "automation.rule"),
		profilebindingcontract.ProfileBindingAuthoringCapability(),
	}
	projections, err := authoringProjections(definitions)
	if err != nil {
		t.Fatal(err)
	}
	previousBytes, compactBytes := 0, 0
	forbidden := []string{"status", "parameters", "errors", "examples", "output_schema", "output_variables", "execution"}
	for index, projection := range projections {
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(projection.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"key", "lifecycle", "input_schema"} {
			if len(payload[key]) == 0 {
				t.Errorf("projection %s does not disclose required model input %q", projection.Key, key)
			}
		}
		for _, key := range forbidden {
			if _, exists := payload[key]; exists {
				t.Errorf("projection %s still discloses owner/runtime field %q", projection.Key, key)
			}
		}
		previous, err := json.Marshal(previousAuthoringProjection(definitions[index]))
		if err != nil {
			t.Fatal(err)
		}
		previousBytes += len(previous)
		compactBytes += len(projection.Payload)
	}
	if compactBytes*100 > previousBytes*65 {
		t.Fatalf("authoring projection reduction is too small: previous=%d compact=%d", previousBytes, compactBytes)
	}
	t.Logf("authoring projection bytes: previous=%d compact=%d reduction=%.1f%%", previousBytes, compactBytes, float64(previousBytes-compactBytes)*100/float64(previousBytes))
}

func TestRecordExportCategoryPublishesOnlySemanticDeliveryContract(t *testing.T) {
	binding := runtimeBindingsByKey(t)["records"]
	document, err := binding.CapabilityCategory(t.Context(), "records.export")
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Projections) != 1 || document.Projections[0].Kind != "runtime.api_operations" || document.Projections[0].Key != "record_export" {
		t.Fatalf("record export projections=%+v", document.Projections)
	}
	text := strings.ToLower(string(document.Projections[0].Payload))
	for _, required := range []string{`"record_export"`, `"result_schema":"file"`} {
		if !strings.Contains(text, required) {
			t.Errorf("record export semantic contract is missing %s", required)
		}
	}
	for _, forbidden := range []string{"server_selected", "background", "record_batch_job", "record_export_download", `"202"`, `"method"`, `"path"`, "idempotency", "sse"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("record export semantic contract still discloses %q", forbidden)
		}
	}
}

type previousAuthoringProjectionDocument struct {
	Key                string                                            `json:"key"`
	Status             string                                            `json:"status"`
	Lifecycle          string                                            `json:"lifecycle"`
	AllowedContexts    []string                                          `json:"allowed_contexts,omitempty"`
	Parameters         []capabilitycontract.CapabilityAuthoringParameter `json:"parameters,omitempty"`
	Requires           []string                                          `json:"requires,omitempty"`
	Conflicts          []string                                          `json:"conflicts,omitempty"`
	Errors             []capabilitycontract.CapabilityAuthoringError     `json:"errors,omitempty"`
	Examples           []capabilitycontract.CapabilityAuthoringExample   `json:"examples,omitempty"`
	InputSchema        *capabilitycontract.CapabilityAuthoringSchema     `json:"input_schema,omitempty"`
	OutputSchema       *capabilitycontract.CapabilityAuthoringSchema     `json:"output_schema,omitempty"`
	OutputVariables    []capabilitycontract.CapabilityAuthoringOutput    `json:"output_variables,omitempty"`
	ReferenceContracts []authoringReference                              `json:"reference_contracts,omitempty"`
	Execution          *capabilitycontract.CapabilityAuthoringExecution  `json:"execution,omitempty"`
}

func previousAuthoringProjection(definition capabilitycontract.CapabilityAuthoringDefinition) previousAuthoringProjectionDocument {
	references := make([]authoringReference, 0, len(definition.ReferenceContracts))
	for _, reference := range definition.ReferenceContracts {
		references = append(references, authoringReference{Kind: reference.Kind, InputJSONPointer: reference.InputJSONPointer, ScopeFrom: reference.ScopeFrom})
	}
	return previousAuthoringProjectionDocument{
		Key: definition.Key, Status: definition.Status, Lifecycle: definition.Lifecycle, AllowedContexts: definition.AllowedContexts,
		Parameters: definition.Parameters, Requires: definition.Requires, Conflicts: definition.Conflicts, Errors: definition.Errors, Examples: definition.Examples,
		InputSchema: definition.InputSchema, OutputSchema: definition.OutputSchema, OutputVariables: definition.OutputVariables, ReferenceContracts: references, Execution: definition.Execution,
	}
}

func authoringDefinition(t *testing.T, domain capabilitycontract.CapabilityAuthoringDomain, key string) capabilitycontract.CapabilityAuthoringDefinition {
	t.Helper()
	for _, definition := range domain.Capabilities {
		if definition.Key == key {
			return definition
		}
	}
	t.Fatalf("authoring definition %q is unavailable", key)
	return capabilitycontract.CapabilityAuthoringDefinition{}
}

func TestOwnerValidatorsRejectMalformedAndSemanticInvalidCandidates(t *testing.T) {
	bindings := runtimeBindingsByKey(t)
	tests := []struct {
		name, moduleKey, categoryKey, kind string
		candidate                          modulecapability.AuthoringFragment
		referencedContext                  []modulecapability.AuthoringFragment
		wantRule                           string
	}{
		{name: "schema closed fragment", moduleKey: "runtime_schema", categoryKey: "schema.authoring", kind: "schema.object", candidate: authoringFragment("objects", "order", `{"key":"order","name":"Order","description":"","fields":[],"unknown":true}`), wantRule: "runtime.authoring.fragment_invalid"},
		{name: "schema owner policy", moduleKey: "runtime_schema", categoryKey: "schema.authoring", kind: "schema.object", candidate: authoringFragment("objects", "order", `{"key":"order","name":"","description":"","fields":[]}`), wantRule: "backend.metadata.object_name_required"},
		{name: "action kind", moduleKey: "records", categoryKey: "records.authoring", kind: "action.definition", candidate: authoringFragment("actions", "order.run", `{"key":"order.run","name":"Run","object_key":"order","kind":"script","audit_event":"order_ran"}`), wantRule: "backend.action.kind_invalid"},
		{name: "workflow graph", moduleKey: "workflow", categoryKey: "workflow.authoring", kind: "workflow.definition", candidate: authoringFragment("workflows", "order.complete", `{"key":"order.complete","name":"Complete order","trigger":{},"condition":{},"action":{},"enabled":true,"trigger_contract":{"type":"manual"},"graph":{"version":2,"nodes":[{"id":"same","type":"trigger"},{"id":"same","type":"action","contract":{"action":{"action_key":"order.complete"}}}],"edges":[]}}`), wantRule: "backend.workflow.graph_node_invalid"},
		{name: "automation instruction", moduleKey: "automation", categoryKey: "automation.rules", kind: "automation.rule", candidate: authoringFragment("automation_rules", "order.ready", `{"key":"order.ready","name":"Order ready","object_key":"order","enabled":true,"trigger":{"phase":"after","operation":"update"},"instructions":[{"key":"","type":"emit_event","config":{"event_type":"order.ready"}}]}`), referencedContext: []modulecapability.AuthoringFragment{authoringFragment("objects", "order", `{"key":"order","name":"Order","description":"","fields":[]}`)}, wantRule: "backend.automation.instruction_key_invalid"},
		{name: "profile relation contract", moduleKey: "profile_binding", categoryKey: "profile_binding.authoring", kind: "principal.profile_binding", candidate: authoringFragment("objects", "staff_profile", `{"key":"staff_profile","name":"Staff","description":"","fields":[{"key":"identity_user","name":"Identity","type":"relation","config":{"object_key":"identity_user"},"required":true}],"ux":{"kind":"identity_profile_extension","config":{"identity_relation_field":"identity_user","business_identity":{"key":"staff"}}}}`), wantRule: "backend.identity.profile_binding_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := validateRuntimeCandidate(t, bindings[test.moduleKey], test.categoryKey, test.kind, test.candidate, test.referencedContext)
			if !diagnosticsContainRule(result.Diagnostics, test.wantRule) {
				t.Fatalf("diagnostics=%#v, want rule %q", result.Diagnostics, test.wantRule)
			}
		})
	}
}

func TestOwnerValidatorsAcceptRepresentativeValidCandidates(t *testing.T) {
	bindings := runtimeBindingsByKey(t)
	identityUser := authoringFragment("objects", "identity_user", `{"key":"identity_user","name":"Identity user","description":"","fields":[]}`)
	order := authoringFragment("objects", "order", `{"key":"order","name":"Order","description":"","fields":[]}`)
	action := authoringFragment("actions", "order.complete", `{"key":"order.complete","name":"Complete","object_key":"order","kind":"record_update","audit_event":"order_completed"}`)
	tests := []struct {
		name, moduleKey, categoryKey, kind string
		candidate                          modulecapability.AuthoringFragment
		referencedContext                  []modulecapability.AuthoringFragment
	}{
		{name: "object", moduleKey: "runtime_schema", categoryKey: "schema.authoring", kind: "schema.object", candidate: authoringFragment("objects", "order", `{"key":"order","name":"Order","description":"","fields":[]}`)},
		{name: "object governance", moduleKey: "runtime_schema", categoryKey: "schema.authoring", kind: "schema.object", candidate: authoringFragment("objects", "audit_entry", `{"key":"audit_entry","name":"Audit entry","description":"","fields":[],"capabilities":{"create":true,"read":true,"update":false,"delete":false,"export":true},"lifecycle_policy":{"mode":"append_only"},"ledger_policy":{"integrity":"sha256_chain","signature":"hmac_sha256"},"export_assurance_policy":{"required_methods":["otp"]}}`)},
		{name: "action", moduleKey: "records", categoryKey: "records.authoring", kind: "action.definition", candidate: action, referencedContext: []modulecapability.AuthoringFragment{order}},
		{name: "workflow", moduleKey: "workflow", categoryKey: "workflow.authoring", kind: "workflow.definition", candidate: authoringFragment("workflows", "order.complete", `{"key":"order.complete","name":"Complete order","trigger":{},"condition":{},"action":{},"enabled":true,"trigger_contract":{"type":"manual"},"graph":{"version":2,"nodes":[{"id":"trigger","type":"trigger"},{"id":"action","type":"action","contract":{"action":{"action_key":"order.complete"}}}],"edges":[{"id":"start","source":"trigger","target":"action"}]}}`), referencedContext: []modulecapability.AuthoringFragment{action, order}},
		{name: "automation", moduleKey: "automation", categoryKey: "automation.rules", kind: "automation.rule", candidate: authoringFragment("automation_rules", "order.ready", `{"key":"order.ready","name":"Order ready","object_key":"order","enabled":true,"trigger":{"phase":"after","operation":"update"},"instructions":[{"key":"event","type":"emit_event","config":{"event_type":"order.ready"}}]}`), referencedContext: []modulecapability.AuthoringFragment{order}},
		{name: "profile binding", moduleKey: "profile_binding", categoryKey: "profile_binding.authoring", kind: "principal.profile_binding", candidate: authoringFragment("objects", "staff_profile", `{"key":"staff_profile","name":"Staff","description":"","fields":[{"key":"identity_user","name":"Identity","type":"relation","config":{"object_key":"identity_user"},"required":true,"unique":true}],"ux":{"kind":"identity_profile_extension","config":{"identity_relation_field":"identity_user","business_identity":{"key":"staff"}}}}`), referencedContext: []modulecapability.AuthoringFragment{identityUser}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := validateRuntimeCandidate(t, bindings[test.moduleKey], test.categoryKey, test.kind, test.candidate, test.referencedContext)
			if len(result.Diagnostics) != 0 {
				t.Fatalf("valid candidate diagnostics=%#v", result.Diagnostics)
			}
		})
	}
}

func runtimeBindingsByKey(t *testing.T) map[string]modulecapability.Binding {
	t.Helper()
	bindings, err := Bindings()
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]modulecapability.Binding, len(bindings))
	for _, binding := range bindings {
		summary, summaryErr := binding.CapabilitySummary(t.Context())
		if summaryErr != nil {
			t.Fatal(summaryErr)
		}
		result[summary.Identity.Key] = binding
	}
	return result
}

func validateRuntimeCandidate(t *testing.T, binding modulecapability.Binding, categoryKey, kind string, candidate modulecapability.AuthoringFragment, reference []modulecapability.AuthoringFragment) modulecapability.ValidationResult {
	t.Helper()
	if binding == nil {
		t.Fatal("Runtime binding is unavailable")
	}
	summary, err := binding.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	result, err := binding.ValidateCapabilityCandidate(t.Context(), modulecapability.ValidationRequest{
		ContractVersion: modulecapability.ValidationContractVersion,
		ModuleKey:       summary.Identity.Key, CategoryKey: categoryKey, ContractSHA256: summary.Identity.ContractSHA256,
		Kind: kind, Candidate: candidate, ReferencedContext: reference,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func authoringFragment(collection, key, value string) modulecapability.AuthoringFragment {
	return modulecapability.AuthoringFragment{Collection: collection, Key: key, Value: json.RawMessage(value)}
}

func diagnosticsContainRule(values []modulecapability.Diagnostic, rule string) bool {
	for _, value := range values {
		if value.RuleKey == rule {
			return true
		}
	}
	return false
}

func assertRuntimeCategoryOperations(t *testing.T, binding modulecapability.Binding, summary modulecapability.ModuleSummary) {
	t.Helper()
	for _, category := range summary.Categories {
		document, err := binding.CapabilityCategory(t.Context(), category.Key)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, methods := range document.OpenAPI.Paths {
			for _, raw := range methods {
				count++
				var operation map[string]any
				if err := json.Unmarshal(raw, &operation); err != nil {
					t.Fatal(err)
				}
				for key := range operation {
					if strings.HasPrefix(key, "x-domainry-") && key != modulecapability.OperationExtensionKey {
						t.Fatalf("provider %s category %s retained obsolete extension %s", summary.Identity.Key, category.Key, key)
					}
				}
			}
		}
		if count != category.OperationCount {
			t.Fatalf("provider %s category %s operation count=%d, want %d", summary.Identity.Key, category.Key, count, category.OperationCount)
		}
	}
}
