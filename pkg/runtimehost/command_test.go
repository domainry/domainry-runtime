package runtimehost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func TestRunCommandHelpDoesNotStartRuntime(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	started := false

	exitCode := runCommand([]string{"--help"}, &stdout, &stderr, func() error {
		started = true
		return nil
	})

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if started {
		t.Fatal("runtime started while rendering help")
	}
	if !strings.Contains(stdout.String(), "Usage: domainry-runtime") {
		t.Fatalf("stdout = %q, want usage", stdout.String())
	}
}

func TestRunCommandRejectsArgumentsWithoutStartingRuntime(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	started := false

	exitCode := runCommand([]string{"unexpected"}, &stdout, &stderr, func() error {
		started = true
		return nil
	})

	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2", exitCode)
	}
	if started {
		t.Fatal("runtime started after invalid arguments")
	}
	if !strings.Contains(stderr.String(), "unexpected arguments") {
		t.Fatalf("stderr = %q, want argument diagnostic", stderr.String())
	}
}

func TestRunCommandReturnsRuntimeFailure(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runCommand(nil, &stdout, &stderr, func() error {
		return errors.New("boom")
	})

	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr.String(), "boom") {
		t.Fatalf("stderr = %q, want runtime failure", stderr.String())
	}
}

func TestRunCommandPrintsDefinitionUpgradePlanAndExitsZero(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	plan := appschemamodel.ApplicationSchemaUpgradePlan{
		ContractVersion: appschemamodel.ApplicationSchemaUpgradePlanContractVersion, FromVersion: "1", ToVersion: "2",
		Steps:       []appschemamodel.ApplicationSchemaUpgradeStep{{ApplicationSchemaMigrationStep: appschemamodel.ApplicationSchemaMigrationStep{ObjectKey: "customer", Table: "customer", Operation: "add_column", ColumnKey: "tier"}, Classification: "compatible"}},
		Diagnostics: []appschemamodel.ApplicationSchemaUpgradeDiagnostic{},
	}
	exitCode := runCommand(nil, &stdout, &stderr, func() error {
		return fmt.Errorf("Runtime bootstrap failed after manifest Provision: %w", &bootstrap.DefinitionUpgradePlanRequested{Plan: plan})
	})
	if exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("exit code = %d stderr=%q", exitCode, stderr.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout.String())
	}
	if decoded["contract_version"] != appschemamodel.ApplicationSchemaUpgradePlanContractVersion || decoded["from_version"] != "1" || decoded["to_version"] != "2" || decoded["blocking"] != false {
		t.Fatalf("plan document=%s", stdout.String())
	}
	steps, _ := decoded["steps"].([]any)
	if len(steps) != 1 || strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("plan document=%s", stdout.String())
	}
}
