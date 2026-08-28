package integrationtest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type gymCapabilityCoverageInventory struct {
	SchemaVersion          string                       `json:"schema_version"`
	Source                 gymCapabilityCoverageSource  `json:"source"`
	AllowedSupportStatuses []string                     `json:"allowed_support_statuses"`
	Capabilities           []gymCapabilityCoverageOwner `json:"capabilities"`
	Entities               []gymCapabilityCoverageItem  `json:"entities"`
	StateMachines          []gymCapabilityStateMachine  `json:"state_machines"`
	Workflows              []gymCapabilityCoverageItem  `json:"workflows"`
	E2ECases               []gymCapabilityE2ECase       `json:"e2e_cases"`
	Constraints            []gymCapabilityConstraint    `json:"constraints"`
}

type gymCapabilityCoverageSource struct {
	Document string `json:"document"`
	Version  string `json:"version"`
	Date     string `json:"date"`
}

type gymCapabilityCoverageOwner struct {
	Key               string                        `json:"key"`
	SupportStatus     string                        `json:"support_status"`
	AuthoringContract string                        `json:"authoring_contract"`
	ValidationOwner   string                        `json:"validation_owner"`
	ExecutionOwner    string                        `json:"execution_owner"`
	PersistenceOwner  string                        `json:"persistence_owner"`
	AuditOwner        string                        `json:"audit_owner"`
	FailureSemantics  gymCapabilityFailureSemantics `json:"failure_semantics"`
}

type gymCapabilityFailureSemantics struct {
	Code       string `json:"code"`
	HTTPStatus int    `json:"http_status"`
}

type gymCapabilityCoverageItem struct {
	Key             string `json:"key"`
	CapabilityOwner string `json:"capability_owner"`
	SupportStatus   string `json:"support_status"`
	E2ECase         string `json:"e2e_case"`
}

type gymCapabilityStateMachine struct {
	Key             string   `json:"key"`
	States          []string `json:"states"`
	CapabilityOwner string   `json:"capability_owner"`
	SupportStatus   string   `json:"support_status"`
	E2ECase         string   `json:"e2e_case"`
}

type gymCapabilityE2ECase struct {
	ID              string `json:"id"`
	CapabilityOwner string `json:"capability_owner"`
	Title           string `json:"title"`
}

type gymCapabilityConstraint struct {
	ID              string `json:"id"`
	CapabilityOwner string `json:"capability_owner"`
	SupportStatus   string `json:"support_status"`
	E2ECase         string `json:"e2e_case"`
}

func TestGymCapabilityInventoryOwnsEveryPRDRequirement(t *testing.T) {
	inventory := readGymCapabilityCoverageInventory(t)
	if inventory.SchemaVersion != "runtime-business-capability-coverage-v1" {
		t.Fatalf("schema_version=%q", inventory.SchemaVersion)
	}

	allowedStatuses := gymStringSet(inventory.AllowedSupportStatuses)
	wantStatuses := gymStringSet([]string{"supported", "supported_with_modeling", "runtime_gap", "external_connector"})
	if !reflect.DeepEqual(allowedStatuses, wantStatuses) {
		t.Fatalf("allowed_support_statuses=%v", inventory.AllowedSupportStatuses)
	}

	owners := map[string]gymCapabilityCoverageOwner{}
	for _, owner := range inventory.Capabilities {
		if _, duplicate := owners[owner.Key]; duplicate {
			t.Errorf("duplicate capability owner %q", owner.Key)
		}
		owners[owner.Key] = owner
		if owner.Key == "" || !allowedStatuses[owner.SupportStatus] || owner.AuthoringContract == "" || owner.ValidationOwner == "" || owner.ExecutionOwner == "" || owner.PersistenceOwner == "" || owner.AuditOwner == "" || owner.FailureSemantics.Code == "" || owner.FailureSemantics.HTTPStatus < 400 || owner.FailureSemantics.HTTPStatus > 599 {
			t.Errorf("incomplete capability owner: %#v", owner)
		}
		if strings.HasPrefix(owner.AuthoringContract, "action.step.") {
			t.Errorf("capability owner %q still references retired Action Step authoring contract %q", owner.Key, owner.AuthoringContract)
		}
	}

	e2eCases := map[string]bool{}
	for _, testCase := range inventory.E2ECases {
		if e2eCases[testCase.ID] {
			t.Errorf("duplicate E2E case %q", testCase.ID)
		}
		e2eCases[testCase.ID] = true
		assertGymCapabilityOwnerExists(t, owners, testCase.CapabilityOwner)
		if testCase.Title == "" {
			t.Errorf("E2E case %q has no title", testCase.ID)
		}
	}
	for index := 1; index <= 14; index++ {
		id := fmt.Sprintf("GYM-%02d", index)
		if !e2eCases[id] {
			t.Errorf("missing E2E case %q", id)
		}
	}

	assertGymCoverageItems(t, "entity", inventory.Entities, owners, e2eCases)
	assertGymCoverageItems(t, "workflow", inventory.Workflows, owners, e2eCases)
	stateItems := make([]gymCapabilityCoverageItem, 0, len(inventory.StateMachines))
	for _, machine := range inventory.StateMachines {
		if len(machine.States) == 0 || len(gymStringSet(machine.States)) != len(machine.States) {
			t.Errorf("state machine %q has empty or duplicate states: %v", machine.Key, machine.States)
		}
		stateItems = append(stateItems, gymCapabilityCoverageItem{Key: machine.Key, CapabilityOwner: machine.CapabilityOwner, SupportStatus: machine.SupportStatus, E2ECase: machine.E2ECase})
	}
	assertGymCoverageItems(t, "state_machine", stateItems, owners, e2eCases)

	wantConstraintIDs := gymPRDConstraintIDs()
	gotConstraintIDs := make([]string, 0, len(inventory.Constraints))
	seenConstraints := map[string]bool{}
	for _, constraint := range inventory.Constraints {
		if seenConstraints[constraint.ID] {
			t.Errorf("duplicate constraint %q", constraint.ID)
		}
		seenConstraints[constraint.ID] = true
		gotConstraintIDs = append(gotConstraintIDs, constraint.ID)
		assertGymCoverageReference(t, constraint.ID, constraint.CapabilityOwner, constraint.SupportStatus, constraint.E2ECase, owners, e2eCases)
	}
	sort.Strings(gotConstraintIDs)
	sort.Strings(wantConstraintIDs)
	if !reflect.DeepEqual(gotConstraintIDs, wantConstraintIDs) {
		t.Fatalf("constraint inventory mismatch\ngot:  %v\nwant: %v", gotConstraintIDs, wantConstraintIDs)
	}
}

func readGymCapabilityCoverageInventory(t *testing.T) gymCapabilityCoverageInventory {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", "gym", "gym_capability_coverage_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var inventory gymCapabilityCoverageInventory
	if err := decoder.Decode(&inventory); err != nil {
		t.Fatal(err)
	}
	return inventory
}

func assertGymCoverageItems(t *testing.T, kind string, items []gymCapabilityCoverageItem, owners map[string]gymCapabilityCoverageOwner, e2eCases map[string]bool) {
	t.Helper()
	seen := map[string]bool{}
	for _, item := range items {
		if item.Key == "" || seen[item.Key] {
			t.Errorf("%s has empty or duplicate key %q", kind, item.Key)
		}
		seen[item.Key] = true
		assertGymCoverageReference(t, kind+":"+item.Key, item.CapabilityOwner, item.SupportStatus, item.E2ECase, owners, e2eCases)
	}
	if len(items) == 0 {
		t.Errorf("%s inventory is empty", kind)
	}
}

func assertGymCoverageReference(t *testing.T, item, ownerKey, status, e2eCase string, owners map[string]gymCapabilityCoverageOwner, e2eCases map[string]bool) {
	t.Helper()
	owner := assertGymCapabilityOwnerExists(t, owners, ownerKey)
	if owner.SupportStatus != status {
		t.Errorf("%s status=%q does not match owner %q status=%q", item, status, ownerKey, owner.SupportStatus)
	}
	if !e2eCases[e2eCase] {
		t.Errorf("%s references unknown E2E case %q", item, e2eCase)
	}
}

func assertGymCapabilityOwnerExists(t *testing.T, owners map[string]gymCapabilityCoverageOwner, ownerKey string) gymCapabilityCoverageOwner {
	t.Helper()
	owner, ok := owners[ownerKey]
	if !ok {
		t.Errorf("unknown capability owner %q", ownerKey)
	}
	return owner
}

func gymPRDConstraintIDs() []string {
	result := []string{}
	for _, group := range []struct {
		prefix string
		count  int
	}{{"C", 12}, {"T", 10}, {"U", 10}, {"P", 25}, {"F", 8}} {
		for index := 1; index <= group.count; index++ {
			result = append(result, fmt.Sprintf("%s-%02d", group.prefix, index))
		}
	}
	return result
}

func gymStringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[strings.TrimSpace(value)] = true
	}
	return result
}
