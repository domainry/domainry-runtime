package provision

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestConfiguringProvisionStartsObjectlessProjectRuntimeAndIsIdempotent(t *testing.T) {
	target := filepath.Join(t.TempDir(), "runtime", "manifest.json")
	activations := 0
	entered := LifecycleState{}
	server := NewServerWithConfiguringLifecycle(target, "dev", testContractIdentity(), func(manifest manifestmodel.ManifestSchema) error {
		activations++
		if manifest.SourceBlueprintID != DirectAuthoringSourceID || len(manifest.Objects) != 0 {
			t.Fatalf("configuring manifest=%#v", manifest)
		}
		return nil
	}, func(state LifecycleState) { entered = state }).Routes()

	payload := map[string]any{"runtime_id": "project-42", "builder_task_id": "task-42"}
	created := provisionRequest(t, server, http.MethodPost, "/provision/configuring", payload)
	if created.Code != http.StatusCreated || !bytes.Contains(created.Body.Bytes(), []byte(`"status":"configuring"`)) {
		t.Fatalf("created status=%d body=%s", created.Code, created.Body.String())
	}
	if activations != 1 || entered.BuilderTaskID != "task-42" || entered.RuntimeID != "project-42" {
		t.Fatalf("activations=%d entered=%#v", activations, entered)
	}
	manifestRaw, err := os.ReadFile(target)
	if err != nil || !bytes.Contains(manifestRaw, []byte(`"source_blueprint_id": "runtime-direct-authoring-v4"`)) {
		t.Fatalf("manifest err=%v body=%s", err, manifestRaw)
	}
	if state, found, err := ReadLifecycle(target); err != nil || !found || state.Status != LifecycleStatusConfiguring || state.SnapshotHash == "" {
		t.Fatalf("state=%#v found=%v err=%v", state, found, err)
	}

	replay := provisionRequest(t, server, http.MethodPost, "/provision/configuring", payload)
	if replay.Code != http.StatusOK || activations != 1 || !bytes.Contains(replay.Body.Bytes(), []byte(`"idempotent":true`)) {
		t.Fatalf("replay status=%d activations=%d body=%s", replay.Code, activations, replay.Body.String())
	}
	conflict := provisionRequest(t, server, http.MethodPost, "/provision/configuring", map[string]any{"runtime_id": "project-42", "builder_task_id": "other"})
	if conflict.Code != http.StatusConflict || !bytes.Contains(conflict.Body.Bytes(), []byte(`runtime.lifecycle_conflict`)) {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	health := provisionRequest(t, server, http.MethodGet, "/health", nil)
	if health.Code != http.StatusOK || !bytes.Contains(health.Body.Bytes(), []byte(`"ready":false`)) || !bytes.Contains(health.Body.Bytes(), []byte(`"status":"configuring"`)) {
		t.Fatalf("health status=%d body=%s", health.Code, health.Body.String())
	}
}

func TestConfiguringProvisionCleansOwnedFilesWhenActivationFails(t *testing.T) {
	target := filepath.Join(t.TempDir(), "runtime", "manifest.json")
	server := NewServerWithConfiguringLifecycle(target, "dev", testContractIdentity(), func(manifestmodel.ManifestSchema) error {
		return errors.New("injected activation failure")
	}, nil).Routes()
	response := provisionRequest(t, server, http.MethodPost, "/provision/configuring", map[string]any{"runtime_id": "project", "builder_task_id": "task"})
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	for _, path := range []string{target, LifecyclePath(target)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("failed configuring artifact remains: %s err=%v", path, err)
		}
	}
}

func TestAuthoringValidationLifecycleIsOwnedAndRepairable(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "runtime.json")
	state := newConfiguringLifecycle("project", "task-1", "snapshot-initial")
	if err := writeLifecycle(manifestPath, state); err != nil {
		t.Fatal(err)
	}
	validating, err := BeginAuthoringValidation(manifestPath, "task-1")
	if err != nil || validating.Status != LifecycleStatusValidating {
		t.Fatalf("validating=%#v err=%v", validating, err)
	}
	failed, err := CompleteAuthoringValidation(manifestPath, "task-1", "snapshot-invalid", false)
	if err != nil || failed.Status != LifecycleStatusConfiguring || failed.SnapshotHash != "snapshot-invalid" {
		t.Fatalf("failed=%#v err=%v", failed, err)
	}
	if _, err := BeginAuthoringValidation(manifestPath, "other-task"); err == nil {
		t.Fatal("another builder task changed the lifecycle")
	}
	if _, err := BeginAuthoringValidation(manifestPath, "task-1"); err != nil {
		t.Fatal(err)
	}
	verified, err := CompleteAuthoringValidation(manifestPath, "task-1", "snapshot-valid", true)
	if err != nil || verified.Status != LifecycleStatusVerifying || verified.SnapshotHash != "snapshot-valid" {
		t.Fatalf("verified=%#v err=%v", verified, err)
	}
	server := NewServerWithConfiguringLifecycle(manifestPath, "dev", testContractIdentity(), nil, nil).Routes()
	lifecycle := provisionRequest(t, server, http.MethodGet, "/provision/lifecycle", nil)
	if lifecycle.Code != http.StatusOK || !bytes.Contains(lifecycle.Body.Bytes(), []byte(`"status":"verifying"`)) || !bytes.Contains(lifecycle.Body.Bytes(), []byte(`"ready":false`)) {
		t.Fatalf("verifying lifecycle became deliverable: status=%d body=%s", lifecycle.Code, lifecycle.Body.String())
	}
	if rejected, err := CompleteAuthoringDelivery(manifestPath, "task-1", "snapshot-valid", false); err != nil || rejected.Status != LifecycleStatusVerifying {
		t.Fatalf("rejected delivery=%#v err=%v", rejected, err)
	}
	ready, err := CompleteAuthoringDelivery(manifestPath, "task-1", "snapshot-valid", true)
	if err != nil || ready.Status != LifecycleStatusReady {
		t.Fatalf("ready delivery=%#v err=%v", ready, err)
	}
	replayed, err := CompleteAuthoringDelivery(manifestPath, "task-1", "snapshot-valid", true)
	if err != nil || replayed != ready {
		t.Fatalf("ready delivery replay=%#v err=%v", replayed, err)
	}
	if _, err := CompleteAuthoringDelivery(manifestPath, "task-1", "different-snapshot", true); err == nil {
		t.Fatal("ready delivery accepted a different snapshot")
	}
	lifecycle = provisionRequest(t, server, http.MethodGet, "/provision/lifecycle", nil)
	if lifecycle.Code != http.StatusOK || !bytes.Contains(lifecycle.Body.Bytes(), []byte(`"status":"ready"`)) || !bytes.Contains(lifecycle.Body.Bytes(), []byte(`"ready":true`)) {
		t.Fatalf("ready lifecycle was not deliverable: status=%d body=%s", lifecycle.Code, lifecycle.Body.String())
	}
}

func TestConfiguringRuntimeAbandonRequiresExactOwnerAndCleansOnlyOwnedFiles(t *testing.T) {
	target := filepath.Join(t.TempDir(), "runtime", "manifest.json")
	stopped := 0
	server := NewServerWithConfiguringLifecycle(target, "dev", testContractIdentity(), func(manifestmodel.ManifestSchema) error {
		return nil
	}, nil).UseAbandonRuntime(func() error {
		stopped++
		return nil
	}).Routes()
	payload := map[string]any{"runtime_id": "project", "builder_task_id": "task"}
	if response := provisionRequest(t, server, http.MethodPost, "/provision/configuring", payload); response.Code != http.StatusCreated {
		t.Fatalf("configure status=%d body=%s", response.Code, response.Body.String())
	}
	mismatch := provisionRequest(t, server, http.MethodPost, "/provision/abandon", map[string]any{"runtime_id": "project", "builder_task_id": "other"})
	if mismatch.Code != http.StatusConflict || stopped != 0 {
		t.Fatalf("mismatch status=%d stopped=%d body=%s", mismatch.Code, stopped, mismatch.Body.String())
	}
	abandoned := provisionRequest(t, server, http.MethodPost, "/provision/abandon", payload)
	if abandoned.Code != http.StatusOK || stopped != 1 || !bytes.Contains(abandoned.Body.Bytes(), []byte(`"status":"abandoned"`)) {
		t.Fatalf("abandon status=%d stopped=%d body=%s", abandoned.Code, stopped, abandoned.Body.String())
	}
	for _, path := range []string{target, LifecyclePath(target)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("abandoned artifact remains: %s err=%v", path, err)
		}
	}
}
