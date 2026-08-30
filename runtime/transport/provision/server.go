package provision

import changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"

import (
	"encoding/json"
	"fmt"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	"net/http"
	"os"
	"strings"
	"sync"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	"github.com/domainry/domainry-runtime/runtime/platform/productbrand"

	businessmanifest "github.com/domainry/domainry-runtime/runtime/domain/manifest/validation"
)

const emptySnapshotHash = "empty"

// ActivateFunc starts the business Runtime after its Manifest is installed.
// Identity provisioning and credential delivery belong to the selected
// Identity deployment and are intentionally absent from this boundary.
type ActivateFunc func(manifestmodel.ManifestSchema) error

type Server struct {
	manifestPath              string
	runtimeVersion            string
	productBrandName          string
	contract                  ContractIdentity
	activate                  ActivateFunc
	enterConfiguring          func(LifecycleState)
	abandon                   func() error
	writeConfiguringManifest  func(string, manifestmodel.ManifestSchema) error
	writeConfiguringLifecycle func(string, LifecycleState) error
	mu                        sync.Mutex
}

type ContractIdentity struct {
	ServiceKind        string
	APIContractVersion string
	APIContractHash    string
}

type manifestRequest struct {
	Manifest             manifestmodel.ManifestSchema `json:"manifest"`
	ReviewedManifestHash string                       `json:"reviewed_manifest_hash,omitempty"`
	ExpectedSnapshotHash string                       `json:"expected_snapshot_hash,omitempty"`
	Actor                string                       `json:"actor,omitempty"`
	Reason               string                       `json:"reason,omitempty"`
	ApproveDestructive   bool                         `json:"approve_destructive,omitempty"`
}

type v1ProvisionRequest struct {
	ProvisionRequest map[string]any `json:"provision_request"`
}

type v1RollbackRequest struct {
	ContractVersion string `json:"contract_version,omitempty"`
	RequestHash     string `json:"request_hash,omitempty"`
	ReceiptHash     string `json:"receipt_hash,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

type provisionDiagnostic struct {
	Code    string         `json:"code"`
	Path    string         `json:"path"`
	Message string         `json:"message"`
	Repair  map[string]any `json:"repair,omitempty"`
}

func NewServer(manifestPath, runtimeVersion string, contract ContractIdentity, activate ActivateFunc) *Server {
	return &Server{manifestPath: manifestPath, runtimeVersion: runtimeVersion, productBrandName: productbrand.DefaultName, contract: contract, activate: activate, writeConfiguringManifest: writeManifestAtomic, writeConfiguringLifecycle: writeLifecycle}
}

func NewServerWithConfiguringLifecycle(manifestPath, runtimeVersion string, contract ContractIdentity, activate ActivateFunc, enterConfiguring func(LifecycleState)) *Server {
	return &Server{manifestPath: manifestPath, runtimeVersion: runtimeVersion, productBrandName: productbrand.DefaultName, contract: contract, activate: activate, enterConfiguring: enterConfiguring, writeConfiguringManifest: writeManifestAtomic, writeConfiguringLifecycle: writeLifecycle}
}

func (s *Server) UseProductBrand(name string) *Server {
	s.productBrandName = productbrand.ResolveName(name)
	return s
}

func (s *Server) UseAbandonRuntime(abandon func() error) *Server {
	s.abandon = abandon
	return s
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.identity)
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /openapi.json", s.openapi)
	mux.HandleFunc("POST /metadata/manifests/validate", s.validate)
	mux.HandleFunc("POST /metadata/manifests/review", s.review)
	mux.HandleFunc("POST /metadata/manifests/apply", s.apply)
	mux.HandleFunc("GET /metadata/manifests/current", s.current)
	mux.HandleFunc("POST /provision/review", s.v1Review)
	mux.HandleFunc("POST /provision/apply", s.v1Apply)
	mux.HandleFunc("POST /provision/rollback", s.v1Rollback)
	mux.HandleFunc("POST /provision/configuring", s.configuring)
	mux.HandleFunc("GET /provision/lifecycle", s.lifecycle)
	mux.HandleFunc("POST /provision/abandon", s.abandonConfiguring)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) identity(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, s.contractPayload(map[string]any{
		"status":  "provisioning",
		"message": "Runtime is waiting for an initial manifest Provision",
	}))
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	if state, found, err := ReadLifecycle(s.manifestPath); err == nil && found {
		s.writeJSON(w, http.StatusOK, s.contractPayload(map[string]any{"status": state.Status, "ready": false, "lifecycle": state}))
		return
	}
	s.writeJSON(w, http.StatusOK, s.contractPayload(map[string]any{
		"status": "provisioning",
		"ready":  false,
	}))
}

func (s *Server) openapi(w http.ResponseWriter, _ *http.Request) {
	paths := map[string]any{}
	for _, path := range []string{"/metadata/manifests/validate", "/metadata/manifests/review", "/metadata/manifests/apply"} {
		paths[path] = map[string]any{"post": map[string]any{"security": []any{}, "description": "Temporarily unauthenticated builder Provision endpoint"}}
	}
	for _, path := range []string{"/provision/review", "/provision/apply", "/provision/rollback", "/provision/configuring", "/provision/abandon"} {
		paths[path] = map[string]any{"post": map[string]any{"security": []any{}, "description": s.productBrandName + " Framework Builder v1 Project Runtime Provision endpoint"}}
	}
	paths["/provision/lifecycle"] = map[string]any{"get": map[string]any{"security": []any{}, "description": "Runtime direct-authoring lifecycle status"}}
	paths["/metadata/manifests/current"] = map[string]any{"get": map[string]any{"security": []any{}, "description": "Temporarily unauthenticated builder Provision endpoint"}}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": s.productBrandName + " domain Runtime Provision API", "version": s.runtimeVersion},
		"paths":   paths,
	})
}

func (s *Server) validate(w http.ResponseWriter, r *http.Request) {
	request, ok := s.decodeRequest(w, r)
	if !ok {
		return
	}
	hash, err := validateAndHash(request.Manifest, s.contract)
	if err != nil {
		s.writeError(w, http.StatusUnprocessableEntity, "manifest_validation_failed", err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, s.contractPayload(map[string]any{
		"status":        "valid",
		"valid":         true,
		"template_id":   request.Manifest.TemplateID,
		"manifest_hash": hash,
		"diagnostics":   []any{},
	}))
}

func (s *Server) review(w http.ResponseWriter, r *http.Request) {
	request, ok := s.decodeRequest(w, r)
	if !ok {
		return
	}
	hash, err := validateAndHash(request.Manifest, s.contract)
	if err != nil {
		s.writeError(w, http.StatusUnprocessableEntity, "manifest_validation_failed", err.Error())
		return
	}
	current, currentHash, found, err := s.loadCurrent()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "current_manifest_unreadable", err.Error())
		return
	}
	review := businessmanifest.ReviewResult{Changes: []businessmanifest.ReviewChange{}, Blockers: []businessmanifest.ReviewChange{}}
	if found && currentHash != hash {
		review = businessmanifest.ReviewManifestUpdate(current, request.Manifest, businessmanifest.ReviewOptions{ApproveDestructive: request.ApproveDestructive})
	}
	s.writeJSON(w, http.StatusOK, s.contractPayload(map[string]any{
		"status":                "reviewed",
		"template_id":           request.Manifest.TemplateID,
		"manifest_hash":         hash,
		"current_snapshot_hash": snapshotHash(found, currentHash),
		"initial_install":       !found,
		"idempotent":            found && currentHash == hash,
		"changes":               review.Changes,
		"blockers":              review.Blockers,
		"apply_allowed":         len(review.Blockers) == 0,
	}))
}

func (s *Server) apply(w http.ResponseWriter, r *http.Request) {
	request, ok := s.decodeRequest(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(request.Actor) == "" || strings.TrimSpace(request.Reason) == "" {
		s.writeError(w, http.StatusBadRequest, "provision_evidence_required", "actor and reason are required")
		return
	}
	hash, err := validateAndHash(request.Manifest, s.contract)
	if err != nil {
		s.writeError(w, http.StatusUnprocessableEntity, "manifest_validation_failed", err.Error())
		return
	}
	if request.ReviewedManifestHash != hash {
		s.writeError(w, http.StatusConflict, "reviewed_manifest_hash_mismatch", "reviewed_manifest_hash does not match the submitted manifest")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, currentHash, found, err := s.loadCurrent()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "current_manifest_unreadable", err.Error())
		return
	}
	if request.ExpectedSnapshotHash != snapshotHash(found, currentHash) {
		s.writeError(w, http.StatusConflict, "stale_snapshot", "expected_snapshot_hash does not match the current Runtime manifest")
		return
	}
	if found && currentHash == hash {
		if err := s.appendAudit(request, hash, "noop", "no state change; installed manifest already matches"); err != nil {
			s.writeError(w, http.StatusInternalServerError, "provision_audit_write_failed", err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, s.applyResult("noop", request.Manifest, hash, currentHash))
		return
	}
	if found && current.TemplateID != request.Manifest.TemplateID {
		s.writeError(w, http.StatusConflict, "template_id_conflict", "a different template_id is already installed")
		return
	}
	if found {
		review := businessmanifest.ReviewManifestUpdate(current, request.Manifest, businessmanifest.ReviewOptions{ApproveDestructive: request.ApproveDestructive})
		if review.HasBlockers() {
			s.writeJSON(w, http.StatusConflict, map[string]any{"code": "manifest_review_required", "message": "manifest changes require destructive approval", "blockers": review.Blockers})
			return
		}
	}
	if err := writeManifestAtomic(s.manifestPath, request.Manifest); err != nil {
		s.writeError(w, http.StatusInternalServerError, "manifest_write_failed", err.Error())
		return
	}
	if s.activate != nil {
		if err = s.activate(request.Manifest); err != nil {
			if found {
				_ = writeManifestAtomic(s.manifestPath, current)
			} else {
				_ = os.Remove(s.manifestPath)
			}
			_ = s.appendAudit(request, hash, "rolled_back", "manifest file restored after Runtime activation failure; database recovery follows Runtime bootstrap transaction boundary")
			s.writeError(w, http.StatusInternalServerError, "runtime_activation_failed", err.Error())
			return
		}
	}
	if err := s.appendAudit(request, hash, "applied", "manifest persisted; Runtime bootstrap transaction is the database recovery boundary"); err != nil {
		s.writeError(w, http.StatusInternalServerError, "provision_audit_write_failed", err.Error())
		return
	}
	result := s.applyResult("applied", request.Manifest, hash, hash)
	s.writeJSON(w, http.StatusCreated, result)
}

func (s *Server) current(w http.ResponseWriter, _ *http.Request) {
	manifest, hash, found, err := s.loadCurrent()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "current_manifest_unreadable", err.Error())
		return
	}
	if !found {
		s.writeJSON(w, http.StatusOK, s.contractPayload(map[string]any{"status": "empty", "snapshot_hash": emptySnapshotHash, "manifest": nil, "runtime_metadata": changeplanprojection.RuntimeNativeMetadataModel{ModelVersion: changeplanprojection.RuntimeNativeMetadataModelVersion, Status: "empty", ServiceKind: s.contract.ServiceKind, RuntimeVersion: s.runtimeVersion, SnapshotHash: emptySnapshotHash, APIContractVersion: s.contract.APIContractVersion, APIContractHash: s.contract.APIContractHash}}))
		return
	}
	contract := capabilityapplication.RuntimeAuthoringCapabilities()
	metadata := changeplanprojection.RuntimeNativeMetadataModel{
		ModelVersion: changeplanprojection.RuntimeNativeMetadataModelVersion, Status: "installed", ServiceKind: s.contract.ServiceKind,
		RuntimeVersion: s.runtimeVersion, TemplateID: manifest.TemplateID, TemplateVersion: manifest.Version,
		ManifestHash: hash, SnapshotHash: hash, SourceBlueprintID: manifest.SourceBlueprintID,
		APIContractVersion: s.contract.APIContractVersion, APIContractHash: s.contract.APIContractHash,
		AuthoringContractVersion: contract.ContractVersion, AuthoringContractHash: contract.ContractHash, Manifest: &manifest,
	}
	s.writeJSON(w, http.StatusOK, s.contractPayload(map[string]any{"status": "installed", "snapshot_hash": hash, "manifest_hash": hash, "manifest": manifest, "runtime_metadata": metadata}))
}

func (s *Server) v1Review(w http.ResponseWriter, r *http.Request) {
	request, manifest, diagnostics, ok := s.decodeV1ProvisionRequest(w, r)
	if !ok {
		return
	}
	hash := ""
	if len(diagnostics) == 0 {
		var err error
		hash, err = validateAndHash(manifest, s.contract)
		if err != nil {
			diagnostics = append(diagnostics, provisionV1Diagnostic("runtime.manifest_validation_failed", "/provision_request/manifest", err.Error(), "run_blueprint_compile"))
		}
	}
	currentSnapshot := emptySnapshotHash
	initialInstall := true
	idempotent := false
	if len(diagnostics) == 0 {
		current, currentHash, found, err := s.loadCurrent()
		if err != nil {
			diagnostics = append(diagnostics, provisionV1Diagnostic("runtime.current_manifest_unreadable", "/runtime/current", err.Error(), "inspect_runtime_instance"))
		} else {
			currentSnapshot = snapshotHash(found, currentHash)
			initialInstall = !found
			idempotent = found && currentHash == hash
			if found && currentHash != hash {
				review := businessmanifest.ReviewManifestUpdate(current, manifest, businessmanifest.ReviewOptions{ApproveDestructive: false})
				for index, blocker := range review.Blockers {
					diagnostics = append(diagnostics, provisionV1Diagnostic("runtime.manifest_update_blocked", fmt.Sprintf("/blockers/%d", index), blocker.Description, "approve_destructive_manifest_deployment"))
				}
			}
		}
	}
	status := "ready"
	if len(diagnostics) > 0 {
		status = "blocked"
	}
	s.writeJSON(w, http.StatusOK, s.contractPayload(map[string]any{
		"contract_version":       "domainry-runtime-provision-review-v1",
		"status":                 status,
		"request_hash":           stringFromMap(request, "request_hash"),
		"manifest_hash":          hash,
		"reviewed_manifest_hash": hash,
		"current_snapshot_hash":  currentSnapshot,
		"initial_install":        initialInstall,
		"idempotent":             idempotent,
		"apply_allowed":          status == "ready",
		"diagnostic_count":       len(diagnostics),
		"diagnostics":            diagnostics,
	}))
}

func (s *Server) v1Apply(w http.ResponseWriter, r *http.Request) {
	request, manifest, diagnostics, ok := s.decodeV1ProvisionRequest(w, r)
	if !ok {
		return
	}
	hash := ""
	if len(diagnostics) == 0 {
		var err error
		hash, err = validateAndHash(manifest, s.contract)
		if err != nil {
			diagnostics = append(diagnostics, provisionV1Diagnostic("runtime.manifest_validation_failed", "/provision_request/manifest", err.Error(), "run_blueprint_compile"))
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	currentSnapshot := emptySnapshotHash
	currentHash := ""
	found := false
	var currentManifest manifestmodel.ManifestSchema
	if len(diagnostics) == 0 {
		current, loadedHash, loaded, err := s.loadCurrent()
		if err != nil {
			diagnostics = append(diagnostics, provisionV1Diagnostic("runtime.current_manifest_unreadable", "/runtime/current", err.Error(), "inspect_runtime_instance"))
		} else {
			currentHash = loadedHash
			found = loaded
			currentManifest = current
			currentSnapshot = snapshotHash(found, currentHash)
			if found && currentHash != hash {
				if current.TemplateID != manifest.TemplateID {
					diagnostics = append(diagnostics, provisionV1Diagnostic("runtime.template_id_conflict", "/provision_request/manifest/template_id", "a different template_id is already installed", "use_dedicated_project_runtime"))
				}
			}
		}
	}
	if len(diagnostics) > 0 {
		s.writeJSON(w, http.StatusOK, s.contractPayload(map[string]any{
			"contract_version": "domainry-runtime-provision-apply-v1",
			"status":           "blocked",
			"request_hash":     stringFromMap(request, "request_hash"),
			"diagnostic_count": len(diagnostics),
			"diagnostics":      diagnostics,
		}))
		return
	}
	resultStatus := "applied"
	if found && currentHash == hash {
		resultStatus = "noop"
	} else {
		if err := writeManifestAtomic(s.manifestPath, manifest); err != nil {
			s.writeV1ApplyBlocked(w, request, "runtime.manifest_write_failed", "/runtime/manifest", err.Error(), "inspect_runtime_instance_storage")
			return
		}
		if s.activate != nil {
			if err := s.activate(manifest); err != nil {
				if found {
					_ = writeManifestAtomic(s.manifestPath, currentManifest)
				} else {
					_ = os.Remove(s.manifestPath)
				}
				s.writeV1ApplyBlocked(w, request, "runtime.activation_failed", "/runtime/activation", err.Error(), "inspect_runtime_bootstrap")
				return
			}
		}
	}
	auditRequest := manifestRequest{Manifest: manifest, Actor: valueOrDefault(stringFromMap(request, "actor"), "builder-agent"), Reason: valueOrDefault(stringFromMap(request, "reason"), s.productBrandName+" Framework Builder v1 provision apply"), ExpectedSnapshotHash: currentSnapshot}
	if err := s.appendAudit(auditRequest, hash, resultStatus, "manifest persisted; Runtime bootstrap transaction is the database recovery boundary"); err != nil {
		s.writeV1ApplyBlocked(w, request, "runtime.provision_audit_write_failed", "/runtime/audit", err.Error(), "inspect_runtime_instance_storage")
		return
	}
	receipt := s.v1Receipt(request, manifest, hash, resultStatus)
	s.writeJSON(w, http.StatusCreated, s.contractPayload(map[string]any{
		"contract_version": "domainry-runtime-provision-apply-v1",
		"status":           resultStatus,
		"request_hash":     stringFromMap(request, "request_hash"),
		"receipt":          receipt,
	}))
}

func (s *Server) v1Rollback(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	decoder.DisallowUnknownFields()
	var request v1RollbackRequest
	if err := decoder.Decode(&request); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status := "not_applied"
	if err := os.Remove(s.manifestPath); err == nil {
		status = "rolled_back"
	} else if !os.IsNotExist(err) {
		s.writeJSON(w, http.StatusOK, s.contractPayload(map[string]any{
			"contract_version": "domainry-runtime-provision-rollback-v1",
			"status":           "blocked",
			"request_hash":     request.RequestHash,
			"receipt_hash":     request.ReceiptHash,
			"diagnostic_count": 1,
			"diagnostics":      []provisionDiagnostic{provisionV1Diagnostic("runtime.rollback_failed", "/runtime/manifest", err.Error(), "inspect_runtime_instance_storage")},
		}))
		return
	}
	s.writeJSON(w, http.StatusOK, s.contractPayload(map[string]any{
		"contract_version": "domainry-runtime-provision-rollback-v1",
		"status":           status,
		"request_hash":     request.RequestHash,
		"receipt_hash":     request.ReceiptHash,
		"reason":           request.Reason,
		"diagnostic_count": 0,
		"diagnostics":      []provisionDiagnostic{},
	}))
}
