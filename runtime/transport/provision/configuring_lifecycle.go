package provision

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var lifecyclePathLocks sync.Map

const (
	LifecycleStatusConfiguring = "configuring"
	LifecycleStatusValidating  = "validating"
	LifecycleStatusVerifying   = "verifying"
	LifecycleStatusReady       = "ready"
	DirectAuthoringSourceID    = "runtime-direct-authoring-v4"
)

type LifecycleState struct {
	Version       string `json:"version"`
	Status        string `json:"status"`
	RuntimeID     string `json:"runtime_id"`
	BuilderTaskID string `json:"builder_task_id"`
	SnapshotHash  string `json:"snapshot_hash"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type lifecycleTemporaryFile interface {
	Name() string
	Write([]byte) (int, error)
	Chmod(os.FileMode) error
	Close() error
}

type lifecycleWriteOperations struct {
	mkdirAll   func(string, os.FileMode) error
	createTemp func(string, string) (lifecycleTemporaryFile, error)
	remove     func(string) error
	rename     func(string, string) error
}

func LifecyclePath(manifestPath string) string { return manifestPath + ".lifecycle.json" }

func lifecyclePathLock(manifestPath string) *sync.Mutex {
	key := filepath.Clean(strings.TrimSpace(manifestPath))
	value, _ := lifecyclePathLocks.LoadOrStore(key, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func ReadLifecycle(manifestPath string) (LifecycleState, bool, error) {
	raw, err := os.ReadFile(LifecyclePath(manifestPath))
	if os.IsNotExist(err) {
		return LifecycleState{}, false, nil
	}
	if err != nil {
		return LifecycleState{}, false, err
	}
	var state LifecycleState
	if err := json.Unmarshal(raw, &state); err != nil {
		return LifecycleState{}, false, fmt.Errorf("decode Runtime lifecycle: %w", err)
	}
	return state, true, nil
}

func writeLifecycle(manifestPath string, state LifecycleState) error {
	return writeLifecycleWithOperations(manifestPath, state, lifecycleWriteOperations{
		mkdirAll: os.MkdirAll,
		createTemp: func(directory, pattern string) (lifecycleTemporaryFile, error) {
			return os.CreateTemp(directory, pattern)
		},
		remove: os.Remove,
		rename: os.Rename,
	})
}

func writeLifecycleWithOperations(manifestPath string, state LifecycleState, operations lifecycleWriteOperations) error {
	raw, _ := json.MarshalIndent(state, "", "  ") // LifecycleState is JSON-safe by construction.
	raw = append(raw, '\n')
	target := LifecyclePath(manifestPath)
	if err := operations.mkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	temporary, err := operations.createTemp(filepath.Dir(target), ".runtime-lifecycle-*.json")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer operations.remove(name)
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return operations.rename(name, target)
}

func configuringRuntimeID(value string) string {
	value = strings.TrimSpace(value)
	var result strings.Builder
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			result.WriteRune(character)
		}
	}
	if result.Len() == 0 {
		return "runtime"
	}
	return result.String()
}

func newConfiguringLifecycle(runtimeID, builderTaskID, snapshotHash string) LifecycleState {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return LifecycleState{Version: "runtime-lifecycle-v1", Status: LifecycleStatusConfiguring, RuntimeID: runtimeID, BuilderTaskID: builderTaskID, SnapshotHash: snapshotHash, CreatedAt: now, UpdatedAt: now}
}

func BeginAuthoringValidation(manifestPath, builderTaskID string) (LifecycleState, error) {
	return transitionAuthoringValidation(manifestPath, builderTaskID, "", LifecycleStatusValidating)
}

func CompleteAuthoringValidation(manifestPath, builderTaskID, snapshotHash string, valid bool) (LifecycleState, error) {
	target := LifecycleStatusConfiguring
	if valid {
		target = LifecycleStatusVerifying
	}
	return transitionAuthoringValidation(manifestPath, builderTaskID, snapshotHash, target)
}

func CompleteAuthoringDelivery(manifestPath, builderTaskID, snapshotHash string, valid bool) (LifecycleState, error) {
	target := LifecycleStatusVerifying
	if valid {
		target = LifecycleStatusReady
	}
	return transitionAuthoringValidation(manifestPath, builderTaskID, snapshotHash, target)
}

func transitionAuthoringValidation(manifestPath, builderTaskID, snapshotHash, target string) (LifecycleState, error) {
	return transitionAuthoringValidationWithWriter(manifestPath, builderTaskID, snapshotHash, target, writeLifecycle)
}

func transitionAuthoringValidationWithWriter(manifestPath, builderTaskID, snapshotHash, target string, writer func(string, LifecycleState) error) (LifecycleState, error) {
	lock := lifecyclePathLock(manifestPath)
	lock.Lock()
	defer lock.Unlock()
	state, found, err := ReadLifecycle(manifestPath)
	if err != nil {
		return LifecycleState{}, err
	}
	if !found {
		return LifecycleState{}, fmt.Errorf("Runtime authoring lifecycle not found")
	}
	if strings.TrimSpace(builderTaskID) == "" || state.BuilderTaskID != strings.TrimSpace(builderTaskID) {
		return LifecycleState{}, fmt.Errorf("Runtime authoring lifecycle is owned by another task")
	}
	if target == LifecycleStatusReady && state.Status == LifecycleStatusReady {
		if strings.TrimSpace(snapshotHash) == "" || state.SnapshotHash != strings.TrimSpace(snapshotHash) {
			return LifecycleState{}, fmt.Errorf("Runtime ready lifecycle snapshot does not match delivery verification")
		}
		return state, nil
	}
	if state.Status != LifecycleStatusConfiguring && state.Status != LifecycleStatusValidating && state.Status != LifecycleStatusVerifying {
		return LifecycleState{}, fmt.Errorf("Runtime lifecycle %q cannot enter authoring validation", state.Status)
	}
	if target == LifecycleStatusReady && state.Status != LifecycleStatusVerifying {
		return LifecycleState{}, fmt.Errorf("Runtime lifecycle %q cannot enter ready before verification", state.Status)
	}
	state.Status = target
	if strings.TrimSpace(snapshotHash) != "" {
		state.SnapshotHash = strings.TrimSpace(snapshotHash)
	}
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := writer(manifestPath, state); err != nil {
		return LifecycleState{}, err
	}
	return state, nil
}
