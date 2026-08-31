package provision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	businessmanifest "github.com/domainry/domainry-runtime/runtime/domain/manifest/validation"
)

type provisionFile interface {
	Name() string
	Write([]byte) (int, error)
	Chmod(os.FileMode) error
	Close() error
}

type provisionAuditFile interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

var (
	provisionCreateTemp    = func(dir, pattern string) (provisionFile, error) { return os.CreateTemp(dir, pattern) }
	provisionRename        = os.Rename
	provisionOpenAuditFile = func(name string, flag int, perm os.FileMode) (provisionAuditFile, error) {
		return os.OpenFile(name, flag, perm)
	}
	// Provisioning may inject the Integration-owned Catalog projection. The
	// default validates connector declarations already carried by the manifest
	// and never restores a Runtime-owned provider catalog.
	provisionCatalog = func() ([]connectormodel.ConnectorSchema, error) { return nil, nil }
)

func (s *Server) decodeRequest(w http.ResponseWriter, r *http.Request) (manifestRequest, bool) {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20))
	decoder.DisallowUnknownFields()
	var request manifestRequest
	if err := decoder.Decode(&request); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return manifestRequest{}, false
	}
	return request, true
}

func (s *Server) decodeV1ProvisionRequest(w http.ResponseWriter, r *http.Request) (map[string]any, manifestmodel.ManifestSchema, []provisionDiagnostic, bool) {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20))
	decoder.DisallowUnknownFields()
	var envelope v1ProvisionRequest
	if err := decoder.Decode(&envelope); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return nil, manifestmodel.ManifestSchema{}, nil, false
	}
	diagnostics := []provisionDiagnostic{}
	if len(envelope.ProvisionRequest) == 0 {
		diagnostics = append(diagnostics, provisionV1Diagnostic("runtime.provision_request_missing", "/provision_request", "provision_request is required", "run_provision_prepare"))
		return map[string]any{}, manifestmodel.ManifestSchema{}, diagnostics, true
	}
	manifestValue, ok := envelope.ProvisionRequest["manifest"]
	if !ok {
		diagnostics = append(diagnostics, provisionV1Diagnostic("runtime.manifest_missing", "/provision_request/manifest", "provision_request.manifest is required", "run_blueprint_compile"))
	} else {
		payload, _ := json.Marshal(manifestValue)
		var manifest manifestmodel.ManifestSchema
		if err := json.Unmarshal(payload, &manifest); err != nil {
			diagnostics = append(diagnostics, provisionV1Diagnostic("runtime.manifest_invalid", "/provision_request/manifest", err.Error(), "run_blueprint_compile"))
		}
		if strings.TrimSpace(stringFromMap(envelope.ProvisionRequest, "request_hash")) == "" {
			diagnostics = append(diagnostics, provisionV1Diagnostic("runtime.request_hash_missing", "/provision_request/request_hash", "provision_request.request_hash is required", "run_provision_prepare"))
		}
		return envelope.ProvisionRequest, manifest, diagnostics, true
	}
	if strings.TrimSpace(stringFromMap(envelope.ProvisionRequest, "request_hash")) == "" {
		diagnostics = append(diagnostics, provisionV1Diagnostic("runtime.request_hash_missing", "/provision_request/request_hash", "provision_request.request_hash is required", "run_provision_prepare"))
	}
	return envelope.ProvisionRequest, manifestmodel.ManifestSchema{}, diagnostics, true
}

func (s *Server) loadCurrent() (manifestmodel.ManifestSchema, string, bool, error) {
	payload, err := os.ReadFile(s.manifestPath)
	if os.IsNotExist(err) {
		return manifestmodel.ManifestSchema{}, "", false, nil
	}
	if err != nil {
		return manifestmodel.ManifestSchema{}, "", false, err
	}
	var manifest manifestmodel.ManifestSchema
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return manifestmodel.ManifestSchema{}, "", false, err
	}
	hash, err := manifestHash(manifest)
	return manifest, hash, true, err
}

func (s *Server) contractPayload(payload map[string]any) map[string]any {
	payload["service_kind"] = s.contract.ServiceKind
	payload["runtime_version"] = s.runtimeVersion
	payload["api_contract_version"] = s.contract.APIContractVersion
	payload["api_contract_hash"] = s.contract.APIContractHash
	contract := capabilityapplication.RuntimeAuthoringCapabilities()
	payload["authoring_contract_version"] = contract.ContractVersion
	payload["authoring_contract_hash"] = contract.ContractHash
	return payload
}

func (s *Server) applyResult(status string, manifest manifestmodel.ManifestSchema, hash, snapshot string) map[string]any {
	return s.contractPayload(map[string]any{
		"status":        status,
		"template_id":   manifest.TemplateID,
		"manifest_hash": hash,
		"snapshot_hash": snapshot,
		"seed_result":   map[string]any{"status": "initialized", "deterministic": true},
		"audit_path":    s.auditPath(),
	})
}

func (s *Server) v1Receipt(request map[string]any, manifest manifestmodel.ManifestSchema, manifestHash string, status string) map[string]any {
	return map[string]any{
		"contract_version":             "domainry-provision-receipt-v1",
		"status":                       status,
		"request_hash":                 stringFromMap(request, "request_hash"),
		"foundation_hash":              stringFromMap(request, "foundation_hash"),
		"blueprint_hash":               stringFromMap(request, "blueprint_hash"),
		"runtime_manifest_hash":        stringFromMap(request, "runtime_manifest_hash"),
		"authoring_contract_hash":      stringFromMap(request, "authoring_contract_hash"),
		"target_api_contract_hash":     stringFromMap(request, "target_api_contract_hash"),
		"application_lock_hash":        stringFromMap(request, "application_lock_hash"),
		"release_manifest_hash":        stringFromMap(request, "release_manifest_hash"),
		"runtime_version":              s.runtimeVersion,
		"runtime_package_hash":         stringFromMap(request, "runtime_package_hash"),
		"runtime_instance_config_hash": stringFromMap(request, "runtime_instance_config_hash"),
		"runtime_api_contract_hash":    s.contract.APIContractHash,
		"installed_manifest_hash":      manifestHash,
		"seed_graph_hash":              hashManifestPart(map[string]any{"seed_records": manifest.SeedRecords, "automation_execution_seeds": manifest.AutomationExecutionSeeds}),
		"effective_permission_hash":    hashManifestPart(map[string]any{"report_export_controls": manifest.ReportExportControls, "sensitive_field_policies": manifest.SensitiveFieldPolicies}),
		"template_id":                  manifest.TemplateID,
		"template_version":             manifest.Version,
	}
}

func (s *Server) auditPath() string {
	return filepath.Join(filepath.Dir(s.manifestPath), "provision-audit.jsonl")
}

func (s *Server) appendAudit(request manifestRequest, hash, result, recoveryBoundary string) error {
	evidence := map[string]any{
		"recorded_at":             time.Now().UTC().Format(time.RFC3339Nano),
		"operation":               "initial_manifest_provision",
		"result":                  result,
		"actor":                   strings.TrimSpace(request.Actor),
		"reason":                  strings.TrimSpace(request.Reason),
		"template_id":             request.Manifest.TemplateID,
		"manifest_hash":           hash,
		"source_blueprint_id":     request.Manifest.SourceBlueprintID,
		"expected_snapshot_hash":  request.ExpectedSnapshotHash,
		"recovery_boundary":       recoveryBoundary,
		"runtime_version":         s.runtimeVersion,
		"api_contract_version":    s.contract.APIContractVersion,
		"authoring_contract_hash": request.Manifest.AuthoringContractHash,
	}
	payload, _ := json.Marshal(evidence)
	dir := filepath.Dir(s.manifestPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file, err := provisionOpenAuditFile(s.auditPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(payload, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func (s *Server) writeV1ApplyBlocked(w http.ResponseWriter, request map[string]any, code, path, message, operation string) {
	s.writeJSON(w, http.StatusOK, s.contractPayload(map[string]any{
		"contract_version": "domainry-runtime-provision-apply-v1",
		"status":           "blocked",
		"request_hash":     stringFromMap(request, "request_hash"),
		"diagnostic_count": 1,
		"diagnostics":      []provisionDiagnostic{provisionV1Diagnostic(code, path, message, operation)},
	}))
}

func provisionV1Diagnostic(code, path, message, operation string) provisionDiagnostic {
	return provisionDiagnostic{Code: code, Path: path, Message: message, Repair: map[string]any{"operation": operation}}
}

func hashManifestPart(value any) string {
	payload, _ := json.Marshal(value)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func stringFromMap(value map[string]any, key string) string {
	raw, ok := value[key]
	if !ok || raw == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (s *Server) writeError(w http.ResponseWriter, status int, code, message string) {
	s.writeJSON(w, status, map[string]any{"code": code, "message": message, "openapi_url": "/openapi.json"})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func validateAndHash(manifest manifestmodel.ManifestSchema, contract ContractIdentity) (string, error) {
	connectors, err := provisionCatalog()
	if err != nil {
		return "", fmt.Errorf("load Runtime Connector validation catalog: %w", err)
	}
	if err := businessmanifest.ValidateManifestWithConnectorCatalog(manifest, connectors); err != nil {
		return "", err
	}
	hash, err := manifestHash(manifest)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(manifest.ManifestHash) == "" {
		return "", fmt.Errorf("manifest_hash is required; use blueprint-compile instead of a raw domain blueprint")
	}
	if manifest.ManifestHash != hash {
		return "", fmt.Errorf("manifest_hash does not match normalized Runtime manifest content")
	}
	if manifest.TargetAPIContractVersion != contract.APIContractVersion || manifest.TargetAPIContractHash != contract.APIContractHash {
		return "", fmt.Errorf("target Runtime API contract is incompatible with this domain Runtime")
	}
	return hash, nil
}

func manifestHash(manifest manifestmodel.ManifestSchema) (string, error) {
	return manifestmodel.ManifestContentHash(manifest)
}

func snapshotHash(found bool, hash string) string {
	if !found {
		return emptySnapshotHash
	}
	return hash
}

func writeManifestAtomic(path string, manifest manifestmodel.ManifestSchema) error {
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := provisionCreateTemp(dir, ".domainry-*.json")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(payload); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := provisionRename(tempName, path); err != nil {
		return fmt.Errorf("install Runtime manifest: %w", err)
	}
	return nil
}
