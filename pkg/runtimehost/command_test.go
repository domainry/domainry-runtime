package runtimehost

import (
	"bytes"
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
