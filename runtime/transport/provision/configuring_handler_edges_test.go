package provision

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func rawProvisionRequest(server http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func TestConfiguringHandlerRejectsMalformedBlankOwnedAndUnreadableStates(t *testing.T) {
	target := filepath.Join(t.TempDir(), "runtime", "manifest.json")
	server := NewServerWithConfiguringLifecycle(target, "dev", testContractIdentity(), nil, nil).Routes()
	if response := rawProvisionRequest(server, http.MethodPost, "/provision/configuring", "{"); response.Code != http.StatusBadRequest {
		t.Fatalf("malformed status=%d body=%s", response.Code, response.Body.String())
	}
	if response := provisionRequest(t, server, http.MethodPost, "/provision/configuring", map[string]any{"runtime_id": "runtime", "builder_task_id": " "}); response.Code != http.StatusBadRequest {
		t.Fatalf("blank builder status=%d body=%s", response.Code, response.Body.String())
	}

	if err := os.MkdirAll(LifecyclePath(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if response := provisionRequest(t, server, http.MethodPost, "/provision/configuring", map[string]any{"runtime_id": "runtime", "builder_task_id": "task"}); response.Code != http.StatusInternalServerError {
		t.Fatalf("unreadable lifecycle status=%d body=%s", response.Code, response.Body.String())
	}

	for _, state := range []LifecycleState{
		{Status: LifecycleStatusValidating, RuntimeID: "runtime", BuilderTaskID: "task"},
		{Status: LifecycleStatusConfiguring, RuntimeID: "other", BuilderTaskID: "task"},
	} {
		root := t.TempDir()
		path := filepath.Join(root, "manifest.json")
		if err := writeLifecycle(path, state); err != nil {
			t.Fatal(err)
		}
		response := provisionRequest(t, NewServerWithConfiguringLifecycle(path, "dev", testContractIdentity(), nil, nil).Routes(), http.MethodPost, "/provision/configuring", map[string]any{"runtime_id": "runtime", "builder_task_id": "task"})
		if response.Code != http.StatusConflict {
			t.Fatalf("state=%#v status=%d body=%s", state, response.Code, response.Body.String())
		}
	}
}

func TestConfiguringHandlerCoversExistingUnreadableAndWriteFailurePaths(t *testing.T) {
	create := func(target string) *httptest.ResponseRecorder {
		return provisionRequest(t, NewServerWithConfiguringLifecycle(target, "dev", testContractIdentity(), nil, nil).Routes(), http.MethodPost, "/provision/configuring", map[string]any{"runtime_id": "runtime", "builder_task_id": "task"})
	}
	t.Run("existing manifest", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "manifest.json")
		if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if response := create(target); response.Code != http.StatusConflict {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
	t.Run("unreadable manifest stat", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "loop")
		if err := os.Symlink(target, target); err != nil {
			t.Fatal(err)
		}
		if response := create(target); response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
	t.Run("manifest write", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "manifest.json")
		server := NewServerWithConfiguringLifecycle(target, "dev", testContractIdentity(), nil, nil)
		server.writeConfiguringManifest = func(string, manifestmodel.ManifestSchema) error { return errors.New("write failed") }
		if response := provisionRequest(t, server.Routes(), http.MethodPost, "/provision/configuring", map[string]any{"runtime_id": "runtime", "builder_task_id": "task"}); response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
	t.Run("lifecycle write", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "manifest.json")
		server := NewServerWithConfiguringLifecycle(target, "dev", testContractIdentity(), nil, nil)
		server.writeConfiguringLifecycle = func(string, LifecycleState) error { return errors.New("write failed") }
		if response := provisionRequest(t, server.Routes(), http.MethodPost, "/provision/configuring", map[string]any{"runtime_id": "runtime", "builder_task_id": "task"}); response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("manifest cleanup err=%v", err)
		}
	})
	t.Run("nil callbacks", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "manifest.json")
		if response := create(target); response.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
}

func TestLifecycleHandlerCoversUnreadableMissingAndFoundStates(t *testing.T) {
	target := filepath.Join(t.TempDir(), "manifest.json")
	server := NewServerWithConfiguringLifecycle(target, "dev", testContractIdentity(), nil, nil).Routes()
	if response := provisionRequest(t, server, http.MethodGet, "/provision/lifecycle", nil); response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"status":"provisioning"`)) {
		t.Fatalf("missing status=%d body=%s", response.Code, response.Body.String())
	}
	if err := os.MkdirAll(LifecyclePath(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if response := provisionRequest(t, server, http.MethodGet, "/provision/lifecycle", nil); response.Code != http.StatusInternalServerError {
		t.Fatalf("unreadable status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAbandonConfiguringCoversRequestOwnershipStatusManifestAndShutdownFailures(t *testing.T) {
	request := map[string]any{"runtime_id": "runtime", "builder_task_id": "task"}
	makeOwned := func(t *testing.T, status string, withManifest bool) (string, *Server) {
		t.Helper()
		target := filepath.Join(t.TempDir(), "manifest.json")
		if withManifest {
			manifest := manifestmodel.ManifestSchema{SchemaVersion: manifestmodel.CurrentManifestSchemaVersion, SourceBlueprintID: DirectAuthoringSourceID}
			if err := writeManifestAtomic(target, manifest); err != nil {
				t.Fatal(err)
			}
		}
		if err := writeLifecycle(target, LifecycleState{Status: status, RuntimeID: "runtime", BuilderTaskID: "task"}); err != nil {
			t.Fatal(err)
		}
		return target, NewServerWithConfiguringLifecycle(target, "dev", testContractIdentity(), nil, nil)
	}

	base := NewServerWithConfiguringLifecycle(filepath.Join(t.TempDir(), "missing.json"), "dev", testContractIdentity(), nil, nil).Routes()
	if response := rawProvisionRequest(base, http.MethodPost, "/provision/abandon", "{"); response.Code != http.StatusBadRequest {
		t.Fatalf("malformed status=%d", response.Code)
	}
	for _, body := range []map[string]any{{"runtime_id": "", "builder_task_id": "task"}, {"runtime_id": "runtime", "builder_task_id": ""}} {
		if response := provisionRequest(t, base, http.MethodPost, "/provision/abandon", body); response.Code != http.StatusBadRequest {
			t.Fatalf("blank body=%v status=%d", body, response.Code)
		}
	}
	if response := provisionRequest(t, base, http.MethodPost, "/provision/abandon", request); response.Code != http.StatusConflict {
		t.Fatalf("missing lifecycle status=%d", response.Code)
	}

	t.Run("unreadable lifecycle", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "manifest.json")
		if err := os.MkdirAll(LifecyclePath(target), 0o755); err != nil {
			t.Fatal(err)
		}
		response := provisionRequest(t, NewServerWithConfiguringLifecycle(target, "dev", testContractIdentity(), nil, nil).Routes(), http.MethodPost, "/provision/abandon", request)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
	for _, state := range []LifecycleState{{Status: LifecycleStatusConfiguring, RuntimeID: "other", BuilderTaskID: "task"}, {Status: LifecycleStatusConfiguring, RuntimeID: "runtime", BuilderTaskID: "other"}, {Status: LifecycleStatusReady, RuntimeID: "runtime", BuilderTaskID: "task"}} {
		t.Run(state.Status+state.RuntimeID+state.BuilderTaskID, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "manifest.json")
			if err := writeLifecycle(target, state); err != nil {
				t.Fatal(err)
			}
			response := provisionRequest(t, NewServerWithConfiguringLifecycle(target, "dev", testContractIdentity(), nil, nil).Routes(), http.MethodPost, "/provision/abandon", request)
			if response.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	for _, status := range []string{LifecycleStatusValidating, LifecycleStatusVerifying} {
		t.Run(status, func(t *testing.T) {
			_, server := makeOwned(t, status, false)
			if response := provisionRequest(t, server.Routes(), http.MethodPost, "/provision/abandon", request); response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	t.Run("manifest unreadable", func(t *testing.T) {
		target, server := makeOwned(t, LifecycleStatusConfiguring, false)
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if response := provisionRequest(t, server.Routes(), http.MethodPost, "/provision/abandon", request); response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
	for name, contents := range map[string]string{"invalid": "{", "not owned": `{"source_blueprint_id":"other"}`} {
		t.Run(name, func(t *testing.T) {
			target, server := makeOwned(t, LifecycleStatusConfiguring, false)
			if err := os.WriteFile(target, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if response := provisionRequest(t, server.Routes(), http.MethodPost, "/provision/abandon", request); response.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	t.Run("shutdown failure", func(t *testing.T) {
		_, server := makeOwned(t, LifecycleStatusConfiguring, true)
		server.UseAbandonRuntime(func() error { return errors.New("stop failed") })
		if response := provisionRequest(t, server.Routes(), http.MethodPost, "/provision/abandon", request); response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
	t.Run("manifest cleanup failure", func(t *testing.T) {
		target, server := makeOwned(t, LifecycleStatusConfiguring, true)
		server.UseAbandonRuntime(func() error {
			if err := os.Remove(target); err != nil {
				return err
			}
			if err := os.Mkdir(target, 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(target, "owned"), []byte("x"), 0o600)
		})
		if response := provisionRequest(t, server.Routes(), http.MethodPost, "/provision/abandon", request); response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
	t.Run("lifecycle cleanup failure", func(t *testing.T) {
		target, server := makeOwned(t, LifecycleStatusConfiguring, true)
		server.UseAbandonRuntime(func() error {
			path := LifecyclePath(target)
			if err := os.Remove(path); err != nil {
				return err
			}
			if err := os.Mkdir(path, 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(path, "owned"), []byte("x"), 0o600)
		})
		if response := provisionRequest(t, server.Routes(), http.MethodPost, "/provision/abandon", request); response.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
	t.Run("already missing lifecycle cleanup", func(t *testing.T) {
		target, server := makeOwned(t, LifecycleStatusConfiguring, true)
		server.UseAbandonRuntime(func() error { return os.Remove(LifecyclePath(target)) })
		if response := provisionRequest(t, server.Routes(), http.MethodPost, "/provision/abandon", request); response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
}
