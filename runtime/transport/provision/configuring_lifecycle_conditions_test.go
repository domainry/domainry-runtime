package provision

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type lifecycleTemporaryFileStub struct {
	name     string
	writeErr error
	chmodErr error
	closeErr error
}

func (file *lifecycleTemporaryFileStub) Name() string              { return file.name }
func (file *lifecycleTemporaryFileStub) Write([]byte) (int, error) { return 0, file.writeErr }
func (file *lifecycleTemporaryFileStub) Chmod(os.FileMode) error   { return file.chmodErr }
func (file *lifecycleTemporaryFileStub) Close() error              { return file.closeErr }

func TestConfiguringLifecycleCoversGenericStateTransitions(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "runtime.json")
	if state, found, err := ReadLifecycle(manifestPath); err != nil || found || state != (LifecycleState{}) {
		t.Fatalf("missing state=%#v found=%v err=%v", state, found, err)
	}
	if err := os.WriteFile(LifecyclePath(manifestPath), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadLifecycle(manifestPath); err == nil {
		t.Fatal("malformed lifecycle accepted")
	}
	if err := os.Remove(LifecyclePath(manifestPath)); err != nil {
		t.Fatal(err)
	}
	state := newConfiguringLifecycle("runtime", "task", "snapshot-1")
	if err := writeLifecycle(manifestPath, state); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginAuthoringValidation(manifestPath, " "); err == nil {
		t.Fatal("blank owner accepted")
	}
	if _, err := BeginAuthoringValidation(manifestPath, "other"); err == nil {
		t.Fatal("different owner accepted")
	}
	validating, err := BeginAuthoringValidation(manifestPath, "task")
	if err != nil || validating.Status != LifecycleStatusValidating {
		t.Fatalf("validating=%#v err=%v", validating, err)
	}
	verifying, err := CompleteAuthoringValidation(manifestPath, "task", "snapshot-2", true)
	if err != nil || verifying.Status != LifecycleStatusVerifying || verifying.SnapshotHash != "snapshot-2" {
		t.Fatalf("verifying=%#v err=%v", verifying, err)
	}
	configuring, err := CompleteAuthoringValidation(manifestPath, "task", "", false)
	if err != nil || configuring.Status != LifecycleStatusConfiguring || configuring.SnapshotHash != "snapshot-2" {
		t.Fatalf("configuring=%#v err=%v", configuring, err)
	}
	state.Status = LifecycleStatusReady
	if err := writeLifecycle(manifestPath, state); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginAuthoringValidation(manifestPath, "task"); err == nil {
		t.Fatal("ready lifecycle re-entered validation")
	}
	if _, err := BeginAuthoringValidation(filepath.Join(t.TempDir(), "missing.json"), "task"); err == nil {
		t.Fatal("missing lifecycle entered validation")
	}
}

func TestConfiguringLifecycleCoversGenericFilesystemAndIdentifierEdges(t *testing.T) {
	directoryManifest := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.Mkdir(LifecyclePath(directoryManifest), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadLifecycle(directoryManifest); err == nil {
		t.Fatal("directory lifecycle path read succeeded")
	}
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeLifecycle(filepath.Join(blocker, "runtime.json"), newConfiguringLifecycle("runtime", "task", "snapshot")); err == nil {
		t.Fatal("write through file parent succeeded")
	}
	targetDirectory := filepath.Join(t.TempDir(), "runtime.json.lifecycle.json")
	if err := os.Mkdir(targetDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeLifecycle(strings.TrimSuffix(targetDirectory, ".lifecycle.json"), newConfiguringLifecycle("runtime", "task", "snapshot")); err == nil {
		t.Fatal("rename over lifecycle directory succeeded")
	}

	if got := configuringRuntimeID(" azAZ09-_:[]{} "); got != "azAZ09-_" {
		t.Fatalf("runtime id=%q", got)
	}
	if got := configuringRuntimeID(" !@# "); got != "runtime" {
		t.Fatalf("fallback runtime id=%q", got)
	}
}

func TestConfiguringLifecycleCoversDeterministicFilesystemFailures(t *testing.T) {
	sentinel := errors.New("filesystem failure")
	baseOperations := func(file lifecycleTemporaryFile) lifecycleWriteOperations {
		return lifecycleWriteOperations{
			mkdirAll: func(string, os.FileMode) error { return nil },
			createTemp: func(string, string) (lifecycleTemporaryFile, error) {
				return file, nil
			},
			remove: func(string) error { return nil },
			rename: func(string, string) error { return nil },
		}
	}
	state := newConfiguringLifecycle("runtime", "task", "snapshot")
	cases := []struct {
		name       string
		operations lifecycleWriteOperations
	}{
		{name: "mkdir", operations: func() lifecycleWriteOperations {
			operations := baseOperations(&lifecycleTemporaryFileStub{name: "temporary"})
			operations.mkdirAll = func(string, os.FileMode) error { return sentinel }
			return operations
		}()},
		{name: "create", operations: func() lifecycleWriteOperations {
			operations := baseOperations(&lifecycleTemporaryFileStub{name: "temporary"})
			operations.createTemp = func(string, string) (lifecycleTemporaryFile, error) { return nil, sentinel }
			return operations
		}()},
		{name: "write", operations: baseOperations(&lifecycleTemporaryFileStub{name: "temporary", writeErr: sentinel})},
		{name: "chmod", operations: baseOperations(&lifecycleTemporaryFileStub{name: "temporary", chmodErr: sentinel})},
		{name: "close", operations: baseOperations(&lifecycleTemporaryFileStub{name: "temporary", closeErr: sentinel})},
		{name: "rename", operations: func() lifecycleWriteOperations {
			operations := baseOperations(&lifecycleTemporaryFileStub{name: "temporary"})
			operations.rename = func(string, string) error { return sentinel }
			return operations
		}()},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := writeLifecycleWithOperations("runtime.json", state, test.operations); !errors.Is(err, sentinel) {
				t.Fatalf("err=%v", err)
			}
		})
	}

	manifestPath := filepath.Join(t.TempDir(), "runtime.json")
	if err := writeLifecycle(manifestPath, state); err != nil {
		t.Fatal(err)
	}
	if _, err := transitionAuthoringValidationWithWriter(manifestPath, "task", "snapshot-2", LifecycleStatusValidating, func(string, LifecycleState) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("transition write err=%v", err)
	}
	directoryManifest := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.Mkdir(LifecyclePath(directoryManifest), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginAuthoringValidation(directoryManifest, "task"); err == nil {
		t.Fatal("transition ignored lifecycle read failure")
	}
}
