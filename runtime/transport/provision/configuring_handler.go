package provision

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

type configuringRequest struct {
	RuntimeID     string `json:"runtime_id"`
	BuilderTaskID string `json:"builder_task_id"`
}

type abandonConfiguringRequest struct {
	RuntimeID     string `json:"runtime_id"`
	BuilderTaskID string `json:"builder_task_id"`
}

func (s *Server) configuring(w http.ResponseWriter, r *http.Request) {
	var request configuringRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.writeError(w, http.StatusBadRequest, "runtime.configuring_request_invalid", err.Error())
		return
	}
	request.RuntimeID = configuringRuntimeID(request.RuntimeID)
	request.BuilderTaskID = strings.TrimSpace(request.BuilderTaskID)
	if request.BuilderTaskID == "" {
		s.writeError(w, http.StatusBadRequest, "runtime.builder_task_id_required", "builder_task_id is required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lifecycleLock := lifecyclePathLock(s.manifestPath)
	lifecycleLock.Lock()
	defer lifecycleLock.Unlock()
	if state, found, err := ReadLifecycle(s.manifestPath); err != nil {
		s.writeError(w, http.StatusInternalServerError, "runtime.lifecycle_unreadable", err.Error())
		return
	} else if found {
		if state.Status == LifecycleStatusConfiguring && state.BuilderTaskID == request.BuilderTaskID && state.RuntimeID == request.RuntimeID {
			s.writeJSON(w, http.StatusOK, s.contractPayload(map[string]any{"status": state.Status, "lifecycle": state, "idempotent": true}))
			return
		}
		s.writeError(w, http.StatusConflict, "runtime.lifecycle_conflict", "Runtime lifecycle is already owned by another task")
		return
	}
	if _, err := os.Stat(s.manifestPath); err == nil {
		s.writeError(w, http.StatusConflict, "runtime.installed_manifest_is_source_controlled", "installed Runtime metadata must be updated through a versioned manifest deployment")
		return
	} else if !os.IsNotExist(err) {
		s.writeError(w, http.StatusInternalServerError, "runtime.current_manifest_unreadable", err.Error())
		return
	}
	manifest := manifestmodel.ManifestSchema{
		SchemaVersion: manifestmodel.CurrentManifestSchemaVersion,
		TemplateID:    "direct-authoring-" + request.RuntimeID, Version: "0.0.0-configuring", SourceBlueprintID: DirectAuthoringSourceID,
		Objects: []definitionmodel.ObjectSchema{},
	}
	hash := hashManifestPart(manifest)
	if err := s.writeConfiguringManifest(s.manifestPath, manifest); err != nil {
		s.writeError(w, http.StatusInternalServerError, "runtime.manifest_write_failed", err.Error())
		return
	}
	state := newConfiguringLifecycle(request.RuntimeID, request.BuilderTaskID, hash)
	if err := s.writeConfiguringLifecycle(s.manifestPath, state); err != nil {
		_ = os.Remove(s.manifestPath)
		s.writeError(w, http.StatusInternalServerError, "runtime.lifecycle_write_failed", err.Error())
		return
	}
	if s.activate != nil {
		if err := s.activate(manifest); err != nil {
			_ = os.Remove(LifecyclePath(s.manifestPath))
			_ = os.Remove(s.manifestPath)
			s.writeError(w, http.StatusInternalServerError, "runtime.activation_failed", err.Error())
			return
		}
	}
	if s.enterConfiguring != nil {
		s.enterConfiguring(state)
	}
	s.writeJSON(w, http.StatusCreated, s.contractPayload(map[string]any{"status": state.Status, "lifecycle": state, "idempotent": false}))
}

func (s *Server) lifecycle(w http.ResponseWriter, _ *http.Request) {
	state, found, err := ReadLifecycle(s.manifestPath)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "runtime.lifecycle_unreadable", err.Error())
		return
	}
	if !found {
		s.writeJSON(w, http.StatusOK, s.contractPayload(map[string]any{"status": "provisioning", "ready": false}))
		return
	}
	s.writeJSON(w, http.StatusOK, s.contractPayload(map[string]any{"status": state.Status, "ready": state.Status == LifecycleStatusReady, "lifecycle": state}))
}

func (s *Server) abandonConfiguring(w http.ResponseWriter, r *http.Request) {
	var request abandonConfiguringRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.writeError(w, http.StatusBadRequest, "runtime.abandon_request_invalid", "runtime_id and builder_task_id are required")
		return
	}
	rawRuntimeID := strings.TrimSpace(request.RuntimeID)
	request.RuntimeID = configuringRuntimeID(rawRuntimeID)
	request.BuilderTaskID = strings.TrimSpace(request.BuilderTaskID)
	if rawRuntimeID == "" || request.BuilderTaskID == "" {
		s.writeError(w, http.StatusBadRequest, "runtime.abandon_request_invalid", "runtime_id and builder_task_id are required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lifecycleLock := lifecyclePathLock(s.manifestPath)
	lifecycleLock.Lock()
	defer lifecycleLock.Unlock()
	state, found, err := ReadLifecycle(s.manifestPath)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "runtime.lifecycle_unreadable", "Runtime lifecycle cannot be read")
		return
	}
	if !found || state.RuntimeID != request.RuntimeID || state.BuilderTaskID != request.BuilderTaskID {
		s.writeError(w, http.StatusConflict, "runtime.abandon_ownership_mismatch", "Runtime is not owned by this builder task")
		return
	}
	if state.Status != LifecycleStatusConfiguring && state.Status != LifecycleStatusValidating && state.Status != LifecycleStatusVerifying {
		s.writeError(w, http.StatusConflict, "runtime.abandon_delivered_forbidden", "delivered Runtime cannot be abandoned")
		return
	}
	raw, err := os.ReadFile(s.manifestPath)
	if err != nil && !os.IsNotExist(err) {
		s.writeError(w, http.StatusInternalServerError, "runtime.current_manifest_unreadable", "Runtime manifest cannot be read")
		return
	}
	if err == nil {
		var manifest manifestmodel.ManifestSchema
		if err := json.Unmarshal(raw, &manifest); err != nil || manifest.SourceBlueprintID != DirectAuthoringSourceID {
			s.writeError(w, http.StatusConflict, "runtime.abandon_non_owned_manifest", "only direct-authoring Runtime artifacts can be abandoned")
			return
		}
	}
	if s.abandon != nil {
		if err := s.abandon(); err != nil {
			s.writeError(w, http.StatusInternalServerError, "runtime.abandon_shutdown_failed", "Runtime could not be stopped")
			return
		}
	}
	if err := os.Remove(s.manifestPath); err != nil && !os.IsNotExist(err) {
		s.writeError(w, http.StatusInternalServerError, "runtime.abandon_manifest_cleanup_failed", "Runtime manifest could not be removed")
		return
	}
	if err := os.Remove(LifecyclePath(s.manifestPath)); err != nil && !os.IsNotExist(err) {
		s.writeError(w, http.StatusInternalServerError, "runtime.abandon_lifecycle_cleanup_failed", "Runtime lifecycle could not be removed")
		return
	}
	s.writeJSON(w, http.StatusOK, s.contractPayload(map[string]any{"status": "abandoned", "runtime_id": state.RuntimeID, "builder_task_id": state.BuilderTaskID}))
}
