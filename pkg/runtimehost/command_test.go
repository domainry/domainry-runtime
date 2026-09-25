package runtimehost

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
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

func TestRunCheckCommandWritesStructuredResultWithoutServing(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	checked := false

	exitCode := runCheckCommand(
		nil,
		&stdout,
		&stderr,
		func() ProjectCheckResult {
			checked = true
			return ProjectCheckResult{State: projectCheckStateValid, ModelContentHash: "model-hash", Diagnostics: []ProjectCheckDiagnostic{}}
		},
		func() ProjectCheckResult { return ProjectCheckResult{} },
	)

	if exitCode != 0 || !checked || stderr.Len() != 0 {
		t.Fatalf("exit code=%d checked=%t stderr=%q", exitCode, checked, stderr.String())
	}
	var result ProjectCheckResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.State != projectCheckStateValid || result.ModelContentHash != "model-hash" || len(result.Diagnostics) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunCheckCommandReturnsInvalidProjectExitCode(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runCheckCommand(
		nil,
		&stdout,
		&stderr,
		func() ProjectCheckResult {
			return ProjectCheckResult{State: projectCheckStateInvalid, Diagnostics: []ProjectCheckDiagnostic{{
				Code: "project_model.project_key_invalid", Severity: "error", Path: "/project/key",
				Message: "invalid project key", Owner: "project_model", Category: "semantic",
			}}}
		},
		func() ProjectCheckResult { return ProjectCheckResult{} },
	)

	if exitCode != 1 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"path":"/project/key"`) {
		t.Fatalf("exit code=%d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
}

func TestRunCheckCommandSelectsModelOnlyValidation(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	projectChecked := false
	modelChecked := false

	exitCode := runCheckCommand(
		[]string{"--model-only"},
		&stdout,
		&stderr,
		func() ProjectCheckResult {
			projectChecked = true
			return ProjectCheckResult{}
		},
		func() ProjectCheckResult {
			modelChecked = true
			return ProjectCheckResult{State: projectCheckStateValid, Diagnostics: []ProjectCheckDiagnostic{}}
		},
	)

	if exitCode != 0 || projectChecked || !modelChecked || stderr.Len() != 0 {
		t.Fatalf("exit code=%d projectChecked=%t modelChecked=%t stderr=%q", exitCode, projectChecked, modelChecked, stderr.String())
	}
}
