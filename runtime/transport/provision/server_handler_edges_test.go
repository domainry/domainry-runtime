package provision

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func provisionResponseMap(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var value map[string]any
	decodeProvisionResponse(t, response, &value)
	return value
}

func manifestVariant(t *testing.T, mutate func(*manifestmodel.ManifestSchema)) manifestmodel.ManifestSchema {
	t.Helper()
	manifest := provisionTestManifest(t)
	mutate(&manifest)
	var err error
	manifest.ManifestHash, err = manifestHash(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestProvisionReviewCoversInvalidUnreadableIdempotentAndChangedCurrent(t *testing.T) {
	target := filepath.Join(t.TempDir(), "manifest.json")
	server := NewServer(target, "dev", testContractIdentity(), nil).Routes()

	invalid := provisionRequest(t, server, http.MethodPost, "/provision/manifests/review", map[string]any{"manifest": map[string]any{}})
	if invalid.Code != http.StatusUnprocessableEntity || !bytes.Contains(invalid.Body.Bytes(), []byte(`"code":"manifest_validation_failed"`)) {
		t.Fatalf("invalid status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := provisionTestManifest(t)
	unreadable := provisionRequest(t, server, http.MethodPost, "/provision/manifests/review", map[string]any{"manifest": manifest})
	if unreadable.Code != http.StatusInternalServerError || !bytes.Contains(unreadable.Body.Bytes(), []byte(`"code":"current_manifest_unreadable"`)) {
		t.Fatalf("unreadable status=%d body=%s", unreadable.Code, unreadable.Body.String())
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := writeManifestAtomic(target, manifest); err != nil {
		t.Fatal(err)
	}
	idempotent := provisionRequest(t, server, http.MethodPost, "/provision/manifests/review", map[string]any{"manifest": manifest})
	if payload := provisionResponseMap(t, idempotent); idempotent.Code != http.StatusOK || payload["idempotent"] != true || payload["initial_install"] != false {
		t.Fatalf("status=%d payload=%#v", idempotent.Code, payload)
	}
	changed := manifestVariant(t, func(value *manifestmodel.ManifestSchema) { value.Name = value.Name + " Updated" })
	reviewed := provisionRequest(t, server, http.MethodPost, "/provision/manifests/review", map[string]any{"manifest": changed})
	if payload := provisionResponseMap(t, reviewed); reviewed.Code != http.StatusOK || payload["idempotent"] != false || payload["initial_install"] != false {
		t.Fatalf("status=%d payload=%#v", reviewed.Code, payload)
	}
}

func TestProvisionHealthFallsBackWhenLifecycleIsUnreadable(t *testing.T) {
	target := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(LifecyclePath(target), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	NewServer(target, "dev", testContractIdentity(), nil).Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"provisioning"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestProvisionApplyCoversEvidenceValidationCurrentAndActivationFailures(t *testing.T) {
	manifest := provisionTestManifest(t)
	hash := manifest.ManifestHash
	tests := []struct {
		name       string
		setup      func(t *testing.T, target string) ActivateFunc
		payload    map[string]any
		status     int
		code       string
		fileAbsent bool
	}{
		{name: "evidence", payload: map[string]any{"manifest": manifest}, status: http.StatusBadRequest, code: "provision_evidence_required"},
		{name: "manifest", payload: map[string]any{"manifest": map[string]any{}, "actor": "builder", "reason": "test"}, status: http.StatusUnprocessableEntity, code: "manifest_validation_failed"},
		{name: "review hash", payload: map[string]any{"manifest": manifest, "actor": "builder", "reason": "test", "reviewed_manifest_hash": "wrong"}, status: http.StatusConflict, code: "reviewed_manifest_hash_mismatch"},
		{name: "stale", payload: map[string]any{"manifest": manifest, "actor": "builder", "reason": "test", "reviewed_manifest_hash": hash, "expected_snapshot_hash": "wrong"}, status: http.StatusConflict, code: "stale_snapshot"},
		{name: "unreadable", setup: func(t *testing.T, target string) ActivateFunc {
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			return nil
		}, payload: map[string]any{"manifest": manifest, "actor": "builder", "reason": "test", "reviewed_manifest_hash": hash, "expected_snapshot_hash": emptySnapshotHash}, status: http.StatusInternalServerError, code: "current_manifest_unreadable"},
		{name: "activation", setup: func(*testing.T, string) ActivateFunc {
			return func(manifestmodel.ManifestSchema) error {
				return errors.New("activation failed")
			}
		}, payload: map[string]any{"manifest": manifest, "actor": "builder", "reason": "test", "reviewed_manifest_hash": hash, "expected_snapshot_hash": emptySnapshotHash}, status: http.StatusInternalServerError, code: "runtime_activation_failed", fileAbsent: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "manifest.json")
			var activate ActivateFunc
			if test.setup != nil {
				activate = test.setup(t, target)
			}
			server := NewServer(target, "dev", testContractIdentity(), activate).Routes()
			response := provisionRequest(t, server, http.MethodPost, "/provision/manifests/apply", test.payload)
			if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.fileAbsent {
				if _, err := os.Stat(target); !os.IsNotExist(err) {
					t.Fatalf("manifest retained after activation failure: %v", err)
				}
			}
		})
	}
}

func TestProvisionApplyRejectsTemplateConflictAndAcceptsSourceControlledUpdate(t *testing.T) {
	current := provisionTestManifest(t)
	for name, next := range map[string]manifestmodel.ManifestSchema{
		"template": manifestVariant(t, func(value *manifestmodel.ManifestSchema) { value.TemplateID = value.TemplateID + "-other" }),
		"update":   manifestVariant(t, func(value *manifestmodel.ManifestSchema) { value.Name = value.Name + " Updated" }),
	} {
		t.Run(name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "manifest.json")
			if err := writeManifestAtomic(target, current); err != nil {
				t.Fatal(err)
			}
			server := NewServer(target, "dev", testContractIdentity(), nil).Routes()
			response := provisionRequest(t, server, http.MethodPost, "/provision/manifests/apply", map[string]any{
				"manifest": next, "reviewed_manifest_hash": next.ManifestHash, "expected_snapshot_hash": current.ManifestHash, "actor": "builder", "reason": "test",
			})
			if name == "template" {
				if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"template_id_conflict"`) {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
			} else if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"status":"applied"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	target := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(filepath.Dir(target), "provision-audit.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	server := NewServer(target, "dev", testContractIdentity(), nil).Routes()
	response := provisionRequest(t, server, http.MethodPost, "/provision/manifests/apply", map[string]any{
		"manifest": current, "reviewed_manifest_hash": current.ManifestHash, "expected_snapshot_hash": emptySnapshotHash, "actor": "builder", "reason": "test",
	})
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"code":"provision_audit_write_failed"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestProvisionCurrentCoversEmptyAndUnreadableState(t *testing.T) {
	target := filepath.Join(t.TempDir(), "manifest.json")
	server := NewServer(target, "dev", testContractIdentity(), nil).Routes()
	empty := provisionRequest(t, server, http.MethodGet, "/provision/manifests/current", nil)
	if payload := provisionResponseMap(t, empty); empty.Code != http.StatusOK || payload["status"] != "empty" || payload["snapshot_hash"] != emptySnapshotHash {
		t.Fatalf("status=%d payload=%#v", empty.Code, payload)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	unreadable := provisionRequest(t, server, http.MethodGet, "/provision/manifests/current", nil)
	if unreadable.Code != http.StatusInternalServerError || !strings.Contains(unreadable.Body.String(), `"code":"current_manifest_unreadable"`) {
		t.Fatalf("status=%d body=%s", unreadable.Code, unreadable.Body.String())
	}
}

func TestBuilderV1ApplyAndRollbackFailureBoundaries(t *testing.T) {
	manifest := provisionTestManifest(t)
	request := builderV1ProvisionRequest(t, manifest, "dev")
	target := filepath.Join(t.TempDir(), "manifest.json")
	server := NewServer(target, "dev", testContractIdentity(), func(manifestmodel.ManifestSchema) error {
		return errors.New("activation failed")
	}).Routes()
	apply := provisionRequest(t, server, http.MethodPost, "/provision/apply", map[string]any{"provision_request": request})
	if payload := provisionResponseMap(t, apply); apply.Code != http.StatusOK || payload["status"] != "blocked" || !strings.Contains(apply.Body.String(), "runtime.activation_failed") {
		t.Fatalf("status=%d payload=%#v body=%s", apply.Code, payload, apply.Body.String())
	}
	malformed := httptest.NewRecorder()
	server.ServeHTTP(malformed, httptest.NewRequest(http.MethodPost, "/provision/rollback", strings.NewReader(`{`)))
	if malformed.Code != http.StatusBadRequest || !strings.Contains(malformed.Body.String(), `"code":"invalid_json"`) {
		t.Fatalf("status=%d body=%s", malformed.Code, malformed.Body.String())
	}
	notApplied := provisionRequest(t, server, http.MethodPost, "/provision/rollback", map[string]any{"request_hash": "request"})
	if payload := provisionResponseMap(t, notApplied); notApplied.Code != http.StatusOK || payload["status"] != "not_applied" {
		t.Fatalf("status=%d payload=%#v", notApplied.Code, payload)
	}
	blockedTarget := filepath.Join(t.TempDir(), "manifest-dir")
	if err := os.MkdirAll(blockedTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockedTarget, "child"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	blockedServer := NewServer(blockedTarget, "dev", testContractIdentity(), nil).Routes()
	blocked := provisionRequest(t, blockedServer, http.MethodPost, "/provision/rollback", map[string]any{"request_hash": "request"})
	if payload := provisionResponseMap(t, blocked); blocked.Code != http.StatusOK || payload["status"] != "blocked" || !strings.Contains(blocked.Body.String(), "runtime.rollback_failed") {
		t.Fatalf("status=%d payload=%#v body=%s", blocked.Code, payload, blocked.Body.String())
	}
}

func v1DiagnosticCodes(t *testing.T, response *httptest.ResponseRecorder) (string, map[string]bool) {
	t.Helper()
	var payload struct {
		Status      string `json:"status"`
		Idempotent  bool   `json:"idempotent"`
		Diagnostics []struct {
			Code string `json:"code"`
		} `json:"diagnostics"`
	}
	decodeProvisionResponse(t, response, &payload)
	codes := make(map[string]bool, len(payload.Diagnostics))
	for _, diagnostic := range payload.Diagnostics {
		codes[diagnostic.Code] = true
	}
	if payload.Idempotent {
		codes["idempotent"] = true
	}
	return payload.Status, codes
}

func TestBuilderV1ReviewCoversValidationAndInstalledStateBoundaries(t *testing.T) {
	manifest := provisionTestManifest(t)
	validRequest := builderV1ProvisionRequest(t, manifest, "dev")

	malformedServer := NewServer(filepath.Join(t.TempDir(), "manifest.json"), "dev", testContractIdentity(), nil).Routes()
	malformed := httptest.NewRecorder()
	malformedServer.ServeHTTP(malformed, httptest.NewRequest(http.MethodPost, "/provision/review", strings.NewReader(`{`)))
	if malformed.Code != http.StatusBadRequest || !strings.Contains(malformed.Body.String(), `"code":"invalid_json"`) {
		t.Fatalf("status=%d body=%s", malformed.Code, malformed.Body.String())
	}

	invalidRequest := map[string]any{"request_hash": "request", "manifest": map[string]any{}}
	invalid := provisionRequest(t, malformedServer, http.MethodPost, "/provision/review", map[string]any{"provision_request": invalidRequest})
	if status, codes := v1DiagnosticCodes(t, invalid); status != "blocked" || !codes["runtime.manifest_validation_failed"] {
		t.Fatalf("status=%q codes=%#v body=%s", status, codes, invalid.Body.String())
	}

	unreadableTarget := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.MkdirAll(unreadableTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	unreadableServer := NewServer(unreadableTarget, "dev", testContractIdentity(), nil).Routes()
	unreadable := provisionRequest(t, unreadableServer, http.MethodPost, "/provision/review", map[string]any{"provision_request": validRequest})
	if status, codes := v1DiagnosticCodes(t, unreadable); status != "blocked" || !codes["runtime.current_manifest_unreadable"] {
		t.Fatalf("status=%q codes=%#v body=%s", status, codes, unreadable.Body.String())
	}

	target := filepath.Join(t.TempDir(), "manifest.json")
	if err := writeManifestAtomic(target, manifest); err != nil {
		t.Fatal(err)
	}
	installedServer := NewServer(target, "dev", testContractIdentity(), nil).Routes()
	idempotent := provisionRequest(t, installedServer, http.MethodPost, "/provision/review", map[string]any{"provision_request": validRequest})
	if status, codes := v1DiagnosticCodes(t, idempotent); status != "ready" || !codes["idempotent"] {
		t.Fatalf("status=%q codes=%#v body=%s", status, codes, idempotent.Body.String())
	}

	changed := manifestVariant(t, func(value *manifestmodel.ManifestSchema) { value.Name += " Updated" })
	changedRequest := builderV1ProvisionRequest(t, changed, "dev")
	changedReview := provisionRequest(t, installedServer, http.MethodPost, "/provision/review", map[string]any{"provision_request": changedRequest})
	if status, codes := v1DiagnosticCodes(t, changedReview); status != "ready" || len(codes) != 0 {
		t.Fatalf("status=%q codes=%#v body=%s", status, codes, changedReview.Body.String())
	}
}

func TestBuilderV1ApplyCoversBlockedInstalledAndStorageBoundaries(t *testing.T) {
	manifest := provisionTestManifest(t)
	validRequest := builderV1ProvisionRequest(t, manifest, "dev")

	invalidServer := NewServer(filepath.Join(t.TempDir(), "manifest.json"), "dev", testContractIdentity(), nil).Routes()
	invalidRequest := map[string]any{"request_hash": "request", "manifest": map[string]any{}}
	invalid := provisionRequest(t, invalidServer, http.MethodPost, "/provision/apply", map[string]any{"provision_request": invalidRequest})
	if status, codes := v1DiagnosticCodes(t, invalid); status != "blocked" || !codes["runtime.manifest_validation_failed"] {
		t.Fatalf("status=%q codes=%#v body=%s", status, codes, invalid.Body.String())
	}

	unreadableTarget := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.MkdirAll(unreadableTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	unreadableServer := NewServer(unreadableTarget, "dev", testContractIdentity(), nil).Routes()
	unreadable := provisionRequest(t, unreadableServer, http.MethodPost, "/provision/apply", map[string]any{"provision_request": validRequest})
	if status, codes := v1DiagnosticCodes(t, unreadable); status != "blocked" || !codes["runtime.current_manifest_unreadable"] {
		t.Fatalf("status=%q codes=%#v body=%s", status, codes, unreadable.Body.String())
	}

	for name, next := range map[string]manifestmodel.ManifestSchema{
		"template": manifestVariant(t, func(value *manifestmodel.ManifestSchema) { value.TemplateID += "-other" }),
		"update":   manifestVariant(t, func(value *manifestmodel.ManifestSchema) { value.Name += " Updated" }),
	} {
		t.Run(name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "manifest.json")
			if err := writeManifestAtomic(target, manifest); err != nil {
				t.Fatal(err)
			}
			server := NewServer(target, "dev", testContractIdentity(), nil).Routes()
			response := provisionRequest(t, server, http.MethodPost, "/provision/apply", map[string]any{"provision_request": builderV1ProvisionRequest(t, next, "dev")})
			status, codes := v1DiagnosticCodes(t, response)
			if name == "template" {
				if status != "blocked" || !codes["runtime.template_id_conflict"] {
					t.Fatalf("status=%q codes=%#v body=%s", status, codes, response.Body.String())
				}
			} else if status != "applied" {
				t.Fatalf("status=%q codes=%#v body=%s", status, codes, response.Body.String())
			}
		})
	}

	readOnlyDir := filepath.Join(t.TempDir(), "read-only")
	if err := os.MkdirAll(readOnlyDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnlyDir, 0o700) })
	writeServer := NewServer(filepath.Join(readOnlyDir, "manifest.json"), "dev", testContractIdentity(), nil).Routes()
	writeFailed := provisionRequest(t, writeServer, http.MethodPost, "/provision/apply", map[string]any{"provision_request": validRequest})
	if status, codes := v1DiagnosticCodes(t, writeFailed); status != "blocked" || !codes["runtime.manifest_write_failed"] {
		t.Fatalf("status=%q codes=%#v body=%s", status, codes, writeFailed.Body.String())
	}

	auditTarget := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.MkdirAll(filepath.Join(filepath.Dir(auditTarget), "provision-audit.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	auditServer := NewServer(auditTarget, "dev", testContractIdentity(), nil).Routes()
	auditFailed := provisionRequest(t, auditServer, http.MethodPost, "/provision/apply", map[string]any{"provision_request": validRequest})
	if status, codes := v1DiagnosticCodes(t, auditFailed); status != "blocked" || !codes["runtime.provision_audit_write_failed"] {
		t.Fatalf("status=%q codes=%#v body=%s", status, codes, auditFailed.Body.String())
	}
}
