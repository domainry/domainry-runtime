package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestRunRendersHelpWithoutStartingRuntime(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exitCode := run([]string{"--help"}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("help exit code=%d stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage") {
		t.Fatalf("help output=%q", stdout.String())
	}
}

func TestMainDelegatesExitCode(t *testing.T) {
	previousArgs, previousExit := os.Args, runtimeExit
	os.Args = []string{"runtime", "--help"}
	called := false
	runtimeExit = func(code int) {
		called = true
		if code != 0 {
			t.Fatalf("exit code=%d", code)
		}
	}
	t.Cleanup(func() { os.Args, runtimeExit = previousArgs, previousExit })
	main()
	if !called {
		t.Fatal("runtime exit was not called")
	}
}
