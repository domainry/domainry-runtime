package provision

import (
	"errors"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

type provisionFileProbe struct {
	name      string
	writeErr  error
	chmodErr  error
	closeErr  error
	written   []byte
	closeCall int
}

func (f *provisionFileProbe) Name() string { return f.name }
func (f *provisionFileProbe) Write(value []byte) (int, error) {
	f.written = append(f.written, value...)
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(value), nil
}
func (f *provisionFileProbe) Chmod(os.FileMode) error { return f.chmodErr }
func (f *provisionFileProbe) Close() error {
	f.closeCall++
	return f.closeErr
}

type provisionAuditFileProbe struct{ writeErr error }

func (f *provisionAuditFileProbe) Write(value []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(value), nil
}
func (*provisionAuditFileProbe) Sync() error  { return nil }
func (*provisionAuditFileProbe) Close() error { return nil }

func destructiveProvisionManifest(t *testing.T) manifestmodel.ManifestSchema {
	t.Helper()
	return manifestVariant(t, func(value *manifestmodel.ManifestSchema) {
		value.Objects = value.Objects[:1]
		for roleIndex := range value.Roles {
			permissions := value.Roles[roleIndex].Permissions[:0]
			for _, permission := range value.Roles[roleIndex].Permissions {
				if !strings.HasPrefix(permission.PermissionKey, "opportunity.") {
					permissions = append(permissions, permission)
				}
			}
			value.Roles[roleIndex].Permissions = permissions
		}
		seeds := value.SeedRecords[:0]
		for _, seed := range value.SeedRecords {
			if seed.ObjectKey != "opportunity" {
				seeds = append(seeds, seed)
			}
		}
		value.SeedRecords = seeds
	})
}

func TestClassicProvisionRemainingDecodeEvidenceBlockerAndNoopEdges(t *testing.T) {
	manifest := provisionTestManifest(t)
	target := filepath.Join(t.TempDir(), "manifest.json")
	server := NewServer(target, "dev", testContractIdentity(), nil).Routes()
	for _, path := range []string{"/metadata/manifests/review", "/metadata/manifests/apply"} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{`)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s malformed status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	missingReason := provisionRequest(t, server, http.MethodPost, "/metadata/manifests/apply", map[string]any{"manifest": manifest, "actor": "builder", "reason": " "})
	if missingReason.Code != http.StatusBadRequest || !strings.Contains(missingReason.Body.String(), "provision_evidence_required") {
		t.Fatalf("missing reason status=%d body=%s", missingReason.Code, missingReason.Body.String())
	}

	if err := writeManifestAtomic(target, manifest); err != nil {
		t.Fatal(err)
	}
	destructive := destructiveProvisionManifest(t)
	review := provisionRequest(t, server, http.MethodPost, "/metadata/manifests/review", map[string]any{"manifest": destructive})
	if review.Code != http.StatusOK || !strings.Contains(review.Body.String(), `"apply_allowed":false`) {
		t.Fatalf("destructive review status=%d body=%s", review.Code, review.Body.String())
	}
	blocked := provisionRequest(t, server, http.MethodPost, "/metadata/manifests/apply", map[string]any{
		"manifest": destructive, "reviewed_manifest_hash": destructive.ManifestHash, "expected_snapshot_hash": manifest.ManifestHash,
		"actor": "builder", "reason": "remove object",
	})
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "manifest_review_required") {
		t.Fatalf("destructive apply status=%d body=%s", blocked.Code, blocked.Body.String())
	}

	originalCreateTemp := provisionCreateTemp
	t.Cleanup(func() { provisionCreateTemp = originalCreateTemp })
	provisionCreateTemp = func(string, string) (provisionFile, error) { return nil, errors.New("create failed") }
	writeFailedServer := NewServer(filepath.Join(t.TempDir(), "manifest.json"), "dev", testContractIdentity(), nil).Routes()
	writeFailed := provisionRequest(t, writeFailedServer, http.MethodPost, "/metadata/manifests/apply", map[string]any{
		"manifest": manifest, "reviewed_manifest_hash": manifest.ManifestHash, "expected_snapshot_hash": emptySnapshotHash,
		"actor": "builder", "reason": "initial install",
	})
	if writeFailed.Code != http.StatusInternalServerError || !strings.Contains(writeFailed.Body.String(), "manifest_write_failed") {
		t.Fatalf("write failure status=%d body=%s", writeFailed.Code, writeFailed.Body.String())
	}
	provisionCreateTemp = originalCreateTemp

	if err := os.MkdirAll(filepath.Join(filepath.Dir(target), "provision-audit.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	noop := provisionRequest(t, server, http.MethodPost, "/metadata/manifests/apply", map[string]any{
		"manifest": manifest, "reviewed_manifest_hash": manifest.ManifestHash, "expected_snapshot_hash": manifest.ManifestHash,
		"actor": "builder", "reason": "replay",
	})
	if noop.Code != http.StatusInternalServerError || !strings.Contains(noop.Body.String(), "provision_audit_write_failed") {
		t.Fatalf("noop audit status=%d body=%s", noop.Code, noop.Body.String())
	}
}

func TestBuilderV1RemainingDecodeBlockerAndNoopEdges(t *testing.T) {
	manifest := provisionTestManifest(t)
	target := filepath.Join(t.TempDir(), "manifest.json")
	server := NewServer(target, "dev", testContractIdentity(), nil).Routes()
	missing := provisionRequest(t, server, http.MethodPost, "/provision/apply", map[string]any{})
	if status, codes := v1DiagnosticCodes(t, missing); status != "blocked" || !codes["runtime.provision_request_missing"] {
		t.Fatalf("missing status=%q codes=%v body=%s", status, codes, missing.Body.String())
	}
	missingManifest := provisionRequest(t, server, http.MethodPost, "/provision/review", map[string]any{
		"provision_request": map[string]any{"request_hash": "request"},
	})
	if status, codes := v1DiagnosticCodes(t, missingManifest); status != "blocked" || !codes["runtime.manifest_missing"] || codes["runtime.request_hash_missing"] {
		t.Fatalf("missing manifest status=%q codes=%v body=%s", status, codes, missingManifest.Body.String())
	}
	malformed := httptest.NewRecorder()
	server.ServeHTTP(malformed, httptest.NewRequest(http.MethodPost, "/provision/apply", strings.NewReader(`{`)))
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed status=%d body=%s", malformed.Code, malformed.Body.String())
	}

	if err := writeManifestAtomic(target, manifest); err != nil {
		t.Fatal(err)
	}
	request := builderV1ProvisionRequest(t, manifest, "dev")
	noop := provisionRequest(t, server, http.MethodPost, "/provision/apply", map[string]any{"provision_request": request})
	if payload := provisionResponseMap(t, noop); noop.Code != http.StatusCreated || payload["status"] != "noop" {
		t.Fatalf("noop status=%d payload=%v body=%s", noop.Code, payload, noop.Body.String())
	}

	destructive := destructiveProvisionManifest(t)
	review := provisionRequest(t, server, http.MethodPost, "/provision/review", map[string]any{"provision_request": builderV1ProvisionRequest(t, destructive, "dev")})
	if status, codes := v1DiagnosticCodes(t, review); status != "blocked" || !codes["runtime.manifest_update_blocked"] {
		t.Fatalf("review status=%q codes=%v body=%s", status, codes, review.Body.String())
	}
}

func TestProvisionValidationAndFilesystemFailureSeams(t *testing.T) {
	manifest := provisionTestManifest(t)
	originalCatalog := provisionCatalog
	provisionCatalog = func() ([]connectormodel.ConnectorSchema, error) { return nil, errors.New("catalog failed") }
	if _, err := validateAndHash(manifest, testContractIdentity()); err == nil || !strings.Contains(err.Error(), "catalog failed") {
		t.Fatalf("catalog error=%v", err)
	}
	provisionCatalog = originalCatalog

	invalidHash := manifest
	invalidHash.BusinessLoops = []map[string]any{{"invalid": make(chan int)}}
	if _, err := validateAndHash(invalidHash, testContractIdentity()); err == nil {
		t.Fatal("unhashable manifest accepted")
	}
	versionMismatch := manifest
	versionMismatch.TargetAPIContractVersion = "other"
	versionMismatch.ManifestHash, _ = manifestHash(versionMismatch)
	if _, err := validateAndHash(versionMismatch, testContractIdentity()); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("version mismatch=%v", err)
	}
	if err := writeManifestAtomic(filepath.Join(t.TempDir(), "manifest.json"), invalidHash); err == nil {
		t.Fatal("unencodable manifest was written")
	}

	originalCreateTemp, originalRename := provisionCreateTemp, provisionRename
	t.Cleanup(func() { provisionCreateTemp, provisionRename = originalCreateTemp, originalRename })
	for _, test := range []struct {
		name  string
		probe *provisionFileProbe
	}{
		{name: "write", probe: &provisionFileProbe{name: filepath.Join(t.TempDir(), "write.tmp"), writeErr: errors.New("write failed")}},
		{name: "chmod", probe: &provisionFileProbe{name: filepath.Join(t.TempDir(), "chmod.tmp"), chmodErr: errors.New("chmod failed")}},
		{name: "close", probe: &provisionFileProbe{name: filepath.Join(t.TempDir(), "close.tmp"), closeErr: errors.New("close failed")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			provisionCreateTemp = func(string, string) (provisionFile, error) { return test.probe, nil }
			if err := writeManifestAtomic(filepath.Join(t.TempDir(), "manifest.json"), manifest); err == nil {
				t.Fatalf("%s failure ignored", test.name)
			}
		})
	}
	probe := &provisionFileProbe{name: filepath.Join(t.TempDir(), "rename.tmp")}
	provisionCreateTemp = func(string, string) (provisionFile, error) { return probe, nil }
	provisionRename = func(string, string) error { return errors.New("rename failed") }
	if err := writeManifestAtomic(filepath.Join(t.TempDir(), "manifest.json"), manifest); err == nil || !strings.Contains(err.Error(), "install Runtime manifest") {
		t.Fatalf("rename error=%v", err)
	}

	originalOpen := provisionOpenAuditFile
	t.Cleanup(func() { provisionOpenAuditFile = originalOpen })
	provisionOpenAuditFile = func(string, int, os.FileMode) (provisionAuditFile, error) {
		return &provisionAuditFileProbe{writeErr: errors.New("audit write failed")}, nil
	}
	server := NewServer(filepath.Join(t.TempDir(), "manifest.json"), "dev", testContractIdentity(), nil)
	if err := server.appendAudit(manifestRequest{Manifest: manifest}, manifest.ManifestHash, "failed", "test"); err == nil {
		t.Fatal("audit write failure ignored")
	}
}
